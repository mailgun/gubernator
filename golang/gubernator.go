package gubernator

import (
	"context"
	"fmt"
	"github.com/mailgun/holster"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"strings"
	"sync"
)

const (
	maxBatchSize = 1000
	Healthy      = "healthy"
	UnHealthy    = "unhealthy"
)

var log *logrus.Entry

type Instance struct {
	health    HealthCheckResp
	wg        holster.WaitGroup
	conf      Config
	peerMutex sync.RWMutex
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

	// Register our server with GRPC
	RegisterRateLimitServiceV1Server(conf.GRPCServer, &s)
	RegisterPeersServiceV1Server(conf.GRPCServer, &s)

	if s.conf.Metrics != nil {
		s.conf.Metrics.RegisterCacheStats(s.conf.Cache)
	}

	return &s, nil
}

func (s *Instance) GetRateLimits(ctx context.Context, r *Requests) (*RateLimits, error) {
	var resp RateLimits

	if len(r.Requests) > maxBatchSize {
		return nil, status.Errorf(codes.OutOfRange,
			"Requests.RateLimits list too large; max size is '%d'", maxBatchSize)
	}

	// TODO: Support getting multiple keys in an async manner (FanOut)
	for _, req := range r.Requests {
		globalKey := req.Name + "_" + req.UniqueKey
		var rl *RateLimit
		var peer *PeerClient
		var err error

		if len(req.UniqueKey) == 0 {
			rl = &RateLimit{Error: "field 'unique_key' cannot be empty"}
			goto NextRateLimit
		}

		if len(req.Name) == 0 {
			rl = &RateLimit{Error: "field 'namespace' cannot be empty"}
			goto NextRateLimit
		}

		s.peerMutex.RLock()
		peer, err = s.conf.Picker.Get(globalKey)
		if err != nil {
			s.peerMutex.RUnlock()
			rl = &RateLimit{
				Error: fmt.Sprintf("while finding peer that owns rate limit '%s' - '%s'", globalKey, err),
			}
			goto NextRateLimit
		}
		s.peerMutex.RUnlock()

		// If our server instance is the owner of this rate limit
		if peer.isOwner {
			// Apply our rate limit algorithm to the request
			rl, err = s.getRateLimit(req)
			if err != nil {
				rl = &RateLimit{
					Error: fmt.Sprintf("while applying rate limit for '%s' - '%s'", globalKey, err),
				}
				goto NextRateLimit
			}
		} else {
			// Make an RPC call to the peer that owns this rate limit
			rl, err = peer.GetPeerRateLimit(ctx, req)
			if err != nil {
				rl = &RateLimit{
					Error: fmt.Sprintf("while fetching rate limit '%s' from peer - '%s'", globalKey, err),
				}
			}

			// Inform the client of the owner key of the key
			rl.Metadata = map[string]string{"owner": peer.host}
		}
	NextRateLimit:
		resp.RateLimits = append(resp.RateLimits, rl)
	}
	return &resp, nil
}

// TO
func (s *Instance) UpdatePeerGlobals(ctx context.Context, r *UpdatePeerGlobalsReq) (*UpdatePeerGlobalsResp, error) {
	// NOT IMPLEMENTED
	return nil, nil
}

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
			rl = &RateLimit{Error: err.Error()}
		}
		resp.RateLimits = append(resp.RateLimits, rl)
	}
	return &resp, nil
}

// Returns the health of the peer.
func (s *Instance) HealthCheck(ctx context.Context, r *HealthCheckReq) (*HealthCheckResp, error) {
	s.peerMutex.RLock()
	defer s.peerMutex.RUnlock()
	return &s.health, nil
}

func (s *Instance) getRateLimit(r *Request) (*RateLimit, error) {
	s.conf.Cache.Lock()
	defer s.conf.Cache.Unlock()

	switch r.Algorithm {
	case Algorithm_TOKEN_BUCKET:
		return tokenBucket(s.conf.Cache, r)
	case Algorithm_LEAKY_BUCKET:
		return leakyBucket(s.conf.Cache, r)
	}
	return nil, errors.Errorf("invalid rate limit algorithm '%d'", r.Algorithm)
}

// Called when the pool of peers changes
func (s *Instance) SetPeers(peers []PeerInfo) {
	picker := s.conf.Picker.New()
	var errs []string

	for _, peer := range peers {
		peerInfo, err := NewPeerClient(s.conf.Behaviors, peer.Address)
		if err != nil {
			errs = append(errs,
				fmt.Sprintf("failed to connect to peer '%s'; consistent hash is incomplete", peer.Address))
			continue
		}

		if info := s.conf.Picker.GetPeer(peer.Address); info != nil {
			peerInfo = info
		}

		// If this peer refers to this server instance
		peerInfo.isOwner = peer.IsOwner

		picker.Add(peerInfo)
	}

	// TODO: schedule a disconnect for old PeerClients once they are no longer in flight

	s.peerMutex.Lock()
	defer s.peerMutex.Unlock()

	// Replace our current picker
	s.conf.Picker = picker

	// Update our health status
	s.health.Status = Healthy
	if len(errs) != 0 {
		s.health.Status = UnHealthy
		s.health.Message = strings.Join(errs, "|")
	}
	s.health.PeerCount = int32(picker.Size())
	log.WithField("peers", peers).Debug("Peers updated")
}
