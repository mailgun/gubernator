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
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mailgun/gubernator/v2/tracing"
	"github.com/mailgun/holster/v4/setter"
	"github.com/mailgun/holster/v4/syncutil"
	"github.com/opentracing/opentracing-go/ext"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	maxBatchSize = 1000
	Healthy      = "healthy"
	UnHealthy    = "unhealthy"
)

type V1Instance struct {
	UnimplementedV1Server
	UnimplementedPeersV1Server
	global      *globalManager
	mutliRegion *mutliRegionManager
	peerMutex   sync.RWMutex
	log         logrus.FieldLogger
	conf        Config
	isClosed    bool
}

var getRateLimitCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "gubernator_getratelimit_counter",
	Help: "Count of getRateLimit() calls.  Label \"calltype\" may be \"local\" for calls handled by the same peer, \"forward\" for calls forwarded to another peer, or \"global\" for global rate limits.",
}, []string{"calltype"})
var getPeerRateLimitDurationMetric = prometheus.NewSummaryVec(prometheus.SummaryOpts{
	Name: "baliedge_gubernator_duration",
	Help: "Processing time for calls to getRateLimit within Gubernator.",
	Objectives: map[float64]float64{
		0.5:  0.05,
		0.99: 0.001,
	},
}, []string{"name"})
var getPeerRateLimitLockDurationMetric = prometheus.NewSummaryVec(prometheus.SummaryOpts{
	Name: "baliedge_gubernator_lock_duration",
	Help: "Lock wait time for calls to getRateLimit within Gubernator.",
	Objectives: map[float64]float64{
		0.5:  0.05,
		0.99: 0.001,
	},
}, []string{"name"})
var funcTimeMetric = prometheus.NewSummaryVec(prometheus.SummaryOpts{
	Name: "baliedge_func_duration",
	Objectives: map[float64]float64{
		0.99: 0.001,
	},
}, []string{"name"})
var asyncRequestsRetriesCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "baliedge_asyncrequests_retries",
}, []string{"name"})
var queueLengthMetric = prometheus.NewSummaryVec(prometheus.SummaryOpts{
	Name: "baliedge_queue_length",
	Objectives: map[float64]float64{
		0.99: 0.001,
	},
}, []string{"peerAddr"})
var lockCounterMetric = prometheus.NewSummary(prometheus.SummaryOpts{
	Name: "baliedge_lock_counter",
	Objectives: map[float64]float64{
		0.99: 0.001,
	},
})

// NewV1Instance instantiate a single instance of a gubernator peer and registers this
// instance with the provided GRPCServer.
func NewV1Instance(conf Config) (*V1Instance, error) {
	if conf.GRPCServers == nil {
		return nil, errors.New("at least one GRPCServer instance is required")
	}
	if err := conf.SetDefaults(); err != nil {
		return nil, err
	}

	s := V1Instance{
		log:  conf.Logger,
		conf: conf,
	}
	setter.SetDefault(&s.log, logrus.WithField("category", "gubernator"))

	s.global = newGlobalManager(conf.Behaviors, &s)
	s.mutliRegion = newMultiRegionManager(conf.Behaviors, &s)

	// Register our instance with all GRPC servers
	for _, srv := range conf.GRPCServers {
		RegisterV1Server(srv, &s)
		RegisterPeersV1Server(srv, &s)
	}

	if s.conf.Loader == nil {
		return &s, nil
	}

	// Load the cache.
	ch, err := s.conf.Loader.Load()
	if err != nil {
		return nil, errors.Wrap(err, "while loading persistent from store")
	}

	for item := range ch {
		s.conf.Cache.Add(item)
	}
	return &s, nil
}

func (s *V1Instance) Close() error {
	if s.isClosed {
		return nil
	}

	if s.conf.Loader == nil {
		return nil
	}

	s.global.Close()
	s.mutliRegion.Close()

	// Write cache to store.
	out := make(chan *CacheItem, 500)
	go func() {
		for item := range s.conf.Cache.Each() {
			out <- item
		}
		close(out)
	}()
	s.isClosed = true
	return s.conf.Loader.Save(out)
}

