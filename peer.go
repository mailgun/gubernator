/*
Copyright 2018-2023 Mailgun Technologies Inc

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
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/mailgun/errors"
	"github.com/mailgun/holster/v4/clock"
	"github.com/mailgun/holster/v4/collections"
	"github.com/mailgun/holster/v4/ctxutil"
	"github.com/mailgun/holster/v4/setter"
	"github.com/mailgun/holster/v4/tracing"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type Peer struct {
	lastErrs   *collections.LRUCache
	wg         sync.WaitGroup
	queue      chan *request
	mutex      sync.RWMutex
	client     PeerClient
	conf       PeerConfig
	inShutdown int64
}

type response struct {
	rl  *RateLimitResponse
	err error
}

type request struct {
	request *RateLimitRequest
	resp    chan *response
	ctx     context.Context
}

type PeerConfig struct {
	PeerClient PeerClient
	Behavior   BehaviorConfig
	Info       PeerInfo
	Log        FieldLogger
}

type PeerClient interface {
	Forward(context.Context, *ForwardRequest, *ForwardResponse) error
	Update(context.Context, *UpdateRequest) error
}

func NewPeer(conf PeerConfig) (*Peer, error) {
	if len(conf.Info.HTTPAddress) == 0 {
		return nil, errors.New("Peer.Info.HTTPAddress is empty; must provide an address")
	}

	setter.SetDefault(&conf.PeerClient, NewPeerClient(WithNoTLS(conf.Info.HTTPAddress)))
	setter.SetDefault(&conf.Log, logrus.WithField("category", "Peer"))

	p := &Peer{
		lastErrs: collections.NewLRUCache(100),
		queue:    make(chan *request, 1000),
		client:   conf.PeerClient,
		conf:     conf,
	}
	go p.run()
	return p, nil
}

// Info returns PeerInfo struct that describes this Peer
func (p *Peer) Info() PeerInfo {
	return p.conf.Info
}

var (
	// TODO: Should retry in this case
	ErrPeerShutdown = errors.New("peer is shutdown; try a different peer")
)

// Forward forwards a rate limit request to a peer.
// If the rate limit has `behavior == BATCHING` configured, this method will attempt to batch the rate limits
func (p *Peer) Forward(ctx context.Context, r *RateLimitRequest) (resp *RateLimitResponse, err error) {
	ctx = tracing.StartNamedScope(ctx, "Peer.Forward")
	defer func() { tracing.EndScope(ctx, err) }()
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("peer.HTTPAddress", p.conf.Info.HTTPAddress),
		attribute.String("peer.Datacenter", p.conf.Info.DataCenter),
		attribute.String("request.key", r.UniqueKey),
		attribute.String("request.name", r.Name),
		attribute.Int64("request.algorithm", int64(r.Algorithm)),
		attribute.Int64("request.behavior", int64(r.Behavior)),
		attribute.Int64("request.duration", r.Duration),
		attribute.Int64("request.limit", r.Limit),
		attribute.Int64("request.hits", r.Hits),
		attribute.Int64("request.burst", r.Burst),
	)

	if atomic.LoadInt64(&p.inShutdown) == 1 {
		return nil, ErrPeerShutdown
	}

	// NOTE: Add() must be done within the RLock since we must ensure all in-flight Forward()
	// requests are done before calls to Close() can complete. We can't just wg.Wait() for
	// since there may be Forward() call that is executing at this very code spot when Close()
	// is called. In that scenario wg.Add() and wg.Wait() are in a race.
	p.mutex.RLock()
	p.wg.Add(1)
	defer func() {
		p.mutex.RUnlock()
		defer p.wg.Done()
	}()

	// If config asked for no batching
	if HasBehavior(r.Behavior, Behavior_NO_BATCHING) {
		// If no metadata is provided
		if r.Metadata == nil {
			r.Metadata = make(map[string]string)
		}
		// Propagate the trace context along with the rate limit so
		// peers can continue to report traces for this rate limit.
		prop := propagation.TraceContext{}
		prop.Inject(ctx, &MetadataCarrier{Map: r.Metadata})

		// Forward a single rate limit
		var fr ForwardResponse
		err = p.ForwardBatch(ctx, &ForwardRequest{
			Requests: []*RateLimitRequest{r},
		}, &fr)
		if err != nil {
			err = errors.Wrap(err, "Error in forward")
			return nil, p.setLastErr(err)
		}
		return fr.RateLimits[0], nil
	}

	resp, err = p.forwardBatch(ctx, r)
	if err != nil {
		err = errors.Wrap(err, "Error in forwardBatch")
		return nil, p.setLastErr(err)
	}

	return resp, nil
}

// ForwardBatch requests a list of rate limit statuses from a peer
func (p *Peer) ForwardBatch(ctx context.Context, req *ForwardRequest, resp *ForwardResponse) (err error) {
	ctx = tracing.StartNamedScopeDebug(ctx, "Peer.forward")
	defer func() { tracing.EndScope(ctx, err) }()

	if err = p.client.Forward(ctx, req, resp); err != nil {
		return p.setLastErr(errors.Wrap(err, "Error in client.Forward()"))
	}

	// Unlikely, but this avoids a panic if something wonky happens
	if len(resp.RateLimits) != len(req.Requests) {
		return p.setLastErr(
			errors.New("number of rate limits in peer response does not match request"))
	}
	return nil
}

// Update sends rate limit status updates to a peer
func (p *Peer) Update(ctx context.Context, req *UpdateRequest) (err error) {
	ctx = tracing.StartNamedScope(ctx, "Peer.Update")
	defer func() { tracing.EndScope(ctx, err) }()

	err = p.client.Update(ctx, req)
	if err != nil {
		_ = p.setLastErr(err)
	}
	return err
}

func (p *Peer) GetLastErr() []string {
	var errs []string
	keys := p.lastErrs.Keys()

	// Get errors from each key in the cache
	for _, key := range keys {
		err, ok := p.lastErrs.Get(key)
		if ok {
			errs = append(errs, err.(error).Error())
		}
	}

	return errs
}

// Close will gracefully close all client connections, until the context is canceled
func (p *Peer) Close(ctx context.Context) error {
	if atomic.LoadInt64(&p.inShutdown) == 1 {
		return nil
	}

	atomic.AddInt64(&p.inShutdown, 1)

	// This allows us to wait on the wait group, or until the context
	// has been canceled.
	waitChan := make(chan struct{})
	go func() {
		p.mutex.Lock()
		p.wg.Wait()
		close(p.queue)
		p.mutex.Unlock()
		close(waitChan)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-waitChan:
		return nil
	}
}

func (p *Peer) forwardBatch(ctx context.Context, r *RateLimitRequest) (resp *RateLimitResponse, err error) {
	ctx = tracing.StartNamedScopeDebug(ctx, "Peer.forwardBatch")
	defer func() { tracing.EndScope(ctx, err) }()

	funcTimer := prometheus.NewTimer(metricFuncTimeDuration.WithLabelValues("Peer.forwardBatch"))
	defer funcTimer.ObserveDuration()

	if atomic.LoadInt64(&p.inShutdown) == 1 {
		return nil, p.setLastErr(&ErrNotReady{err: errors.New("already disconnecting")})
	}

	// Wait for a response or context cancel
	ctx2 := tracing.StartNamedScopeDebug(ctx, "Wait for response")
	defer tracing.EndScope(ctx2, nil)

	req := request{
		resp:    make(chan *response, 1),
		ctx:     ctx2,
		request: r,
	}

	// Enqueue the request to be sent
	peerAddr := p.Info().HTTPAddress
	metricBatchQueueLength.WithLabelValues(peerAddr).Set(float64(len(p.queue)))

	select {
	case p.queue <- &req:
		// Successfully enqueued request.
	case <-ctx2.Done():
		return nil, errors.Wrap(ctx2.Err(), "Context error while enqueuing request")
	}

	p.wg.Add(1)
	defer func() {
		p.wg.Done()
	}()

	select {
	case re := <-req.resp:
		if re.err != nil {
			err := errors.Wrap(p.setLastErr(re.err), "Request error")
			return nil, p.setLastErr(err)
		}
		return re.rl, nil
	case <-ctx2.Done():
		return nil, errors.Wrap(ctx2.Err(), "Context error while waiting for response")
	}
}

// run waits for requests to be queued, when either c.batchWait time
// has elapsed or the queue reaches c.batchLimit. Send what is in the queue.
func (p *Peer) run() {
	var interval = NewInterval(p.conf.Behavior.BatchWait)
	defer interval.Stop()

	var queue []*request

	for {
		select {
		case r, ok := <-p.queue:
			// If the queue has closed, we need to send the rest of the queue
			if !ok {
				if len(queue) > 0 {
					p.sendBatch(queue)
				}
				return
			}

			queue = append(queue, r)
			// Send the queue if we reached our batch limit
			if len(queue) >= p.conf.Behavior.BatchLimit {
				p.conf.Log.WithFields(logrus.Fields{
					"queueLen":   len(queue),
					"batchLimit": p.conf.Behavior.BatchLimit,
				}).Debug("run() reached batch limit")
				ref := queue
				queue = nil
				go p.sendBatch(ref)
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
				go p.sendBatch(queue2)
			}
		}
	}
}

// sendBatch sends the queue provided and returns the responses to
// waiting go routines
func (p *Peer) sendBatch(queue []*request) {
	ctx := tracing.StartNamedScopeDebug(context.Background(), "Peer.sendBatch")
	defer tracing.EndScope(ctx, nil)

	batchSendTimer := prometheus.NewTimer(metricBatchSendDuration.WithLabelValues(p.conf.Info.HTTPAddress))
	defer batchSendTimer.ObserveDuration()
	funcTimer := prometheus.NewTimer(metricFuncTimeDuration.WithLabelValues("Peer.sendBatch"))
	defer funcTimer.ObserveDuration()

	var req ForwardRequest
	for _, r := range queue {
		// NOTE: This trace has the same name because it's in a separate trace than the one above.
		// We link the two traces, so we can relate our rate limit trace back to the above trace.
		r.ctx = tracing.StartNamedScopeDebug(r.ctx, "Peer.sendBatch",
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

	ctx, cancel := ctxutil.WithTimeout(ctx, p.conf.Behavior.BatchTimeout)
	var resp ForwardResponse
	err := p.client.Forward(ctx, &req, &resp)
	cancel()

	// An error here indicates the entire request failed
	if err != nil {
		err = errors.Wrap(err, "Error in client.forward")
		p.conf.Log.WithFields(logrus.Fields{
			"batchTimeout": p.conf.Behavior.BatchTimeout.String(),
			"queueLen":     len(queue),
			"error":        err,
		}).Error("Error in client.forward")
		_ = p.setLastErr(err)

		for _, r := range queue {
			r.resp <- &response{err: err}
		}
		return
	}

	// Unlikely, but this avoids a panic if something wonky happens
	if len(resp.RateLimits) != len(queue) {
		for _, r := range queue {
			r.resp <- &response{err: errors.New("server responded with incorrect rate limit list size")}
		}
		return
	}

	// Provide responses to channels waiting in the queue
	for i, r := range queue {
		r.resp <- &response{rl: resp.RateLimits[i]}
	}
}

func (p *Peer) setLastErr(err error) error {
	// If we get a nil error return without caching it
	if err == nil {
		return err
	}

	// Add error to the cache with a TTL of 5 minutes
	p.lastErrs.AddWithTTL(err.Error(),
		errors.Wrap(err, fmt.Sprintf("from host %s", p.conf.Info.HTTPAddress)),
		clock.Minute*5)

	return err
}

// TODO: Replace this with modern error handling

// ErrNotReady is returned if the peer is not connected or is in a closing state
type ErrNotReady struct {
	err error
}

func (p *ErrNotReady) NotReady() bool {
	return true
}

func (p *ErrNotReady) Error() string {
	return p.err.Error()
}

func (p *ErrNotReady) Cause() error {
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
