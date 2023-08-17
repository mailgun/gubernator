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
	"fmt"
	"strings"
	"sync"

	"github.com/duh-rpc/duh-go"
	v1 "github.com/duh-rpc/duh-go/proto/v1"
	"github.com/pkg/errors"

	"github.com/mailgun/holster/v4/ctxutil"
	"github.com/mailgun/holster/v4/syncutil"
	"github.com/mailgun/holster/v4/tracing"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

const (
	maxBatchSize = 1000
	Healthy      = "healthy"
	UnHealthy    = "unhealthy"
)

type Service struct {
	propagator propagation.TraceContext
	global     *globalManager
	peerMutex  sync.RWMutex
	workerPool *WorkerPool
	log        FieldLogger
	conf       Config
	isClosed   bool
}

var (
	metricGetRateLimitCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gubernator_getratelimit_counter",
		Help: "The count of getLocalRateLimit() calls.  Label \"calltype\" may be \"local\" for calls handled by the same peer, or \"global\" for global rate limits.",
	}, []string{"calltype"})
	metricFuncTimeDuration = prometheus.NewSummaryVec(prometheus.SummaryOpts{
		Name: "gubernator_func_duration",
		Help: "The timings of key functions in Gubernator in seconds.",
		Objectives: map[float64]float64{
			1:    0.001,
			0.99: 0.001,
			0.5:  0.01,
		},
	}, []string{"name"})
	metricOverLimitCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "gubernator_over_limit_counter",
		Help: "The number of rate limit checks that are over the limit.",
	})
	metricConcurrentChecks = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "gubernator_concurrent_checks_counter",
		Help: "The number of concurrent GetRateLimits API calls.",
	})
	metricCheckErrorCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gubernator_check_error_counter",
		Help: "The number of errors while checking rate limits.",
	}, []string{"error"})
	metricCommandCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gubernator_command_counter",
		Help: "The count of commands processed by each worker in WorkerPool.",
	}, []string{"worker", "method"})
	metricWorkerQueue = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "gubernator_worker_queue_length",
		Help: "The count of requests queued up in WorkerPool.",
	}, []string{"method", "worker"})

	// Batch behavior.
	metricBatchSendRetries = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gubernator_batch_send_retries",
		Help: "The count of retries occurred in asyncRequest() forwarding a request to another peer.",
	}, []string{"name"})
	metricBatchQueueLength = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "gubernator_batch_queue_length",
		Help: "The getRateLimitsBatch() queue length in PeerClient.  This represents rate checks queued by for batching to a remote peer.",
	}, []string{"peerAddr"})
	metricBatchSendDuration = prometheus.NewSummaryVec(prometheus.SummaryOpts{
		Name: "gubernator_batch_send_duration",
		Help: "The timings of batch send operations to a remote peer.",
		Objectives: map[float64]float64{
			0.99: 0.001,
		},
	}, []string{"peerAddr"})
)

// NewService instantiate a single instance of a gubernator service
func NewService(conf Config) (s *Service, err error) {
	ctx := tracing.StartNamedScopeDebug(context.Background(), "gubernator.NewService")
	defer func() { tracing.EndScope(ctx, err) }()

	if err := conf.SetDefaults(); err != nil {
		return nil, err
	}

	s = &Service{
		log:  conf.Logger,
		conf: conf,
	}

	s.workerPool = NewWorkerPool(&conf)
	s.global = newGlobalManager(conf.Behaviors, s)

	if s.conf.Loader == nil {
		return s, nil
	}

	// Load the cache.
	err = s.workerPool.Load(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "Error in workerPool.Load")
	}

	return s, nil
}

func (s *Service) Close(ctx context.Context) (err error) {
	if s.isClosed {
		return nil
	}

	if s.conf.Loader == nil {
		return nil
	}

	s.global.Close()

	err = s.workerPool.Store(ctx)
	if err != nil {
		s.log.WithError(err).
			Error("Error in workerPool.Store")
		return errors.Wrap(err, "Error in workerPool.Store")
	}

	err = s.workerPool.Close()
	if err != nil {
		s.log.WithError(err).
			Error("Error in workerPool.Close")
		return errors.Wrap(err, "Error in workerPool.Close")
	}

	s.isClosed = true
	return nil
}