// GetRateLimits is the public interface used by clients to request rate limits from the system. If the
// rate limit `Name` and `UniqueKey` is not owned by this instance then we forward the request to the
// peer that does.
func (s *V1Instance) GetRateLimits(ctx context.Context, r *GetRateLimitsReq) (*GetRateLimitsResp, error) {
	span, ctx := tracing.StartSpan(ctx)
	defer span.Finish()

	if len(r.Requests) > maxBatchSize {
		return nil, status.Errorf(codes.OutOfRange,
			"Requests.RateLimits list too large; max size is '%d'", maxBatchSize)
	}

	resp := GetRateLimitsResp{
		Responses: make([]*RateLimitResp, len(r.Requests)),
	}

	var wg sync.WaitGroup
	asyncCh := make(chan AsyncResp, len(r.Requests))

	// For each item in the request body
	for i, req := range r.Requests {
		span2, ctx2 := tracing.StartNamedSpan(ctx, "Loop requests")

		func() {
			defer span2.Finish()
			key := req.Name + "_" + req.UniqueKey
			var peer *PeerClient
			var err error

			if len(req.UniqueKey) == 0 {
				resp.Responses[i] = &RateLimitResp{Error: "field 'unique_key' cannot be empty"}
				return
			}

			if len(req.Name) == 0 {
				resp.Responses[i] = &RateLimitResp{Error: "field 'namespace' cannot be empty"}
				return
			}

			peer, err = s.GetPeer(ctx2, key)
			if err != nil {
				resp.Responses[i] = &RateLimitResp{
					Error: fmt.Sprintf("while finding peer that owns rate limit '%s' - '%s'", key, err),
				}
				return
			}

			// If our server instance is the owner of this rate limit
			if peer.Info().IsOwner {
				// Apply our rate limit algorithm to the request
				getRateLimitCounter.WithLabelValues("local").Add(1)
				funcTimer1 := prometheus.NewTimer(funcTimeMetric.WithLabelValues("V1Instance.getRateLimit (local)"))
				resp.Responses[i], err = s.getRateLimit(ctx2, req)
				funcTimer1.ObserveDuration()
				if err != nil {
					err2 := errors.Wrapf(err, "Error while apply rate limit for '%s'", key)
					ext.LogError(span2, err2)
					resp.Responses[i] = &RateLimitResp{
						Error: err2.Error(),
					}
				}
			} else {
				if HasBehavior(req.Behavior, Behavior_GLOBAL) {
					resp.Responses[i], err = s.getGlobalRateLimit(ctx2, req)
					if err != nil {
						resp.Responses[i] = &RateLimitResp{Error: err.Error()}
					}

					// Inform the client of the owner key of the key
					resp.Responses[i].Metadata = map[string]string{"owner": peer.Info().GRPCAddress}
					return
				}
				wg.Add(1)
				go s.asyncRequests(ctx2, &AsyncReq{
					AsyncCh: asyncCh,
					Peer:    peer,
					Req:     req,
					WG:      &wg,
					Key:     key,
					Idx:     i,
				})
			}
		}()
	}

	// Wait for any async responses if any
	span3, _ := tracing.StartNamedSpan(ctx, "Wait for responses")
	wg.Wait()
	span3.Finish()

	close(asyncCh)
	for a := range asyncCh {
		resp.Responses[a.Idx] = a.Resp
	}

	return &resp, nil
}

type AsyncResp struct {
	Idx  int
	Resp *RateLimitResp
}

type AsyncReq struct {
	WG      *sync.WaitGroup
	AsyncCh chan AsyncResp
	Req     *RateLimitReq
	Peer    *PeerClient
	Key     string
	Idx     int
}

