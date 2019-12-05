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

	"github.com/prometheus/client_golang/prometheus"

	"github.com/mailgun/holster"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	maxBatchSize = 1000
	Healthy      = "healthy"
	UnHealthy    = "unhealthy"
)

var log *logrus.Entry

type Instance struct {
	health    HealthCheckResp
	global    *globalManager
	peerMutex sync.RWMutex
	conf      Config
	isClosed  bool
}

func New(conf Config) (*Instance, error) {
	if conf.GRPCServer == nil {
		return nil, errors.New("GRPCServer instance is required")
	}

	log = logrus.WithField("category", "gubernator")
	if err := conf.SetDefaults(); err != nil {
		return nil, err
	}

	s := Instance{
		conf: conf,
	}

	s.global = newGlobalManager(conf.Behaviors, &s)

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

func (s *Instance) Close() error {
	if s.isClosed {
		return nil
	}

	if s.conf.Loader == nil {
		return nil
	}

	out := make(chan *CacheItem, 500)
	go func() {
		for item := range s.conf.Cache.Each() {
			fmt.Printf("Each: %+v\n", item)
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
func (s *Instance) GetRateLimits(ctx context.Context, r *GetRateLimitsReq) (*GetRateLimitsResp, error) {
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
		fan := holster.NewFanOut(1000)
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
						Error: fmt.Sprintf("GetPeer() keeps returning peers that are in a closing state for '%s' - '%s'", globalKey, err),
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
				if peer.isOwner {
					// Apply our rate limit algorithm to the request
					inOut.Out, err = s.getRateLimit(inOut.In)
					if err != nil {
						inOut.Out = &RateLimitResp{
							Error: fmt.Sprintf("while applying rate limit for '%s' - '%s'", globalKey, err),
						}
					}
				} else {
					if inOut.In.Behavior == Behavior_GLOBAL {
						inOut.Out, err = s.getGlobalRateLimit(inOut.In)
						if err != nil {
							inOut.Out = &RateLimitResp{Error: err.Error()}
						}
						out <- inOut
						return nil
					}

					// Make an RPC call to the peer that owns this rate limit
					inOut.Out, err = peer.GetPeerRateLimit(ctx, inOut.In)
					if err != nil {
						if err == ErrClosing {
							attempts++
							goto getPeer
						}
						inOut.Out = &RateLimitResp{
							Error: fmt.Sprintf("while fetching rate limit '%s' from peer - '%s'", globalKey, err),
						}
					}

					// Inform the client of the owner key of the key
					inOut.Out.Metadata = map[string]string{"owner": peer.host}
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
func (s *Instance) getGlobalRateLimit(req *RateLimitReq) (*RateLimitResp, error) {
	// Queue the hit for async update
	s.global.QueueHit(req)

	s.conf.Cache.Lock()
	item, ok := s.conf.Cache.GetItem(req.HashKey())
	s.conf.Cache.Unlock()
	if ok {
		// Global rate limits are always stored as RateLimitResp regardless of algorithm
		rl, ok := item.Value.(*RateLimitResp)
		if !ok {
			return nil, errors.New("Global rate limit is of incorrect type")
		}
		return rl, nil
	} else {
		cpy := *req
		cpy.Behavior = Behavior_NO_BATCHING
		// Process the rate limit like we own it since we have no data on the rate limit
		resp, err := s.getRateLimit(&cpy)
		return resp, err
	}
}

// UpdatePeerGlobals updates the local cache with a list of global rate limits. This method should only
// be called by a peer who is the owner of a global rate limit.
func (s *Instance) UpdatePeerGlobals(ctx context.Context, r *UpdatePeerGlobalsReq) (*UpdatePeerGlobalsResp, error) {
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
func (s *Instance) GetPeerRateLimits(ctx context.Context, r *GetPeerRateLimitsReq) (*GetPeerRateLimitsResp, error) {
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
func (s *Instance) HealthCheck(ctx context.Context, r *HealthCheckReq) (*HealthCheckResp, error) {
	s.peerMutex.RLock()
	defer s.peerMutex.RUnlock()
	return &s.health, nil
}

func (s *Instance) getRateLimit(r *RateLimitReq) (*RateLimitResp, error) {
	s.conf.Cache.Lock()
	defer s.conf.Cache.Unlock()

	if r.Behavior == Behavior_GLOBAL {
		s.global.QueueUpdate(r)
	}

	switch r.Algorithm {
	case Algorithm_TOKEN_BUCKET:
		return tokenBucket(s.conf.Store, s.conf.Cache, r)
	case Algorithm_LEAKY_BUCKET:
		return leakyBucket(s.conf.Store, s.conf.Cache, r)
	}
	return nil, errors.Errorf("invalid rate limit algorithm '%d'", r.Algorithm)
}

// SetPeers is called when the pool of peers changes
func (s *Instance) SetPeers(peers []PeerInfo) {
	picker := s.conf.Picker.New()
	var errs []string

	for _, peer := range peers {
		peerInfo := s.conf.Picker.GetPeerByHost(peer.Address)
		// If we don't have an existing connection, we need to open one.
		if peerInfo == nil {
			var err error
			peerInfo, err = NewPeerClient(s.conf.Behaviors, peer.Address)
			if err != nil {
				errs = append(errs,
					fmt.Sprintf("failed to connect to peer '%s'; consistent hash is incomplete", peer.Address))
				continue
			}

		}

		// If this peer refers to this server instance
		peerInfo.isOwner = peer.IsOwner

		picker.Add(peerInfo)
	}

	s.peerMutex.Lock()

	oldPicker := s.conf.Picker

	// Replace our current picker
	s.conf.Picker = picker

	// Update our health status
	s.health.Status = Healthy
	if len(errs) != 0 {
		s.health.Status = UnHealthy
		s.health.Message = strings.Join(errs, "|")
	}
	s.health.PeerCount = int32(picker.Size())
	s.peerMutex.Unlock()

	log.WithField("peers", peers).Info("Peers updated")

	// Shutdown any old peers we no longer need
	wg := sync.WaitGroup{}
	ctx, cancel := context.WithTimeout(context.Background(), s.conf.Behaviors.BatchTimeout)
	defer cancel()

	var shutdownPeers []*PeerClient

	for _, p := range oldPicker.Peers() {
		peerInfo := s.conf.Picker.GetPeerByHost(p.host)
		// If this peerInfo is not found, we are no longer using this host
		// and need to shut it down
		if peerInfo == nil {
			shutdownPeers = append(shutdownPeers, p)
			wg.Add(1)
			go func() {
				err := p.Shutdown(ctx)
				if err != nil {
					log.WithError(err).WithField("peer", p).Error("while shutting down peer")
				}
			}()
		}
	}

	wg.Wait()
	if len(shutdownPeers) > 0 {
		log.WithField("peers", shutdownPeers).Info("Peers shutdown")
	}
}

// GetPeers returns a peer client for the hash key provided
func (s *Instance) GetPeer(key string) (*PeerClient, error) {
	s.peerMutex.RLock()
	peer, err := s.conf.Picker.Get(key)
	if err != nil {
		s.peerMutex.RUnlock()
		return nil, err
	}
	s.peerMutex.RUnlock()
	return peer, nil
}

func (s *Instance) GetPeerList() []*PeerClient {
	s.peerMutex.RLock()
	defer s.peerMutex.RUnlock()
	return s.conf.Picker.Peers()
}

// Describe fetches prometheus metrics to be registered
func (s *Instance) Describe(ch chan<- *prometheus.Desc) {
	ch <- s.global.asyncMetrics.Desc()
	ch <- s.global.broadcastMetrics.Desc()
}

// Collect fetches metrics from the server for use by prometheus
func (s *Instance) Collect(ch chan<- prometheus.Metric) {
	ch <- s.global.asyncMetrics
	ch <- s.global.broadcastMetrics
}
