/*
Copyright 2018-2022 Mailgun Technologies Inc

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
	"crypto/tls"
	"fmt"
	"sync"

	"github.com/mailgun/gubernator/v2/tracing"
	"github.com/mailgun/holster/v4/clock"
	"github.com/mailgun/holster/v4/collections"
	otgrpc "github.com/opentracing-contrib/go-grpc"
	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
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
	client   PeersV1Client
	conn     *grpc.ClientConn
	conf     PeerConfig
	queue    chan *request
	lastErrs *collections.LRUCache

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
	ctx     context.Context
}

type PeerConfig struct {
	TLS      *tls.Config
	Behavior BehaviorConfig
	Info     PeerInfo
}

func NewPeerClient(conf PeerConfig) *PeerClient {
	return &PeerClient{
		queue:    make(chan *request, 1000),
		status:   peerNotConnected,
		conf:     conf,
		lastErrs: collections.NewLRUCache(100),
	}
}

// Connect establishes a GRPC connection to a peer
func (c *PeerClient) connect(ctx context.Context) error {
	span, ctx := tracing.StartSpan(ctx)
	defer span.Finish()

	// NOTE: To future self, this mutex is used here because we need to know if the peer is disconnecting and
	// handle ErrClosing. Since this mutex MUST be here we take this opportunity to also see if we are connected.
	// Doing this here encapsulates managing the connected state to the PeerClient struct. Previously a PeerClient
	// was connected when `NewPeerClient()` was called however, when adding support for multi data centers having a
	// PeerClient connected to every Peer in every data center continuously is not desirable.

	funcTimer := prometheus.NewTimer(funcTimeMetric.WithLabelValues("PeerClient.connect"))
	defer funcTimer.ObserveDuration()
	lockTimer := prometheus.NewTimer(funcTimeMetric.WithLabelValues("PeerClient.connect_RLock"))

	c.mutex.RLock()
	lockTimer.ObserveDuration()
	tracing.LogInfo(span, "mutex.RLock()")

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
		tracing.LogInfo(span, "mutex.Lock()")

		// Now that we have the RW lock, ensure no else got here ahead of us.
		if c.status == peerConnected {
			return nil
		}

		// Setup Opentracing interceptor to propagate spans.
		tracer := opentracing.GlobalTracer()
		tracingUnaryInterceptor := otgrpc.OpenTracingClientInterceptor(tracer)
		tracingStreamInterceptor := otgrpc.OpenTracingStreamClientInterceptor(tracer)

		var err error
		opts := []grpc.DialOption{
			grpc.WithUnaryInterceptor(tracingUnaryInterceptor),
			grpc.WithStreamInterceptor(tracingStreamInterceptor),
		}

		if c.conf.TLS != nil {
			opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(c.conf.TLS)))
		} else {
			opts = append(opts, grpc.WithInsecure())
		}

		c.conn, err = grpc.Dial(c.conf.Info.GRPCAddress, opts...)
		if err != nil {
			return c.setLastErr(&PeerErr{err: errors.Wrapf(err, "failed to dial peer %s", c.conf.Info.GRPCAddress)})
		}
		c.client = NewPeersV1Client(c.conn)
		c.status = peerConnected
		go c.run()
		return nil
	}
	c.mutex.RUnlock()
	return nil
}

// Info returns PeerInfo struct that describes this PeerClient
func (c *PeerClient) Info() PeerInfo {
	return c.conf.Info
}

// GetPeerRateLimit forwards a rate limit request to a peer. If the rate limit has `behavior == BATCHING` configured
// this method will attempt to batch the rate limits
func (c *PeerClient) GetPeerRateLimit(ctx context.Context, r *RateLimitReq) (*RateLimitResp, error) {
	span, ctx := tracing.StartSpan(ctx)
	defer span.Finish()
	span.SetTag("request.name", r.Name)
	span.SetTag("request.key", r.UniqueKey)
	span.SetTag("request.limit", r.Limit)
	span.SetTag("request.duration", r.Duration)

	// If config asked for no batching
	if HasBehavior(r.Behavior, Behavior_NO_BATCHING) {
		// Send a single low latency rate limit request
		resp, err := c.GetPeerRateLimits(ctx, &GetPeerRateLimitsReq{
			Requests: []*RateLimitReq{r},
		})
		if err != nil {
			err2 := errors.Wrap(err, "Error in GetPeerRateLimits")
			ext.LogError(span, err2)
			return nil, c.setLastErr(err2)
		}
		return resp.RateLimits[0], nil
	}

	rateLimitResp, err := c.getPeerRateLimitsBatch(ctx, r)
	if err != nil {
		err2 := errors.Wrapf(err, "Error in getPeerRateLimitsBatch while sending batch to peer %s", c.conf.Info.GRPCAddress)
		ext.LogError(span, err2)
		return nil, c.setLastErr(err2)
	}

	return rateLimitResp, nil
}

// GetPeerRateLimits requests a list of rate limit statuses from a peer
func (c *PeerClient) GetPeerRateLimits(ctx context.Context, r *GetPeerRateLimitsReq) (*GetPeerRateLimitsResp, error) {
	span, ctx := tracing.StartSpan(ctx)
	defer span.Finish()
	span.SetTag("numRequests", len(r.Requests))

	if err := c.connect(ctx); err != nil {
		err2 := errors.Wrap(err, "Error in connect")
		ext.LogError(span, err2)
		checkErrorCounter.WithLabelValues("Connect error").Add(1)
		return nil, c.setLastErr(err2)
	}

	// NOTE: This must be done within the RLock since calling Wait() in Shutdown() causes
	// a race condition if called within a separate go routine if the internal wg is `0`
	// when Wait() is called then Add(1) is called concurrently.
	c.mutex.RLock()
	tracing.LogInfo(span, "mutex.RLock()")
	c.wg.Add(1)
	defer func() {
		c.mutex.RUnlock()
		defer c.wg.Done()
	}()

	resp, err := c.client.GetPeerRateLimits(ctx, r)
	if err != nil {
		err2 := errors.Wrap(err, "Error in client.GetPeerRateLimits")
		ext.LogError(span, err2)
		// checkErrorCounter is updated within client.GetPeerRateLimits().
		return nil, c.setLastErr(err2)
	}

	// Unlikely, but this avoids a panic if something wonky happens
	if len(resp.RateLimits) != len(r.Requests) {
		err = errors.New("number of rate limits in peer response does not match request")
		ext.LogError(span, err)
		checkErrorCounter.WithLabelValues("Item mismatch").Add(1)
		return nil, c.setLastErr(err)
	}
	return resp, nil
}

// UpdatePeerGlobals sends global rate limit status updates to a peer
func (c *PeerClient) UpdatePeerGlobals(ctx context.Context, r *UpdatePeerGlobalsReq) (*UpdatePeerGlobalsResp, error) {
	span, ctx := tracing.StartSpan(ctx)
	defer span.Finish()

	if err := c.connect(ctx); err != nil {
		return nil, c.setLastErr(err)
	}

	// See NOTE above about RLock and wg.Add(1)
	c.mutex.RLock()
	tracing.LogInfo(span, "mutex.RLock()")
	c.wg.Add(1)
	defer func() {
		c.mutex.RUnlock()
		defer c.wg.Done()
	}()

	resp, err := c.client.UpdatePeerGlobals(ctx, r)
	if err != nil {
		c.setLastErr(err)
	}

	return resp, err
}

func (c *PeerClient) setLastErr(err error) error {
	// If we get a nil error return without caching it
	if err == nil {
		return err
	}

	// Prepend client address to error
	errWithHostname := errors.Wrap(err, fmt.Sprintf("from host %s", c.conf.Info.GRPCAddress))
	key := err.Error()

	// Add error to the cache with a TTL of 5 minutes
	c.lastErrs.AddWithTTL(key, errWithHostname, clock.Minute*5)

	return err
}

func (c *PeerClient) GetLastErr() []string {
	var errs []string
	keys := c.lastErrs.Keys()

	// Get errors from each key in the cache
	for _, key := range keys {
		err, ok := c.lastErrs.Get(key)
		if ok {
			errs = append(errs, err.(error).Error())
		}
	}

	return errs
}

func (c *PeerClient) getPeerRateLimitsBatch(ctx context.Context, r *RateLimitReq) (*RateLimitResp, error) {
	span, ctx := tracing.StartSpan(ctx)
	defer span.Finish()
	span.SetTag("request.name", r.Name)
	span.SetTag("request.key", r.UniqueKey)
	span.SetTag("request.limit", r.Limit)
	span.SetTag("request.duration", r.Duration)

	funcTimer := prometheus.NewTimer(funcTimeMetric.WithLabelValues("PeerClient.getPeerRateLimitsBatch"))
	defer funcTimer.ObserveDuration()

	if err := c.connect(ctx); err != nil {
		err2 := errors.Wrap(err, "Error in connect")
		ext.LogError(span, err2)
		return nil, c.setLastErr(err2)
	}

	// See NOTE above about RLock and wg.Add(1)
	c.mutex.RLock()
	tracing.LogInfo(span, "mutex.RLock()")
	if c.status == peerClosing {
		err2 := &PeerErr{err: errors.New("already disconnecting")}
		ext.LogError(span, err2)
		return nil, c.setLastErr(err2)
	}
	req := request{
		request: r,
		resp:    make(chan *response, 1),
		ctx:     ctx,
	}

	// Enqueue the request to be sent
	tracing.LogInfo(span, "Enqueue request", "queueLength", len(c.queue))
	peerAddr := c.Info().GRPCAddress
	queueLengthMetric.WithLabelValues(peerAddr).Observe(float64(len(c.queue)))

	select {
	case c.queue <- &req:
		// Successfully enqueued request.
	case <-ctx.Done():
		err := errors.Wrap(ctx.Err(), "Error while enqueuing request")
		ext.LogError(span, err)
		return nil, err
	}

	c.wg.Add(1)
	defer func() {
		c.mutex.RUnlock()
		c.wg.Done()
	}()

	// Wait for a response or context cancel
	span3, ctx2 := tracing.StartNamedSpan(ctx, "Wait for response")
	defer span3.Finish()

	select {
	case resp := <-req.resp:
		if resp.err != nil {
			err2 := errors.Wrap(c.setLastErr(resp.err), "Request error")
			ext.LogError(span, err2)
			return nil, c.setLastErr(err2)
		}
		return resp.rl, nil
	case <-ctx2.Done():
		err := errors.Wrap(ctx2.Err(), "Error while waiting for response")
		ext.LogError(span, err)
		return nil, err
	}
}

// run waits for requests to be queued, when either c.batchWait time
// has elapsed or the queue reaches c.batchLimit. Send what is in the queue.
func (c *PeerClient) run() {
	var interval = NewInterval(c.conf.Behavior.BatchWait)
	defer interval.Stop()

	var queue []*request

	for {
		ctx := context.Background()

		select {
		case r, ok := <-c.queue:
			// If the queue has shutdown, we need to send the rest of the queue
			if !ok {
				if len(queue) > 0 {
					c.sendQueue(ctx, queue)
				}
				return
			}

			// Wrap logic in anon function so we can use defer.
			func() {
				// Use context of the request for opentracing span.
				flushSpan, _ := tracing.StartNamedSpan(r.ctx, "Enqueue batched request")
				defer flushSpan.Finish()
				flushSpan.SetTag("peer.grpcAddress", c.conf.Info.GRPCAddress)

				queue = append(queue, r)

				// Send the queue if we reached our batch limit
				if len(queue) >= c.conf.Behavior.BatchLimit {
					logMsg := "run() reached batch limit"
					logrus.WithFields(logrus.Fields{
						"queueLen":   len(queue),
						"batchLimit": c.conf.Behavior.BatchLimit,
					}).Info(logMsg)
					tracing.LogInfo(flushSpan, logMsg)

					go c.sendQueue(ctx, queue)
					queue = nil
					interval.Next()
					return
				}

				// If this is our first enqueued item since last
				// sendQueue, reset interval timer.
				if len(queue) == 1 {
					interval.Next()
				}
			}()

		case <-interval.C:
			if len(queue) != 0 {
				go c.sendQueue(ctx, queue)
				queue = nil
			}
		}
	}
}

// sendQueue sends the queue provided and returns the responses to
// waiting go routines
func (c *PeerClient) sendQueue(ctx context.Context, queue []*request) {
	span, ctx := tracing.StartSpan(ctx)
	defer span.Finish()
	span.SetTag("queueLen", len(queue))
	span.SetTag("peer.grpcAddress", c.conf.Info.GRPCAddress)

	batchSendTimer := prometheus.NewTimer(batchSendDurationMetric.WithLabelValues(c.conf.Info.GRPCAddress))
	defer batchSendTimer.ObserveDuration()
	funcTimer := prometheus.NewTimer(funcTimeMetric.WithLabelValues("PeerClient.sendQueue"))
	defer funcTimer.ObserveDuration()

	var req GetPeerRateLimitsReq
	for _, r := range queue {
		req.Requests = append(req.Requests, r.request)
	}

	ctx2, cancel2 := tracing.ContextWithTimeout(ctx, c.conf.Behavior.BatchTimeout)
	resp, err := c.client.GetPeerRateLimits(ctx2, &req)
	cancel2()

	// An error here indicates the entire request failed
	if err != nil {
		logPart := "Error in client.GetPeerRateLimits"
		err2 := errors.Wrap(err, logPart)
		logrus.
			WithError(err).
			WithFields(logrus.Fields{
				"queueLen":     len(queue),
				"batchTimeout": c.conf.Behavior.BatchTimeout.String(),
			}).
			Error(logPart)
		ext.LogError(span, err2)
		c.setLastErr(err2)
		// checkErrorCounter is updated within client.GetPeerRateLimits().

		for _, r := range queue {
			r.resp <- &response{err: err}
		}
		return
	}

	// Unlikely, but this avoids a panic if something wonky happens
	if len(resp.RateLimits) != len(queue) {
		err = errors.New("server responded with incorrect rate limit list size")
		ext.LogError(span, err)

		for _, r := range queue {
			checkErrorCounter.WithLabelValues("Item mismatch").Add(1)
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
	defer c.mutex.Unlock()

	c.status = peerClosing
	// We need to close the chan here to prevent a possible race
	close(c.queue)

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