func (s *V1Instance) asyncRequests(ctx context.Context, req *AsyncReq) {
	var attempts int
	var err error

	span, ctx := tracing.StartSpan(ctx)
	defer span.Finish()
	span.SetTag("request.name", req.Req.Name)
	span.SetTag("request.key", req.Req.UniqueKey)
	span.SetTag("request.limit", req.Req.Limit)
	span.SetTag("request.duration", req.Req.Duration)
	span.SetTag("peer.grpcAddress", req.Peer.Info().GRPCAddress)

	funcTimer := prometheus.NewTimer(funcTimeMetric.WithLabelValues("V1Instance.asyncRequests"))
	defer funcTimer.ObserveDuration()

	resp := AsyncResp{
		Idx: req.Idx,
	}

	for {
		if attempts > 5 {
			logrus.
				WithError(errors.WithStack(err)).
				WithField("key", req.Key).
				Error("GetPeer() keeps returning peers that are not connected")
			resp.Resp = &RateLimitResp{
				Error: fmt.Sprintf("GetPeer() keeps returning peers that are not connected for '%s' - '%s'", req.Key, err),
			}
			break
		}

		// If we are attempting again, the owner of the this rate limit might have changed to us!
		if attempts != 0 {
			if req.Peer.Info().IsOwner {
				getRateLimitCounter.WithLabelValues("local").Add(1)
				resp.Resp, err = s.getRateLimit(ctx, req.Req)
				if err != nil {
					logrus.
						WithError(errors.WithStack(err)).
						WithField("key", req.Key).
						Error("Error applying rate limit")
					resp.Resp = &RateLimitResp{
						Error: fmt.Sprintf("while applying rate limit for '%s' - '%s'", req.Key, err),
					}
				}
				break
			}
		}

		// Make an RPC call to the peer that owns this rate limit
		getRateLimitCounter.WithLabelValues("forward").Add(1)
		r, err := req.Peer.GetPeerRateLimit(ctx, req.Req)
		if err != nil {
			if IsNotReady(err) {
				attempts++
				asyncRequestsRetriesCounter.WithLabelValues(req.Req.Name).Add(1)
				req.Peer, err = s.GetPeer(ctx, req.Key)
				if err != nil {
					errPart := fmt.Sprintf("Error finding peer that owns rate limit '%s'", req.Key)
					err2 := errors.Wrap(err, errPart)
					logrus.
						WithError(errors.WithStack(err)).
						WithField("key", req.Key).
						Error(errPart)
					ext.LogError(span, err2)
					resp.Resp = &RateLimitResp{
						Error: err2.Error(),
					}
					break
				}
				continue
			}

			errPart := fmt.Sprintf("Error while fetching rate limit '%s' from peer", req.Key)
			err2 := errors.Wrap(err, errPart)
			logrus.
				WithError(errors.WithStack(err)).
				WithField("key", req.Key).
				Error("Error fetching rate limit from peer")
			ext.LogError(span, err2)
			resp.Resp = &RateLimitResp{
				Error: err2.Error(),
			}
			break
		}
		// Inform the client of the owner key of the key
		resp.Resp = r
		resp.Resp.Metadata = map[string]string{"owner": req.Peer.Info().GRPCAddress}
		break
	}

	req.AsyncCh <- resp
	req.WG.Done()
}