// CheckRateLimits is the public interface used by clients to request rate limits from the system. If the
// rate limit `Name` and `UniqueKey` is not owned by this instance, then we forward the request to the
// peer that does.
func (s *Service) CheckRateLimits(ctx context.Context, req *CheckRateLimitsRequest, resp *CheckRateLimitsResponse) (err error) {

	funcTimer := prometheus.NewTimer(metricFuncTimeDuration.WithLabelValues("Service.CheckRateLimits"))
	defer funcTimer.ObserveDuration()
	metricConcurrentChecks.Inc()
	defer metricConcurrentChecks.Dec()

	if len(req.Requests) > maxBatchSize {
		metricCheckErrorCounter.WithLabelValues("Request too large").Add(1)
		return duh.NewServiceError(duh.CodeBadRequest,
			fmt.Errorf("CheckRateLimitsRequest.RateLimits list too large; max size is '%d'", maxBatchSize), nil)
	}

	if len(req.Requests) == 0 {
		return duh.NewServiceError(duh.CodeBadRequest,
			errors.New("CheckRateLimitsRequest.RateLimits list is empty; provide at least one rate limit"), nil)
	}

	resp.Responses = make([]*RateLimitResponse, len(req.Requests))

	var wg sync.WaitGroup
	asyncCh := make(chan AsyncResp, len(req.Requests))

	// For each item in the request body
	for i, req := range req.Requests {
		key := req.Name + "_" + req.UniqueKey
		var peer *Peer
		var err error

		if len(req.UniqueKey) == 0 {
			metricCheckErrorCounter.WithLabelValues("Invalid request").Add(1)
			resp.Responses[i] = &RateLimitResponse{Error: "field 'unique_key' cannot be empty"}
			continue
		}

		if len(req.Name) == 0 {
			metricCheckErrorCounter.WithLabelValues("Invalid request").Add(1)
			resp.Responses[i] = &RateLimitResponse{Error: "field 'namespace' cannot be empty"}
			continue
		}

		if ctx.Err() != nil {
			err = errors.Wrap(ctx.Err(), "Error while iterating request items")
			span := trace.SpanFromContext(ctx)
			span.RecordError(err)
			resp.Responses[i] = &RateLimitResponse{
				Error: err.Error(),
			}
			continue
		}

		if s.conf.Behaviors.ForceGlobal {
			SetBehavior(&req.Behavior, Behavior_GLOBAL, true)
		}

		peer, err = s.GetPeer(ctx, key)
		if err != nil {
			countError(err, "Error in GetPeer")
			err = errors.Wrapf(err, "Error in GetPeer, looking up peer that owns rate limit '%s'", key)
			resp.Responses[i] = &RateLimitResponse{
				Error: err.Error(),
			}
			continue
		}

		// If our server instance is the owner of this rate limit
		if peer.Info().IsOwner {
			// Apply our rate limit algorithm to the request
			metricGetRateLimitCounter.WithLabelValues("local").Add(1)
			funcTimer1 := prometheus.NewTimer(metricFuncTimeDuration.WithLabelValues("V1Instance.getLocalRateLimit (local)"))
			resp.Responses[i], err = s.checkLocalRateLimit(ctx, req)
			funcTimer1.ObserveDuration()
			if err != nil {
				err = errors.Wrapf(err, "Error while apply rate limit for '%s'", key)
				span := trace.SpanFromContext(ctx)
				span.RecordError(err)
				resp.Responses[i] = &RateLimitResponse{Error: err.Error()}
			}
		} else {
			if HasBehavior(req.Behavior, Behavior_GLOBAL) {
				resp.Responses[i], err = s.getGlobalRateLimit(ctx, req)
				if err != nil {
					err = errors.Wrap(err, "Error in getGlobalRateLimit")
					span := trace.SpanFromContext(ctx)
					span.RecordError(err)
					resp.Responses[i] = &RateLimitResponse{Error: err.Error()}
				}

				// Inform the client of the owner key of the key
				resp.Responses[i].Metadata = map[string]string{"owner": peer.Info().HTTPAddress}
				continue
			}

			// Request must be forwarded to peer that owns the key.
			// Launch remote peer request in goroutine.
			wg.Add(1)
			go s.asyncRequest(ctx, &AsyncReq{
				AsyncCh: asyncCh,
				Peer:    peer,
				Req:     req,
				WG:      &wg,
				Key:     key,
				Idx:     i,
			})
		}
	}

	// Wait for any async responses if any
	wg.Wait()

	close(asyncCh)
	for a := range asyncCh {
		resp.Responses[a.Idx] = a.Resp
	}

	return nil
}

