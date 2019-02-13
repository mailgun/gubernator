package gubernator

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/mailgun/gubernator/pb"
	"github.com/mailgun/holster"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

const (
	maxRequestSize = 1 * 1024 * 1024 // 1Mb
	Healthy        = "healthy"
	UnHealthy      = "unhealthy"
)

type GRPCServer struct {
	health    pb.HealthCheckResponse
	wg        holster.WaitGroup
	log       *logrus.Entry
	conf      ServerConfig
	listener  net.Listener
	server    *grpc.Server
	peerMutex sync.RWMutex
	client    *PeerClient
}

// New creates a server instance.
func NewGRPCServer(conf ServerConfig) (*GRPCServer, error) {
	listener, err := net.Listen("tcp", conf.GRPCListenAddress)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to listen on %s", conf.GRPCListenAddress)
	}

	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(maxRequestSize),
		grpc.StatsHandler(conf.Metrics.GRPCStatsHandler()))

	s := GRPCServer{
		log:      logrus.WithField("category", "grpc"),
		listener: listener,
		server:   server,
		conf:     conf,
	}

	// Register our server with GRPC
	pb.RegisterRateLimitServiceServer(server, &s)
	pb.RegisterPeersServiceServer(server, &s)

	// Register our peer update callback
	s.conf.PeerSyncer.RegisterOnUpdate(s.updatePeers)

	// Register cache stats with out metrics collector
	s.conf.Metrics.RegisterCacheStats(s.conf.Cache)

	// Advertise address is our listen address if not specified
	holster.SetDefault(&s.conf.GRPCAdvertiseAddress, s.Address())

	return &s, nil
}

// Runs the gRPC server; returns when the server starts
func (s *GRPCServer) Start() error {
	// Start the cache
	if err := s.conf.Cache.Start(); err != nil {
		return errors.Wrap(err, "failed to start cache")
	}

	// Start the metrics collector
	if err := s.conf.Metrics.Start(); err != nil {
		return errors.Wrap(err, "failed to start metrics collector")
	}

	// Start the GRPC server
	errs := make(chan error, 1)
	s.wg.Go(func() {
		// Serve will return a non-nil error unless Stop or GracefulStop is called.
		errs <- s.server.Serve(s.listener)
	})

	// Ensure the server is running before we return
	go func() {
		errs <- retry(2, time.Millisecond*500, func() error {
			conn, err := grpc.Dial(s.listener.Addr().String(), grpc.WithInsecure())
			if err != nil {
				return err
			}
			client := pb.NewRateLimitServiceClient(conn)
			ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*500)
			defer cancel()
			_, err = client.HealthCheck(ctx, &pb.HealthCheckRequest{})
			return err
		})
	}()

	// Wait until the server starts or errors
	err := <-errs
	if err != nil {
		return errors.Wrap(err, "while waiting for server to pass health check")
	}

	// Now that our service is up, register our server
	if err := s.conf.PeerSyncer.Start(s.conf.GRPCAdvertiseAddress); err != nil {
		return errors.Wrap(err, "failed to sync configs with other peers")
	}

	s.log.Infof("GRPC Listening on %s ...", s.Address())
	return nil
}

func (s *GRPCServer) Stop() {
	s.server.Stop()
	s.conf.PeerSyncer.Stop()
	s.conf.Metrics.Stop()
	s.wg.Wait()
}

// Return the address the server is listening too
func (s *GRPCServer) Address() string {
	return s.listener.Addr().String()
}

