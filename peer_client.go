/*
Copyright 2018-2019 Mailgun Technologies Inc

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package gubernator

import (
	"context"
	"sync"

	"github.com/pkg/errors"
	"google.golang.org/grpc"
)

type PeerPicker interface {
	GetByPeerInfo(PeerInfo) *PeerClient
	Peers() []*PeerClient
	Get(string) (*PeerClient, error)
	New() PeerPicker
	Add(*PeerClient)
	Size() int // TODO: Might not be useful?
}

type peerStatus int

const (
	peerNotConnected peerStatus = iota
	peerConnected
	peerClosing
)

type PeerClient struct {
	client PeersV1Client
	conn   *grpc.ClientConn
	conf   BehaviorConfig
	queue  chan *request
	info   PeerInfo

	mutex  sync.RWMutex   // This mutex is for verifying the closing state of the client
	status peerStatus     // Keep the current status of the peer
	wg     sync.WaitGroup // This wait group is to monitor the number of in-flight requests
}

type response struct {
	rl  *RateLimitResp
	err error
}

type request struct {
	request *RateLimitReq
	resp    chan *response
}

func NewPeerClient(conf BehaviorConfig, info PeerInfo) *PeerClient {
	return &PeerClient{
		queue:  make(chan *request, 1000),
		status: peerNotConnected,
		conf:   conf,
		info:   info,
	}
}

// Connect establishes a GRPC connection to a peer
func (c *PeerClient) Connect() error {
	// NOTE: To future self, this mutex is used here because we need to know if the peer is disconnecting and
	// handle ErrClosing. Since this mutex MUST be here we take this opportunity to also see if we are connected.
	// Doing this here encapsulates managing the connected state to the PeerClient struct. Previously a PeerClient
	// was connected when `NewPeerClient()` was called however, when adding support for multi data centers having a
	// PeerClient connected to every Peer in every data center continuously is not desirable, especially if nodes
	// in each region are configured to all have sisters.

	c.mutex.RLock()
	if c.status == peerClosing {
		c.mutex.RUnlock()
		return &PeerErr{err: errors.New("already disconnecting")}
	}

	if c.status == peerNotConnected {
		// This mutex stuff looks wonky, but it allows us to use RLock() 99% of the time, while the 1% where we
		// actually need to connect uses a full Lock(), using RLock() most of which should reduce the over head
		// of a full lock on every call

		// Yield the read lock so we can get the RW lock
		c.mutex.RUnlock()
		c.mutex.Lock()
		defer c.mutex.Unlock()

		// Now that we have the RW lock, ensure no else got here ahead of us.
		if c.status == peerConnected {
			return nil
		}

		var err error
		c.conn, err = grpc.Dial(c.info.Address, grpc.WithInsecure())
		if err != nil {
			return &PeerErr{err: errors.Wrapf(err, "failed to dial peer %s", c.info.Address)}
		}
		c.client = NewPeersV1Client(c.conn)
		c.status = peerConnected
		go c.run()
		return nil
	}
	c.mutex.RUnlock()
	return nil
}

// GetPeerRateLimit forwards a rate limit request to a peer. If the rate limit has `behavior == BATCHING` configured
// this method will attempt to batch the rate limits
func (c *PeerClient) GetPeerRateLimit(ctx context.Context, r *RateLimitReq) (*RateLimitResp, error) {
	// If config asked for no batching
	if HasBehavior(r.Behavior, Behavior_NO_BATCHING) {
		// Send a single low latency rate limit request
		resp, err := c.GetPeerRateLimits(ctx, &GetPeerRateLimitsReq{
			Requests: []*RateLimitReq{r},
		})
		if err != nil {
			return nil, err
		}
		return resp.RateLimits[0], nil
	}
	return c.getPeerRateLimitsBatch(ctx, r)
}

// GetPeerRateLimits requests a list of rate limit statuses from a peer
func (c *PeerClient) GetPeerRateLimits(ctx context.Context, r *GetPeerRateLimitsReq) (*GetPeerRateLimitsResp, error) {
	if err := c.Connect(); err != nil {
		return nil, err
	}

	c.wg.Add(1)
	defer c.wg.Done()

	resp, err := c.client.GetPeerRateLimits(ctx, r)
	if err != nil {
		return nil, err
	}

	// Unlikely, but this avoids a panic if something wonky happens
	if len(resp.RateLimits) != len(r.Requests) {
		return nil, errors.New("number of rate limits in peer response does not match request")
	}
	return resp, nil
}

// UpdatePeerGlobals sends global rate limit status updates to a peer
func (c *PeerClient) UpdatePeerGlobals(ctx context.Context, r *UpdatePeerGlobalsReq) (*UpdatePeerGlobalsResp, error) {
	if err := c.Connect(); err != nil {
		return nil, err
	}

	c.wg.Add(1)
	defer c.wg.Done()

	return c.client.UpdatePeerGlobals(ctx, r)
}

func (c *PeerClient) getPeerRateLimitsBatch(ctx context.Context, r *RateLimitReq) (*RateLimitResp, error) {
	if err := c.Connect(); err != nil {
		return nil, err
	}

	req := request{request: r, resp: make(chan *response, 1)}

	// Enqueue the request to be sent
	c.queue <- &req

	c.wg.Add(1)
	defer c.wg.Done()

	// Wait for a response or context cancel
	select {
	case resp := <-req.resp:
		if resp.err != nil {
			return nil, resp.err
		}
		return resp.rl, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// run waits for requests to be queued, when either c.batchWait time
// has elapsed or the queue reaches c.batchLimit. Send what is in the queue.
func (c *PeerClient) run() {
	var interval = NewInterval(c.conf.BatchWait)
	defer interval.Stop()

	var queue []*request

	for {
		select {
		case r, ok := <-c.queue:
			// If the queue has shutdown, we need to send the rest of the queue
			if !ok {
				if len(queue) > 0 {
					c.sendQueue(queue)
				}
				return
			}

			queue = append(queue, r)

			// Send the queue if we reached our batch limit
			if len(queue) == c.conf.BatchLimit {
				c.sendQueue(queue)
				queue = nil
				continue
			}

			// If this is our first queued item since last send
			// queue the next interval
			if len(queue) == 1 {
				interval.Next()
			}

		case <-interval.C:
			if len(queue) != 0 {
				c.sendQueue(queue)
				queue = nil
			}

		}
	}
}

// sendQueue sends the queue provided and returns the responses to
// waiting go routines
func (c *PeerClient) sendQueue(queue []*request) {
	var req GetPeerRateLimitsReq
	for _, r := range queue {
		req.Requests = append(req.Requests, r.request)
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.conf.BatchTimeout)
	resp, err := c.client.GetPeerRateLimits(ctx, &req)
	cancel()

	// An error here indicates the entire request failed
	if err != nil {
		for _, r := range queue {
			r.resp <- &response{err: err}
		}
		return
	}

	// Unlikely, but this avoids a panic if something wonky happens
	if len(resp.RateLimits) != len(queue) {
		err = errors.New("server responded with incorrect rate limit list size")
		for _, r := range queue {
			r.resp <- &response{err: err}
		}
		return
	}

	// Provide responses to channels waiting in the queue
	for i, r := range queue {
		r.resp <- &response{rl: resp.RateLimits[i]}
	}
}

// Shutdown will gracefully shutdown the client connection, until the context is cancelled
func (c *PeerClient) Shutdown(ctx context.Context) error {
	// Take the write lock since we're going to modify the closing state
	c.mutex.Lock()
	if c.status == peerClosing || c.status == peerNotConnected {
		c.mutex.Unlock()
		return nil
	}

	c.status = peerClosing
	// We need to close the chan here to prevent a possible race
	close(c.queue)

	c.mutex.Unlock()

	defer func() {
		if c.conn != nil {
			c.conn.Close()
		}
	}()

	// This allows us to wait on the waitgroup, or until the context
	// has been cancelled. This doesn't leak goroutines, because
	// closing the connection will kill any outstanding requests.
	waitChan := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(waitChan)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-waitChan:
		return nil
	}
}

// PeerErr is returned if the peer is not connected or is in a closing state
type PeerErr struct {
	err error
}

func (p *PeerErr) NotReady() bool {
	return true
}

func (p *PeerErr) Error() string {
	return p.err.Error()
}

func (p *PeerErr) Cause() error {
	return p.err
}

type notReadyErr interface {
	NotReady() bool
}

// IsNotReady returns true if the err is because the peer is not connected or in a closing state
func IsNotReady(err error) bool {
	te, ok := err.(notReadyErr)
	return ok && te.NotReady()
}