type AsyncResp struct {
	Idx  int
	Resp *RateLimitResponse
}

type AsyncReq struct {
	WG      *sync.WaitGroup
	AsyncCh chan AsyncResp
	Req     *RateLimitRequest
	Peer    *Peer
	Key     string
	Idx     int
}

func (s *Service) asyncRequest(ctx context.Context, req *AsyncReq) {
	ctx = tracing.StartNamedScope(ctx, "Service.asyncRequest")
	defer tracing.EndScope(ctx, nil)
	var attempts int
	var err error

	funcTimer := prometheus.NewTimer(metricFuncTimeDuration.WithLabelValues("Service.asyncRequest"))
	defer funcTimer.ObserveDuration()

	resp := AsyncResp{
		Idx: req.Idx,
	}

	for {
		if attempts > 5 {
			s.log.WithContext(ctx).
				WithError(err).
				WithField("key", req.Key).
				Error("GetPeer() returned peer that is not connected")
			countError(err, "Peer not connected")
			err = errors.Wrapf(err, "GetPeer() keeps returning peers that are not connected for '%s'", req.Key)
			resp.Resp = &RateLimitResponse{Error: err.Error()}
			break
		}

		// If we are attempting again, the owner of this rate limit might have changed to us!
		if attempts != 0 {
			if req.Peer.Info().IsOwner {
				metricGetRateLimitCounter.WithLabelValues("local").Add(1)
				resp.Resp, err = s.checkLocalRateLimit(ctx, req.Req)
				if err != nil {
					s.log.WithContext(ctx).
						WithError(err).
						WithField("key", req.Key).
						Error("Error applying rate limit")
					err = errors.Wrapf(err, "Error in checkLocalRateLimit for '%s'", req.Key)
					resp.Resp = &RateLimitResponse{Error: err.Error()}
				}
				break
			}
		}

		// Make an RPC call to the peer that owns this rate limit
		metricGetRateLimitCounter.WithLabelValues("forward").Add(1)
		r, err := req.Peer.Forward(ctx, req.Req)
		if err != nil {
			if IsNotReady(err) {
				attempts++
				metricBatchSendRetries.WithLabelValues(req.Req.Name).Inc()
				req.Peer, err = s.GetPeer(ctx, req.Key)
				if err != nil {
					errPart := fmt.Sprintf("Error finding peer that owns rate limit '%s'", req.Key)
					s.log.WithContext(ctx).WithError(err).WithField("key", req.Key).Error(errPart)
					countError(err, "Error in GetPeer")
					err = errors.Wrap(err, errPart)
					resp.Resp = &RateLimitResponse{Error: err.Error()}
					break
				}
				continue
			}

			// Not calling `countError()` because we expect the remote end to
			// report this error.
			err = errors.Wrap(err, fmt.Sprintf("Error while fetching rate limit '%s' from peer", req.Key))
			resp.Resp = &RateLimitResponse{Error: err.Error()}
			break
		}

		// Inform the client of the owner key of the key
		resp.Resp = r
		resp.Resp.Metadata = map[string]string{"owner": req.Peer.Info().HTTPAddress}
		break
	}

	req.AsyncCh <- resp
	req.WG.Done()

	if isDeadlineExceeded(ctx.Err()) {
		metricCheckErrorCounter.WithLabelValues("Timeout forwarding to peer").Inc()
	}
}