// getGlobalRateLimit handles rate limits that are marked as `Behavior = GLOBAL`. Rate limit responses
// are returned from the local cache and the hits are queued to be sent to the owning peer.
func (s *V1Instance) getGlobalRateLimit(ctx context.Context, req *RateLimitReq) (*RateLimitResp, error) {
	span, ctx := tracing.StartSpan(ctx)
	defer span.Finish()

	funcTimer := prometheus.NewTimer(funcTimeMetric.WithLabelValues("V1Instance.getGlobalRateLimit"))
	defer funcTimer.ObserveDuration()
	// Queue the hit for async update after we have prepared our response.
	// NOTE: The defer here avoids a race condition where we queue the req to
	// be forwarded to the owning peer in a separate goroutine but simultaneously
	// access and possibly copy the req in this method.
	defer s.global.QueueHit(req)

	s.conf.Cache.Lock()
	tracing.LogInfo(span, "conf.Cache.Lock()")
	item, ok := s.conf.Cache.GetItem(req.HashKey())
	s.conf.Cache.Unlock()
	if ok {
		// Global rate limits are always stored as RateLimitResp regardless of algorithm
		rl, ok := item.Value.(*RateLimitResp)
		if ok {
			return rl, nil
		}
		// We get here if the owning node hasn't asynchronously forwarded it's updates to us yet and
		// our cache still holds the rate limit we created on the first hit.
	}
	cpy := proto.Clone(req).(*RateLimitReq)
	cpy.Behavior = Behavior_NO_BATCHING
	// Process the rate limit like we own it
	getRateLimitCounter.WithLabelValues("global").Add(1)
	resp, err := s.getRateLimit(ctx, cpy)

	if err != nil {
		err2 := errors.Wrap(err, "Error in getRateLimit")
		ext.LogError(span, err2)
		return nil, err2
	}

	return resp, nil
}

// UpdatePeerGlobals updates the local cache with a list of global rate limits. This method should only
// be called by a peer who is the owner of a global rate limit.
func (s *V1Instance) UpdatePeerGlobals(ctx context.Context, r *UpdatePeerGlobalsReq) (*UpdatePeerGlobalsResp, error) {
	span, ctx := tracing.StartSpan(ctx)
	defer span.Finish()

	s.conf.Cache.Lock()
	defer s.conf.Cache.Unlock()
	tracing.LogInfo(span, "conf.Cache.Lock()")

	for _, g := range r.Globals {
		s.conf.Cache.Add(&CacheItem{
			ExpireAt:  g.Status.ResetTime,
			Algorithm: g.Algorithm,
			Value:     g.Status,
			Key:       g.Key,
		})
	}
	return &UpdatePeerGlobalsResp{}, nil
}

// GetPeerRateLimits is called by other peers to get the rate limits owned by this peer.
func (s *V1Instance) GetPeerRateLimits(ctx context.Context, r *GetPeerRateLimitsReq) (*GetPeerRateLimitsResp, error) {
	span, ctx := tracing.StartSpan(ctx)
	defer span.Finish()

	span.SetTag("numRequests", len(r.Requests))

	var resp GetPeerRateLimitsResp

	if len(r.Requests) > maxBatchSize {
		err2 := fmt.Errorf("'PeerRequest.rate_limits' list too large; max size is '%d'", maxBatchSize)
		ext.LogError(span, err2)
		return nil, status.Error(codes.OutOfRange, err2.Error())
	}

	for _, req := range r.Requests {
		rl, err := s.getRateLimit(ctx, req)
		if err != nil {
			// Return the error for this request
			err2 := errors.Wrap(err, "Error in getRateLimit")
			ext.LogError(span, err2)
			rl = &RateLimitResp{Error: err2.Error()}
		}
		resp.RateLimits = append(resp.RateLimits, rl)
	}
	return &resp, nil
}

