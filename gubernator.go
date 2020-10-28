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

	"github.com/mailgun/holster/v3/setter"
	"github.com/mailgun/holster/v3/syncutil"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	maxBatchSize = 1000
	Healthy      = "healthy"
	UnHealthy    = "unhealthy"
)

type V1Instance struct {
	health      HealthCheckResp
	global      *globalManager
	mutliRegion *mutliRegionManager
	peerMutex   sync.RWMutex
	log         logrus.FieldLogger
	conf        Config
	isClosed    bool
}

// NewV1Instance instantiate a single instance of a gubernator peer and registers this
// instance with the provided GRPCServer.
func NewV1Instance(conf Config) (*V1Instance, error) {
	if conf.GRPCServer == nil {
		return nil, errors.New("GRPCServer instance is required")
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

	// Register our server with GRPC
	RegisterV1Server(conf.GRPCServer, &s)
	RegisterPeersV1Server(conf.GRPCServer, &s)

	if s.conf.Loader == nil {
		return &s, nil
	}

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
	var resp GetRateLimitsResp
	if len(r.Requests) > maxBatchSize {
		return nil, status.Errorf(codes.OutOfRange,
			"Requests.RateLimits list too large; max size is '%d'", maxBatchSize)
	}

	type InOut struct {
		In  *RateLimitReq
		Idx int
		Out *RateLimitResp
	}

	// Asynchronously fetch rate limits
	out := make(chan InOut)
	go func() {
		fan := syncutil.NewFanOut(1000)
		// For each item in the request body
		for i, item := range r.Requests {
			fan.Run(func(data interface{}) error {
				inOut := data.(InOut)

				globalKey := inOut.In.Name + "_" + inOut.In.UniqueKey
				var peer *PeerClient
				var err error

				if len(inOut.In.UniqueKey) == 0 {
					inOut.Out = &RateLimitResp{Error: "field 'unique_key' cannot be empty"}
					out <- inOut
					return nil
				}

				if len(inOut.In.Name) == 0 {
					inOut.Out = &RateLimitResp{Error: "field 'namespace' cannot be empty"}
					out <- inOut
					return nil
				}

				var attempts int
			getPeer:
				if attempts > 5 {
					inOut.Out = &RateLimitResp{
						Error: fmt.Sprintf("GetPeer() keeps returning peers that are not connected for '%s' - '%s'", globalKey, err),
					}
					out <- inOut
					return nil
				}

				peer, err = s.GetPeer(globalKey)
				if err != nil {
					inOut.Out = &RateLimitResp{
						Error: fmt.Sprintf("while finding peer that owns rate limit '%s' - '%s'", globalKey, err),
					}
					out <- inOut
					return nil
				}

				// If our server instance is the owner of this rate limit
				if peer.Info().IsOwner {
					// Apply our rate limit algorithm to the request
					inOut.Out, err = s.getRateLimit(inOut.In)
					if err != nil {
						inOut.Out = &RateLimitResp{
							Error: fmt.Sprintf("while applying rate limit for '%s' - '%s'", globalKey, err),
						}
					}
				} else {
					if HasBehavior(inOut.In.Behavior, Behavior_GLOBAL) {
						inOut.Out, err = s.getGlobalRateLimit(inOut.In)
						if err != nil {
							inOut.Out = &RateLimitResp{Error: err.Error()}
						}

						// Inform the client of the owner key of the key
						inOut.Out.Metadata = map[string]string{"owner": peer.Info().GRPCAddress}

						out <- inOut
						return nil
					}

					// Make an RPC call to the peer that owns this rate limit
					inOut.Out, err = peer.GetPeerRateLimit(ctx, inOut.In)
					if err != nil {
						if IsNotReady(err) {
							attempts++
							goto getPeer
						}
						inOut.Out = &RateLimitResp{
							Error: fmt.Sprintf("while fetching rate limit '%s' from peer - '%s'", globalKey, err),
						}
					}

					// Inform the client of the owner key of the key
					inOut.Out.Metadata = map[string]string{"owner": peer.Info().GRPCAddress}
				}

				out <- inOut
				return nil
			}, InOut{In: item, Idx: i})
		}
		fan.Wait()
		close(out)
	}()

	resp.Responses = make([]*RateLimitResp, len(r.Requests))
	// Collect the async responses as they return
	for i := range out {
		resp.Responses[i.Idx] = i.Out
	}

	return &resp, nil
}

// getGlobalRateLimit handles rate limits that are marked as `Behavior = GLOBAL`. Rate limit responses
// are returned from the local cache and the hits are queued to be sent to the owning peer.
func (s *V1Instance) getGlobalRateLimit(req *RateLimitReq) (*RateLimitResp, error) {
	// Queue the hit for async update
	s.global.QueueHit(req)

	s.conf.Cache.Lock()
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
	cpy := *req
	cpy.Behavior = Behavior_NO_BATCHING
	// Process the rate limit like we own it
	resp, err := s.getRateLimit(&cpy)
	return resp, err
}

// UpdatePeerGlobals updates the local cache with a list of global rate limits. This method should only
// be called by a peer who is the owner of a global rate limit.
func (s *V1Instance) UpdatePeerGlobals(ctx context.Context, r *UpdatePeerGlobalsReq) (*UpdatePeerGlobalsResp, error) {
	s.conf.Cache.Lock()
	defer s.conf.Cache.Unlock()

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
	var resp GetPeerRateLimitsResp

	if len(r.Requests) > maxBatchSize {
		return nil, status.Errorf(codes.OutOfRange,
			"'PeerRequest.rate_limits' list too large; max size is '%d'", maxBatchSize)
	}

	for _, req := range r.Requests {
		rl, err := s.getRateLimit(req)
		if err != nil {
			// Return the error for this request
			rl = &RateLimitResp{Error: err.Error()}
		}
		resp.RateLimits = append(resp.RateLimits, rl)
	}
	return &resp, nil
}

// HealthCheck Returns the health of our instance.
func (s *V1Instance) HealthCheck(ctx context.Context, r *HealthCheckReq) (*HealthCheckResp, error) {
	var errs []string

	s.peerMutex.RLock()

	// Iterate through local peers and get their last errors
	localPeers := s.conf.LocalPicker.Peers()
	for _, peer := range localPeers {
		lastErr := peer.GetLastErr()

		if lastErr != nil {
			for _, err := range lastErr {
				errs = append(errs, err)
			}
		}
	}

	// Do the same for region peers
	regionPeers := s.conf.RegionPicker.Peers()
	for _, peer := range regionPeers {
		lastErr := peer.GetLastErr()

		if lastErr != nil {
			for _, err := range lastErr {
				errs = append(errs, err)
			}
		}
	}

	s.health.Status = Healthy
	if len(errs) != 0 {
		s.health.Status = UnHealthy
		s.health.Message = strings.Join(errs, "|")
		s.health.PeerCount = int32(s.conf.LocalPicker.Size())
	}

	defer s.peerMutex.RUnlock()
	return &s.health, nil
}

func (s *V1Instance) getRateLimit(r *RateLimitReq) (*RateLimitResp, error) {
	s.conf.Cache.Lock()
	defer s.conf.Cache.Unlock()

	if HasBehavior(r.Behavior, Behavior_GLOBAL) {
		s.global.QueueUpdate(r)
	}

	if HasBehavior(r.Behavior, Behavior_MULTI_REGION) {
		s.mutliRegion.QueueHits(r)
	}

	switch r.Algorithm {
	case Algorithm_TOKEN_BUCKET:
		return tokenBucket(s.conf.Store, s.conf.Cache, r)
	case Algorithm_LEAKY_BUCKET:
		return leakyBucket(s.conf.Store, s.conf.Cache, r)
	}
	return nil, errors.Errorf("invalid rate limit algorithm '%d'", r.Algorithm)
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
	ctx, cancel := context.WithTimeout(context.Background(), s.conf.Behaviors.BatchTimeout)
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
		s.log.WithField("peers", peers).Debug("Peers shutdown")
	}
}

// GetPeers returns a peer client for the hash key provided
func (s *V1Instance) GetPeer(key string) (*PeerClient, error) {
	s.peerMutex.RLock()
	peer, err := s.conf.LocalPicker.Get(key)
	if err != nil {
		s.peerMutex.RUnlock()
		return nil, err
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
}

// Collect fetches metrics from the server for use by prometheus
func (s *V1Instance) Collect(ch chan<- prometheus.Metric) {
	ch <- s.global.asyncMetrics
	ch <- s.global.broadcastMetrics
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