// getGlobalRateLimit handles rate limits that are marked as `Behavior = GLOBAL`. Rate limit responses
// are returned from the local cache and the hits are queued to be sent to the owning peer.
func (s *Service) getGlobalRateLimit(ctx context.Context, req *RateLimitRequest) (resp *RateLimitResponse, err error) {
	ctx = tracing.StartNamedScopeDebug(ctx, "getGlobalRateLimit")
	defer func() { tracing.EndScope(ctx, err) }()

	funcTimer := prometheus.NewTimer(metricFuncTimeDuration.WithLabelValues("Service.getGlobalRateLimit"))
	defer funcTimer.ObserveDuration()
	// Queue the hit for async update after we have prepared our response.
	// NOTE: The defer here avoids a race condition where we queue the req to
	// be forwarded to the owning peer in a separate goroutine but simultaneously
	// access and possibly copy the req in this method.
	defer s.global.QueueHit(req)

	item, ok, err := s.workerPool.GetCacheItem(ctx, req.HashKey())
	if err != nil {
		countError(err, "Error in workerPool.GetCacheItem")
		return nil, errors.Wrap(err, "during in workerPool.GetCacheItem")
	}
	if ok {
		// Global rate limits are always stored as RateLimitResponse regardless of algorithm
		rl, ok := item.Value.(*RateLimitResponse)
		if ok {
			return rl, nil
		}
		// We get here if the owning node hasn't asynchronously forwarded it's updates to us yet and
		// our cache still holds the rate limit we created on the first hit.
	}

	cpy := proto.Clone(req).(*RateLimitRequest)
	cpy.Behavior = Behavior_NO_BATCHING

	// Process the rate limit like we own it
	metricGetRateLimitCounter.WithLabelValues("global").Add(1)
	resp, err = s.checkLocalRateLimit(ctx, cpy)
	if err != nil {
		return nil, errors.Wrap(err, "Error in checkLocalRateLimit")
	}

	metricGetRateLimitCounter.WithLabelValues("global").Inc()
	return resp, nil
}

// Update updates the local cache with a list of rate limits.
// This method should only be called by a peer.
func (s *Service) Update(ctx context.Context, r *UpdateRequest, resp *v1.Reply) (err error) {
	ctx = tracing.StartNamedScopeDebug(ctx, "Service.Update")
	defer func() { tracing.EndScope(ctx, err) }()

	for _, g := range r.Globals {
		item := &CacheItem{
			ExpireAt:  g.Update.ResetTime,
			Algorithm: g.Algorithm,
			Value:     g.Update,
			Key:       g.Key,
		}
		err := s.workerPool.AddCacheItem(ctx, g.Key, item)
		if err != nil {
			return errors.Wrap(err, "Error in workerPool.AddCacheItem")
		}
	}
	resp.Code = duh.CodeOK
	return nil
}

// Forward is called by other peers when forwarding rate limits to this peer
func (s *Service) Forward(ctx context.Context, req *ForwardRequest, resp *ForwardResponse) (err error) {
	ctx = tracing.StartNamedScopeDebug(ctx, "Service.Forward")
	defer func() { tracing.EndScope(ctx, err) }()

	if len(req.Requests) > maxBatchSize {
		metricCheckErrorCounter.WithLabelValues("Request too large").Add(1)
		return duh.NewServiceError(duh.CodeBadRequest,
			fmt.Errorf("'PeerRequest.rate_limits' list too large; max size is '%d'", maxBatchSize), nil)
	}

	// Invoke each rate limit request.
	type reqIn struct {
		idx int
		req *RateLimitRequest
	}
	type respOut struct {
		idx int
		rl  *RateLimitResponse
	}

	resp.RateLimits = make([]*RateLimitResponse, len(req.Requests))
	respChan := make(chan respOut)
	var respWg sync.WaitGroup
	respWg.Add(1)

	go func() {
		// Capture each response and return in the same order
		for out := range respChan {
			resp.RateLimits[out.idx] = out.rl
		}

		respWg.Done()
	}()

	// Fan out requests.
	fan := syncutil.NewFanOut(s.conf.Workers)
	for idx, req := range req.Requests {
		fan.Run(func(in interface{}) error {
			rin := in.(reqIn)
			// Extract the propagated context from the metadata in the request
			ctx := s.propagator.Extract(ctx, &MetadataCarrier{Map: rin.req.Metadata})
			rl, err := s.checkLocalRateLimit(ctx, rin.req)
			if err != nil {
				// Return the error for this request
				err = errors.Wrap(err, "Error in checkLocalRateLimit")
				rl = &RateLimitResponse{Error: err.Error()}
				// metricCheckErrorCounter is updated within checkLocalRateLimit().
			}

			respChan <- respOut{rin.idx, rl}
			return nil
		}, reqIn{idx, req})
	}

	// Wait for all requests to be handled, then clean up.
	_ = fan.Wait()
	close(respChan)
	respWg.Wait()

	return nil
}