// HealthCheck Returns the health of our instance.
func (s *V1Instance) HealthCheck(ctx context.Context, r *HealthCheckReq) (*HealthCheckResp, error) {
	span, ctx := tracing.StartSpan(ctx)
	defer span.Finish()

	var errs []string

	s.peerMutex.RLock()
	tracing.LogInfo(span, "peerMutex.RLock()")

	// Iterate through local peers and get their last errors
	localPeers := s.conf.LocalPicker.Peers()
	for _, peer := range localPeers {
		lastErr := peer.GetLastErr()

		if lastErr != nil {
			for _, err := range lastErr {
				err2 := fmt.Errorf("Error returned from local peer.GetLastErr: %s", err)
				ext.LogError(span, err2)
				errs = append(errs, err2.Error())
			}
		}
	}

	// Do the same for region peers
	regionPeers := s.conf.RegionPicker.Peers()
	for _, peer := range regionPeers {
		lastErr := peer.GetLastErr()

		if lastErr != nil {
			for _, err := range lastErr {
				err2 := fmt.Errorf("Error returned from region peer.GetLastErr: %s", err)
				ext.LogError(span, err2)
				errs = append(errs, err2.Error())
			}
		}
	}

	health := HealthCheckResp{
		PeerCount: int32(len(localPeers) + len(regionPeers)),
		Status:    Healthy,
	}

	if len(errs) != 0 {
		health.Status = UnHealthy
		health.Message = strings.Join(errs, "|")
	}

	span.SetTag("health.peerCount", health.PeerCount)
	span.SetTag("health.status", health.Status)

	defer s.peerMutex.RUnlock()
	return &health, nil
}

func (s *V1Instance) getRateLimit(ctx context.Context, r *RateLimitReq) (*RateLimitResp, error) {
	span, ctx := tracing.StartSpan(ctx)
	defer span.Finish()
	span.SetTag("request.name", r.Name)
	span.SetTag("request.key", r.UniqueKey)
	span.SetTag("request.limit", r.Limit)
	span.SetTag("request.duration", r.Duration)

	requestTimer := prometheus.NewTimer(getPeerRateLimitDurationMetric.WithLabelValues(r.Name))
	defer requestTimer.ObserveDuration()
	lockTimer := prometheus.NewTimer(getPeerRateLimitLockDurationMetric.WithLabelValues(r.Name))

	s.conf.Cache.Lock()
	defer s.conf.Cache.Unlock()
	lockTimer.ObserveDuration()
	lruCache := s.conf.Cache.(*LRUCache)
	lockCounter := atomic.LoadUint64(&lruCache.LockCounter) - atomic.LoadUint64(&lruCache.UnlockCounter)
	lockCounterMetric.Observe(float64(lockCounter))
	tracing.LogInfo(span, "conf.Cache.Lock()", "lockCounter", lockCounter)

	if HasBehavior(r.Behavior, Behavior_GLOBAL) {
		s.global.QueueUpdate(r)
	}

	if HasBehavior(r.Behavior, Behavior_MULTI_REGION) {
		s.mutliRegion.QueueHits(r)
	}

	switch r.Algorithm {
	case Algorithm_TOKEN_BUCKET:
		tokenBucketTimer := prometheus.NewTimer(funcTimeMetric.WithLabelValues("V1Instance.getRateLimit_tokenBucket"))
		defer tokenBucketTimer.ObserveDuration()
		resp, err := tokenBucket(ctx, s.conf.Store, s.conf.Cache, r)
		if err != nil {
			err2 := errors.Wrap(err, "Error in tokenBucket")
			ext.LogError(span, err2)
			return nil, err2
		}
		return resp, nil

	case Algorithm_LEAKY_BUCKET:
		leakyBucketTimer := prometheus.NewTimer(funcTimeMetric.WithLabelValues("V1Instance.getRateLimit_leakyBucket"))
		defer leakyBucketTimer.ObserveDuration()
		resp, err := leakyBucket(ctx, s.conf.Store, s.conf.Cache, r)
		if err != nil {
			err2 := errors.Wrap(err, "Error in leakyBucket")
			ext.LogError(span, err2)
			return nil, err2
		}
		return resp, nil
	}

	err := fmt.Errorf("Invalid rate limit algorithm '%d'", r.Algorithm)
	ext.LogError(span, err)
	return nil, err
}

