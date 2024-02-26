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
	"time"

	"github.com/mailgun/holster/v4/clock"
	"github.com/mailgun/holster/v4/collections"
	"github.com/mailgun/holster/v4/errors"
	"github.com/mailgun/holster/v4/tracing"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

type PeerPicker interface {
	GetByPeerInfo(PeerInfo) *PeerClient
	Peers() []*PeerClient
	Get(string) (*PeerClient, error)
	New() PeerPicker
	Add(*PeerClient)
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
	request     *RateLimitReq
	resp        chan *response
	ctx         context.Context
	requestTime time.Time
}

type PeerConfig struct {
	TLS       *tls.Config
	Behavior  BehaviorConfig
	Info      PeerInfo
	Log       FieldLogger
	TraceGRPC bool
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
func (c *PeerClient) connect(ctx context.Context) (err error) {
	// NOTE: To future self, this mutex is used here because we need to know if the peer is disconnecting and
	// handle ErrClosing. Since this mutex MUST be here we take this opportunity to also see if we are connected.
	// Doing this here encapsulates managing the connected state to the PeerClient struct. Previously a PeerClient
	// was connected when `NewPeerClient()` was called however, when adding support for multi data centers having a
	// PeerClient connected to every Peer in every data center continuously is not desirable.

	funcTimer := prometheus.NewTimer(metricFuncTimeDuration.WithLabelValues("PeerClient.connect"))
	defer funcTimer.ObserveDuration()
	lockTimer := prometheus.NewTimer(metricFuncTimeDuration.WithLabelValues("PeerClient.connect_RLock"))

	c.mutex.RLock()
	lockTimer.ObserveDuration()

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

		// Setup OpenTelemetry interceptor to propagate spans.
		var opts []grpc.DialOption

		if c.conf.TraceGRPC {
			opts = []grpc.DialOption{
				grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
			}
		}

		if c.conf.TLS != nil {
			opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(c.conf.TLS)))
		} else {
			opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
		}

		var err error
		c.conn, err = grpc.Dial(c.conf.Info.GRPCAddress, opts...)
		if err != nil {
			return c.setLastErr(&PeerErr{err: errors.Wrapf(err, "failed to dial peer %s", c.conf.Info.GRPCAddress)})
		}
		c.client = NewPeersV1Client(c.conn)
		c.status = peerConnected

		if !c.conf.Behavior.DisableBatching {
			go c.runBatch()
		}
		return nil
	}
	c.mutex.RUnlock()
	return nil
}

// Info returns PeerInfo struct that describes this PeerClient
func (c *PeerClient) Info() PeerInfo {
	return c.conf.Info
}

// GetPeerRateLimit forwards a rate limit request to a peer. If the rate limit has `behavior == BATCHING` configured,
// this method will attempt to batch the rate limits
func (c *PeerClient) GetPeerRateLimit(ctx context.Context, r *RateLimitReq) (resp *RateLimitResp, err error) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("ratelimit.key", r.UniqueKey),
		attribute.String("ratelimit.name", r.Name),
	)

	// If config asked for no batching
	if c.conf.Behavior.DisableBatching || HasBehavior(r.Behavior, Behavior_NO_BATCHING) {
		// If no metadata is provided
		if r.Metadata == nil {
			r.Metadata = make(map[string]string)
		}
		// Propagate the trace context along with the rate limit so
		// peers can continue to report traces for this rate limit.
		prop := propagation.TraceContext{}
		prop.Inject(ctx, &MetadataCarrier{Map: r.Metadata})

		// Send a single low latency rate limit request
		resp, err := c.GetPeerRateLimits(ctx, &GetPeerRateLimitsReq{
			Requests: []*RateLimitReq{r},
		})
		if err != nil {
			err = errors.Wrap(err, "Error in GetPeerRateLimits")
			return nil, c.setLastErr(err)
		}
		return resp.RateLimits[0], nil
	}

	resp, err = c.getPeerRateLimitsBatch(ctx, r)
	if err != nil {
		err = errors.Wrap(err, "Error in getPeerRateLimitsBatch")
		return nil, c.setLastErr(err)
	}

	return resp, nil
}

// GetPeerRateLimits requests a list of rate limit statuses from a peer
func (c *PeerClient) GetPeerRateLimits(ctx context.Context, r *GetPeerRateLimitsReq) (resp *GetPeerRateLimitsResp, err error) {
	if err := c.connect(ctx); err != nil {
		err = errors.Wrap(err, "Error in connect")
		metricCheckErrorCounter.WithLabelValues("Connect error").Add(1)
		return nil, c.setLastErr(err)
	}

	// NOTE: This must be done within the RLock since calling Wait() in Shutdown() causes
	// a race condition if called within a separate go routine if the internal wg is `0`
	// when Wait() is called then Add(1) is called concurrently.
	c.mutex.RLock()
	c.wg.Add(1)
	defer func() {
		c.mutex.RUnlock()
		defer c.wg.Done()
	}()

	resp, err = c.client.GetPeerRateLimits(ctx, r)
	if err != nil {
		err = errors.Wrap(err, "Error in client.GetPeerRateLimits")
		// metricCheckErrorCounter is updated within client.GetPeerRateLimits().
		return nil, c.setLastErr(err)
	}

	// Unlikely, but this avoids a panic if something wonky happens
	if len(resp.RateLimits) != len(r.Requests) {
		err = errors.New("number of rate limits in peer response does not match request")
		metricCheckErrorCounter.WithLabelValues("Item mismatch").Add(1)
		return nil, c.setLastErr(err)
	}
	return resp, nil
}

