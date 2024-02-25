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
	"sync/atomic"

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

type PeerClient struct {
	client      PeersV1Client
	conn        *grpc.ClientConn
	conf        PeerConfig
	queue       chan *request
	queueClosed atomic.Bool
	lastErrs    *collections.LRUCache

	wgMutex sync.RWMutex
	wg      sync.WaitGroup // Monitor the number of in-flight requests. GUARDED_BY(wgMutex)
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
	TLS       *tls.Config
	Behavior  BehaviorConfig
	Info      PeerInfo
	Log       FieldLogger
	TraceGRPC bool
}

// NewPeerClient establishes a connection to a peer in a non-blocking fashion.
// If batching is enabled, it also starts a goroutine where batches will be processed.
func NewPeerClient(conf PeerConfig) (*PeerClient, error) {
	peerClient := &PeerClient{
		queue:    make(chan *request, 1000),
		conf:     conf,
		lastErrs: collections.NewLRUCache(100),
	}
	var opts []grpc.DialOption

	if conf.TraceGRPC {
		opts = []grpc.DialOption{
			grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		}
	}

	if conf.TLS != nil {
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(conf.TLS)))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	var err error
	peerClient.conn, err = grpc.Dial(conf.Info.GRPCAddress, opts...)
	if err != nil {
		return nil, err
	}
	peerClient.client = NewPeersV1Client(peerClient.conn)

	if !conf.Behavior.DisableBatching {
		go peerClient.runBatch()
	}
	return peerClient, nil
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
	// NOTE: This must be done within the Lock since calling Wait() in Shutdown() causes
	// a race condition if called within a separate go routine if the internal wg is `0`
	// when Wait() is called then Add(1) is called concurrently.
	c.wgMutex.Lock()
	c.wg.Add(1)
	c.wgMutex.Unlock()
	defer c.wg.Done()

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

	// See NOTE above about RLock and wg.Add(1)
	c.wgMutex.Lock()
	c.wg.Add(1)
	c.wgMutex.Unlock()
	defer c.wg.Done()

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

	req := request{
		resp:    make(chan *response, 1),
		ctx:     ctx,
		request: r,
	}

	c.wgMutex.Lock()
	c.wg.Add(1)
	c.wgMutex.Unlock()
	defer c.wg.Done()

	// Enqueue the request to be sent
	peerAddr := c.Info().GRPCAddress
	metricBatchQueueLength.WithLabelValues(peerAddr).Set(float64(len(c.queue)))

	if c.queueClosed.Load() {
		// this check prevents "panic: send on close channel"
		return nil, grpc.ErrClientConnClosing
	}

	select {
	case c.queue <- &req:
		// Successfully enqueued request.
	case <-ctx.Done():
		return nil, errors.Wrap(ctx.Err(), "Context error while enqueuing request")
	}

	// Wait for a response or context cancel
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

// runBatch processes batching requests by waiting for requests to be queued.  Send
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
			if !ok {
				// If the queue has shutdown, we need to send the rest of the queue
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

// Shutdown waits until all outstanding requests have finished and then closes the grpc connection
func (c *PeerClient) Shutdown() {
	// drain in-flight requests
	c.wgMutex.Lock()
	defer c.wgMutex.Unlock()
	c.wg.Wait()

	// signal that no more items will be sent
	c.queueClosed.Store(true)
	close(c.queue)

	c.conn.Close()
}