// HealthCheck Returns the health of our instance.
func (s *Service) HealthCheck(ctx context.Context, _ *HealthCheckRequest, resp *HealthCheckResponse) (err error) {
	var errs []string

	s.peerMutex.RLock()
	defer s.peerMutex.RUnlock()

	// Iterate through local peers and get their last errors
	localPeers := s.conf.LocalPicker.Peers()
	for _, peer := range localPeers {
		for _, errMsg := range peer.GetLastErr() {
			err := fmt.Errorf("error returned from local peer.GetLastErr: %s", errMsg)
			errs = append(errs, err.Error())
		}
	}

	// Do the same for region peers
	regionPeers := s.conf.RegionPicker.Peers()
	for _, peer := range regionPeers {
		for _, errMsg := range peer.GetLastErr() {
			err := fmt.Errorf("error returned from region peer.GetLastErr: %s", errMsg)
			errs = append(errs, err.Error())
		}
	}

	resp.PeerCount = int32(len(localPeers) + len(regionPeers))
	resp.Status = Healthy

	if len(errs) != 0 {
		resp.Status = UnHealthy
		resp.Message = strings.Join(errs, "|")
	}

	return nil
}

func (s *Service) checkLocalRateLimit(ctx context.Context, r *RateLimitRequest) (*RateLimitResponse, error) {
	ctx = tracing.StartNamedScope(ctx, "Service.checkLocalRateLimit")
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("request.key", r.UniqueKey),
		attribute.String("request.name", r.Name),
		attribute.Int64("request.limit", r.Limit),
		attribute.Int64("request.hits", r.Hits),
	)

	defer prometheus.NewTimer(metricFuncTimeDuration.WithLabelValues("Service.checkLocalRateLimit")).ObserveDuration()

	resp, err := s.workerPool.GetRateLimit(ctx, r)
	if err != nil {
		return nil, errors.Wrap(err, "during workerPool.GetRateLimit")
	}

	metricGetRateLimitCounter.WithLabelValues("local").Inc()

	// If global behavior and owning peer, broadcast update to all peers.
	// Assuming that this peer does not own the ratelimit.
	if HasBehavior(r.Behavior, Behavior_GLOBAL) {
		s.global.QueueUpdate(r)
	}

	return resp, nil
}

// SetPeers is called by the implementor to indicate the pool of peers has changed
func (s *Service) SetPeers(peerInfo []PeerInfo) {
	localPicker := s.conf.LocalPicker.New()
	regionPicker := s.conf.RegionPicker.New()

	for _, info := range peerInfo {
		// Add peers that are not in our local DC to the RegionPicker
		if info.DataCenter != s.conf.DataCenter {
			var err error
			peer := s.conf.RegionPicker.GetByPeerInfo(info)
			// If we don't have an existing Peer create a new one
			if peer == nil {
				peer, err = NewPeer(PeerConfig{
					PeerClient: s.conf.PeerClientFactory(info),
					Behavior:   s.conf.Behaviors,
					Log:        s.log,
					Info:       info,
				})
				if err != nil {
					s.log.WithError(err).Error("while adding new peer; skipping..")
				}
			}
			regionPicker.Add(peer)
			continue
		}
		var err error
		// If we don't have an existing Peer create a new one
		peer := s.conf.LocalPicker.GetByPeerInfo(info)
		if peer == nil {
			peer, err = NewPeer(PeerConfig{
				PeerClient: s.conf.PeerClientFactory(info),
				Behavior:   s.conf.Behaviors,
				Log:        s.log,
				Info:       info,
			})
			if err != nil {
				s.log.WithError(err).Error("while adding new peer; skipping..")
			}
		}
		localPicker.Add(peer)
	}

	s.peerMutex.Lock()

	// Replace our current pickers
	oldLocalPicker := s.conf.LocalPicker
	oldRegionPicker := s.conf.RegionPicker
	s.conf.LocalPicker = localPicker
	s.conf.RegionPicker = regionPicker
	s.peerMutex.Unlock()

	s.log.WithField("peers", peerInfo).Debug("peers updated")

	// Close any old peers we no longer need
	ctx, cancel := ctxutil.WithTimeout(context.Background(), s.conf.Behaviors.BatchTimeout)
	defer cancel()

	var shutdownPeers []*Peer
	for _, peer := range oldLocalPicker.Peers() {
		if peerInfo := s.conf.LocalPicker.GetByPeerInfo(peer.Info()); peerInfo == nil {
			shutdownPeers = append(shutdownPeers, peer)
		}
	}

	for _, regionPicker := range oldRegionPicker.Pickers() {
		for _, peer := range regionPicker.Peers() {
			if peerInfo := s.conf.RegionPicker.GetByPeerInfo(peer.Info()); peerInfo == nil {
				shutdownPeers = append(shutdownPeers, peer)
			}
		}
	}

	var wg syncutil.WaitGroup
	for _, p := range shutdownPeers {
		wg.Run(func(obj interface{}) error {
			pc := obj.(*Peer)
			err := pc.Close(ctx)
			if err != nil {
				s.log.WithError(err).WithField("peer", pc).Error("while shutting down peer")
			}
			return nil
		}, p)
	}
	wg.Wait()

	if len(shutdownPeers) > 0 {
		var peers []string
		for _, p := range shutdownPeers {
			peers = append(peers, p.Info().HTTPAddress)
		}
		s.log.WithField("peers", peers).Debug("peers shutdown")
	}
}