// UpdatePeerGlobals sends global rate limit status updates to a peer
func (c *PeerClient) UpdatePeerGlobals(ctx context.Context, r *UpdatePeerGlobalsReq) (resp *UpdatePeerGlobalsResp, err error) {
	if err := c.connect(ctx); err != nil {
		return nil, c.setLastErr(err)
	}

	// See NOTE above about RLock and wg.Add(1)
	c.mutex.RLock()
	c.wg.Add(1)
	defer func() {
		c.mutex.RUnlock()
		defer c.wg.Done()
	}()

	resp, err = c.client.UpdatePeerGlobals(ctx, r)
	if err != nil {
		_ = c.setLastErr(err)
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

func (c *PeerClient) getPeerRateLimitsBatch(ctx context.Context, r *RateLimitReq) (resp *RateLimitResp, err error) {
	funcTimer := prometheus.NewTimer(metricFuncTimeDuration.WithLabelValues("PeerClient.getPeerRateLimitsBatch"))
	defer funcTimer.ObserveDuration()

	if err := c.connect(ctx); err != nil {
		err = errors.Wrap(err, "Error in connect")
		return nil, c.setLastErr(err)
	}

	// See NOTE above about RLock and wg.Add(1)
	c.mutex.RLock()
	if c.status == peerClosing {
		err := &PeerErr{err: errors.New("already disconnecting")}
		return nil, c.setLastErr(err)
	}

	// Wait for a response or context cancel
	req := request{
		resp:    make(chan *response, 1),
		ctx:     ctx,
		request: r,
	}

	// Enqueue the request to be sent
	peerAddr := c.Info().GRPCAddress
	metricBatchQueueLength.WithLabelValues(peerAddr).Set(float64(len(c.queue)))

	select {
	case c.queue <- &req:
		// Successfully enqueued request.
	case <-ctx.Done():
		return nil, errors.Wrap(ctx.Err(), "Context error while enqueuing request")
	}

	c.wg.Add(1)
	defer func() {
		c.mutex.RUnlock()
		c.wg.Done()
	}()

	select {
	case re := <-req.resp:
		if re.err != nil {
			err := errors.Wrap(c.setLastErr(re.err), "Request error")
			return nil, c.setLastErr(err)
		}
		return re.rl, nil
	case <-ctx.Done():
		return nil, errors.Wrap(ctx.Err(), "Context error while waiting for response")
	}
}

// run processes batching requests by waiting for requests to be queued.  Send
// the queue as a batch when either c.batchWait time has elapsed or the queue
// reaches c.batchLimit.
func (c *PeerClient) runBatch() {
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
					c.sendBatch(ctx, queue)
				}
				return
			}

			queue = append(queue, r)
			// Send the queue if we reached our batch limit
			if len(queue) >= c.conf.Behavior.BatchLimit {
				c.conf.Log.WithContext(ctx).
					WithFields(logrus.Fields{
						"queueLen":   len(queue),
						"batchLimit": c.conf.Behavior.BatchLimit,
					}).
					Debug("runBatch() reached batch limit")
				ref := queue
				queue = nil
				go c.sendBatch(ctx, ref)
				continue
			}

			// If this is our first enqueued item since last
			// sendBatch, reset interval timer.
			if len(queue) == 1 {
				interval.Next()
			}
			continue

		case <-interval.C:
			queue2 := queue

			if len(queue2) > 0 {
				queue = nil

				go func() {
					c.sendBatch(ctx, queue2)
				}()
			}
		}
	}
}

// sendBatch sends the queue provided and returns the responses to
// waiting go routines
func (c *PeerClient) sendBatch(ctx context.Context, queue []*request) {
	batchSendTimer := prometheus.NewTimer(metricBatchSendDuration.WithLabelValues(c.conf.Info.GRPCAddress))
	defer batchSendTimer.ObserveDuration()
	funcTimer := prometheus.NewTimer(metricFuncTimeDuration.WithLabelValues("PeerClient.sendBatch"))
	defer funcTimer.ObserveDuration()

	var req GetPeerRateLimitsReq
	for _, r := range queue {
		// NOTE: This trace has the same name because it's in a separate trace than the one above.
		// We link the two traces, so we can relate our rate limit trace back to the above trace.
		r.ctx = tracing.StartNamedScope(r.ctx, "PeerClient.sendBatch",
			trace.WithLinks(trace.LinkFromContext(ctx)))
		// If no metadata is provided
		if r.request.Metadata == nil {
			r.request.Metadata = make(map[string]string)
		}
		// Propagate the trace context along with the batched rate limit so
		// peers can continue to report traces for this rate limit.
		prop := propagation.TraceContext{}
		prop.Inject(r.ctx, &MetadataCarrier{Map: r.request.Metadata})
		req.Requests = append(req.Requests, r.request)
		tracing.EndScope(r.ctx, nil)

	}

	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, c.conf.Behavior.BatchTimeout)
	resp, err := c.client.GetPeerRateLimits(timeoutCtx, &req)
	timeoutCancel()

	// An error here indicates the entire request failed
	if err != nil {
		logPart := "Error in client.GetPeerRateLimits"
		c.conf.Log.WithContext(ctx).
			WithError(err).
			WithFields(logrus.Fields{
				"queueLen":     len(queue),
				"batchTimeout": c.conf.Behavior.BatchTimeout.String(),
			}).
			Error(logPart)
		err = errors.Wrap(err, logPart)
		_ = c.setLastErr(err)
		// metricCheckErrorCounter is updated within client.GetPeerRateLimits().

		for _, r := range queue {
			r.resp <- &response{err: err}
		}
		return
	}

	// Unlikely, but this avoids a panic if something wonky happens
	if len(resp.RateLimits) != len(queue) {
		err = errors.New("server responded with incorrect rate limit list size")

		for _, r := range queue {
			metricCheckErrorCounter.WithLabelValues("Item mismatch").Add(1)
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
		close(c.queue)
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