func (s *GRPCServer) GetRateLimits(ctx context.Context, reqs *pb.RateLimitRequestList) (*pb.RateLimitResponseList, error) {
	var result pb.RateLimitResponseList

	// TODO: Support getting multiple keys in an async manner (FanOut)
	for i, req := range reqs.RateLimits {
		if req.RateLimitConfig == nil {
			return nil, errors.Errorf("required field 'RateLimitConfig' missing from 'RateLimit[%d]'", i)
		}

		if req.Namespace == "" {
			return nil, errors.New("must provide a 'namespace'; cannot be empty")
		}

		if len(req.UniqueKey) == 0 {
			return nil, errors.New("must provide a unique_key; cannot be empty")
		}

		globalKey := req.Namespace + "_" + req.UniqueKey

		s.peerMutex.RLock()
		peer, err := s.conf.Picker.Get(globalKey)
		if err != nil {
			s.peerMutex.RUnlock()
			return nil, errors.Wrapf(err, "while finding peer that owns key '%s'", globalKey)
		}
		s.peerMutex.RUnlock()

		var resp *pb.RateLimitResponse

		// If our server instance is the owner of this rate limit
		if peer.isOwner {
			// Apply our rate limit algorithm to the request
			resp, err = s.applyAlgorithm(req)
			if err != nil {
				return nil, errors.Wrapf(err, "while fetching key '%s' from peer", globalKey)
			}
		} else {
			// Make an RPC call to the peer that owns this rate limit
			resp, err = peer.GetPeerRateLimits(ctx, req)
			if err != nil {
				return nil, errors.Wrapf(err, "while fetching key '%s' from peer", globalKey)
			}

			// Inform the client of the owner key of the key
			resp.Metadata = map[string]string{"owner": peer.host}
		}
		result.RateLimits = append(result.RateLimits, resp)
	}
	return &result, nil
}

func (s *GRPCServer) GetPeerRateLimits(ctx context.Context, req *pb.PeerRateLimitRequest) (*pb.PeerRateLimitResponse, error) {
	var resp pb.PeerRateLimitResponse

	for _, entry := range req.RateLimits {
		status, err := s.applyAlgorithm(entry)
		if err != nil {
			return nil, err
		}
		resp.RateLimits = append(resp.RateLimits, status)
	}
	return &resp, nil
}

// Returns the health of the peer.
func (s *GRPCServer) HealthCheck(ctx context.Context, in *pb.HealthCheckRequest) (*pb.HealthCheckResponse, error) {
	s.peerMutex.RLock()
	defer s.peerMutex.RUnlock()
	return &s.health, nil
}

func (s *GRPCServer) applyAlgorithm(entry *pb.RateLimitRequest) (*pb.RateLimitResponse, error) {
	if entry.RateLimitConfig == nil {
		return nil, errors.New("required field 'RateLimitConfig' missing from 'RateLimitKeyRequest_Entry'")
	}

	s.conf.Cache.Lock()
	defer s.conf.Cache.Unlock()

	switch entry.RateLimitConfig.Algorithm {
	case pb.RateLimitConfig_TOKEN_BUCKET:
		return tokenBucket(s.conf.Cache, entry)
	case pb.RateLimitConfig_LEAKY_BUCKET:
		return leakyBucket(s.conf.Cache, entry)
	}
	return nil, errors.Errorf("invalid rate limit algorithm '%d'", entry.RateLimitConfig.Algorithm)
}

// Called by PeerSyncer when the cluster config changes
func (s *GRPCServer) updatePeers(conf *PeerConfig) {
	picker := s.conf.Picker.New()
	var errs []string

	for _, peer := range conf.Peers {
		peerInfo, err := NewPeerClient(peer)
		if err != nil {
			errs = append(errs, fmt.Sprintf("failed to connect to peer '%s'; consistent hash is incomplete", peer))
			continue
		}

		if info := s.conf.Picker.GetPeer(peer); info != nil {
			peerInfo = info
		}

		// If this peer refers to this server instance
		if peer == s.conf.GRPCAdvertiseAddress {
			peerInfo.isOwner = true
		}

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
	s.log.WithField("peers", conf.Peers).Debug("Peers updated")
}

func retry(attempts int, d time.Duration, callback func() error) (err error) {
	for i := 0; i < attempts; i++ {
		err = callback()
		if err == nil {
			return nil
		}
		time.Sleep(d)
	}
	return err
}