// SetPeers is called by the implementor to indicate the pool of peers has changed
func (s *V1Instance) SetPeers(peerInfo []PeerInfo) {
	localPicker := s.conf.LocalPicker.New()
	regionPicker := s.conf.RegionPicker.New()

	for _, info := range peerInfo {
		// Add peers that are not in our local DC to the RegionPicker
		if info.DataCenter != s.conf.DataCenter {
			peer := s.conf.RegionPicker.GetByPeerInfo(info)
			// If we don't have an existing PeerClient create a new one
			if peer == nil {
				peer = NewPeerClient(PeerConfig{
					TLS:      s.conf.PeerTLS,
					Behavior: s.conf.Behaviors,
					Info:     info,
				})
			}
			regionPicker.Add(peer)
			continue
		}
		// If we don't have an existing PeerClient create a new one
		peer := s.conf.LocalPicker.GetByPeerInfo(info)
		if peer == nil {
			peer = NewPeerClient(PeerConfig{
				TLS:      s.conf.PeerTLS,
				Behavior: s.conf.Behaviors,
				Info:     info,
			})
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

	// Shutdown any old peers we no longer need
	ctx, cancel := tracing.ContextWithTimeout(context.Background(), s.conf.Behaviors.BatchTimeout)
	defer cancel()

	var shutdownPeers []*PeerClient
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
			pc := obj.(*PeerClient)
			err := pc.Shutdown(ctx)
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
			peers = append(peers, p.Info().GRPCAddress)
		}
		s.log.WithField("peers", peers).Debug("peers shutdown")
	}
}

// GetPeer returns a peer client for the hash key provided
func (s *V1Instance) GetPeer(ctx context.Context, key string) (*PeerClient, error) {
	span, _ := tracing.StartSpan(ctx)
	defer span.Finish()

	funcTimer := prometheus.NewTimer(funcTimeMetric.WithLabelValues("V1Instance.GetPeer"))
	defer funcTimer.ObserveDuration()
	lockTimer := prometheus.NewTimer(funcTimeMetric.WithLabelValues("V1Instance.GetPeer_RLock"))

	s.peerMutex.RLock()
	lockTimer.ObserveDuration()
	tracing.LogInfo(span, "peerMutex.RLock()")

	peer, err := s.conf.LocalPicker.Get(key)
	if err != nil {
		s.peerMutex.RUnlock()
		err2 := errors.Wrap(err, "Error in conf.LocalPicker.Get")
		ext.LogError(span, err2)
		return nil, err2
	}
	s.peerMutex.RUnlock()
	return peer, nil
}

func (s *V1Instance) GetPeerList() []*PeerClient {
	s.peerMutex.RLock()
	defer s.peerMutex.RUnlock()
	return s.conf.LocalPicker.Peers()
}

func (s *V1Instance) GetRegionPickers() map[string]PeerPicker {
	s.peerMutex.RLock()
	defer s.peerMutex.RUnlock()
	return s.conf.RegionPicker.Pickers()
}

// Describe fetches prometheus metrics to be registered
func (s *V1Instance) Describe(ch chan<- *prometheus.Desc) {
	ch <- s.global.asyncMetrics.Desc()
	ch <- s.global.broadcastMetrics.Desc()
	getRateLimitCounter.Describe(ch)
	getPeerRateLimitDurationMetric.Describe(ch)
	getPeerRateLimitLockDurationMetric.Describe(ch)
	funcTimeMetric.Describe(ch)
	asyncRequestsRetriesCounter.Describe(ch)
	queueLengthMetric.Describe(ch)
	lockCounterMetric.Describe(ch)
}

// Collect fetches metrics from the server for use by prometheus
func (s *V1Instance) Collect(ch chan<- prometheus.Metric) {
	ch <- s.global.asyncMetrics
	ch <- s.global.broadcastMetrics
	getRateLimitCounter.Collect(ch)
	getPeerRateLimitDurationMetric.Collect(ch)
	getPeerRateLimitLockDurationMetric.Collect(ch)
	funcTimeMetric.Collect(ch)
	asyncRequestsRetriesCounter.Collect(ch)
	queueLengthMetric.Collect(ch)
	lockCounterMetric.Collect(ch)
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