// GetPeer returns a peer client for the hash key provided
func (s *Service) GetPeer(ctx context.Context, key string) (p *Peer, err error) {
	s.peerMutex.RLock()
	defer s.peerMutex.RUnlock()

	p, err = s.conf.LocalPicker.Get(key)
	if err != nil {
		return nil, errors.Wrap(err, "Error in conf.LocalPicker.Get")
	}

	return p, nil
}

func (s *Service) GetPeerList() []*Peer {
	s.peerMutex.RLock()
	defer s.peerMutex.RUnlock()
	return s.conf.LocalPicker.Peers()
}

func (s *Service) GetRegionPickers() map[string]PeerPicker {
	s.peerMutex.RLock()
	defer s.peerMutex.RUnlock()
	return s.conf.RegionPicker.Pickers()
}

// Describe fetches prometheus metrics to be registered
func (s *Service) Describe(ch chan<- *prometheus.Desc) {
	metricBatchQueueLength.Describe(ch)
	metricBatchSendDuration.Describe(ch)
	metricBatchSendRetries.Describe(ch)
	metricCheckErrorCounter.Describe(ch)
	metricCommandCounter.Describe(ch)
	metricConcurrentChecks.Describe(ch)
	metricFuncTimeDuration.Describe(ch)
	metricGetRateLimitCounter.Describe(ch)
	metricOverLimitCounter.Describe(ch)
	metricWorkerQueue.Describe(ch)
	s.global.metricBroadcastCounter.Describe(ch)
	s.global.metricBroadcastDuration.Describe(ch)
	s.global.metricGlobalQueueLength.Describe(ch)
	s.global.metricGlobalSendDuration.Describe(ch)
}

// Collect fetches metrics from the server for use by prometheus
func (s *Service) Collect(ch chan<- prometheus.Metric) {
	metricBatchQueueLength.Collect(ch)
	metricBatchSendDuration.Collect(ch)
	metricBatchSendRetries.Collect(ch)
	metricCheckErrorCounter.Collect(ch)
	metricCommandCounter.Collect(ch)
	metricConcurrentChecks.Collect(ch)
	metricFuncTimeDuration.Collect(ch)
	metricGetRateLimitCounter.Collect(ch)
	metricOverLimitCounter.Collect(ch)
	metricWorkerQueue.Collect(ch)
	s.global.metricBroadcastCounter.Collect(ch)
	s.global.metricBroadcastDuration.Collect(ch)
	s.global.metricGlobalQueueLength.Collect(ch)
	s.global.metricGlobalSendDuration.Collect(ch)
}

// HasBehavior returns true if the provided behavior is set
func HasBehavior(b Behavior, flag Behavior) bool {
	return b&flag != 0
}

// SetBehavior sets or clears the behavior depending on the boolean `set`
func SetBehavior(b *Behavior, flag Behavior, set bool) {
	if set {
		*b = *b | flag
	} else {
		mask := *b ^ flag
		*b &= mask
	}
}

// Count an error type in the metricCheckErrorCounter metric.
// Recurse into wrapped errors if necessary.
func countError(err error, defaultType string) {
	for {
		if err == nil {
			metricCheckErrorCounter.WithLabelValues(defaultType).Inc()
			return
		}
		if errors.Is(err, context.DeadlineExceeded) {
			metricCheckErrorCounter.WithLabelValues("Timeout").Inc()
			return
		}

		err = errors.Unwrap(err)
	}
}

func isDeadlineExceeded(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.DeadlineExceeded)
}
