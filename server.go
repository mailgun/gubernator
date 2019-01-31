package gubernator

import (
	"context"
	"net"
	"sync"

	"github.com/mailgun/gubernator/lru"
	"github.com/mailgun/gubernator/pb"
	"github.com/pkg/errors"
	"github.com/valyala/bytebufferpool"
	"google.golang.org/grpc"
)

const (
	maxRequestSize = 1 * 1024 * 1024 // 1Mb
)

type Server struct {
	listener   net.Listener
	grpc       *grpc.Server
	cache      *lru.Cache
	cacheMutex sync.Mutex
	peerMutex  sync.RWMutex
	client     *PeerClient
	conf       ServerConfig
}

// New creates a server instance.
func NewServer(conf ServerConfig) (*Server, error) {
	listener, err := net.Listen("tcp", conf.ListenAddress)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to listen on %s", conf.ListenAddress)
	}

	server := grpc.NewServer(grpc.MaxRecvMsgSize(maxRequestSize))
	s := Server{
		cache:    lru.NewLRUCache(conf.MaxCacheSize),
		listener: listener,
		grpc:     server,
		conf:     conf,
	}
	// Register our server with grpc
	pb.RegisterRateLimitServiceServer(server, &s)
	pb.RegisterConfigServerServer(server, &s)

	// Register our config update callback
	conf.PeerSyncer.OnUpdate(s.updateConfig)

	if conf.AdvertiseAddress == "" {
		conf.AdvertiseAddress = s.Address()
	}

	return &s, nil
}

// Runs the gRPC server; blocks until server stops
func (s *Server) Run() error {
	// TODO: Allow resizing the cache on the fly depending on the number of cache
	// TODO: hits, so we don't use the MAX cache all the time

	// TODO: Emit metrics <-- (THRAWN) Do this next
	// Create a Metrics client which can be configured via `ServerConfig`

	/*go func() {
		for {
			fmt.Printf("Size: %d\n", s.cache.Size())
			time.Sleep(time.Second)
		}
	}()*/

	if err := s.conf.PeerSyncer.Start(s.conf.AdvertiseAddress); err != nil {
		return errors.Wrap(err, "failed to sync configs with other peers")
	}

	return s.grpc.Serve(s.listener)
}

func (s *Server) Stop() {
	s.grpc.Stop()
	s.conf.PeerSyncer.Stop()
}

// Return the address the server is listening too
func (s *Server) Address() string {
	return s.listener.Addr().String()
}

// TODO: Determine what the server Quality of Service is and set context timeouts for each relayed request?
func (s *Server) GetRateLimit(ctx context.Context, req *pb.RateLimitRequest) (*pb.RateLimitResponse, error) {
	var results []*pb.DescriptorStatus

	// TODO: Support getting multiple keys in an async manner (FanOut)
	for _, desc := range req.Descriptors {
		// TODO: Get buffer out of pool
		var key bytebufferpool.ByteBuffer

		if err := generateKey(&key, req.Domain, desc); err != nil {
			return nil, err
		}

		if desc.RateLimitConfig == nil {
			return nil, errors.New("required field 'RateLimitConfig' missing from 'Descriptor'")
		}

		s.peerMutex.RLock()
		var peer PeerClient
		if err := s.conf.Picker.Get(key.B, &peer); err != nil {
			s.peerMutex.RUnlock()
			return nil, errors.Wrapf(err, "while finding peer that owns key '%s'", string(key.B))
		}
		s.peerMutex.RUnlock()

		var resp *pb.DescriptorStatus
		var err error

		// If our server instance is the owner of this rate limit
		if peer.isOwner {
			// Apply our rate limit algorithm to the request
			resp, err = s.applyAlgorithm(&pb.RateLimitKeyRequest_Entry{
				RateLimitConfig: desc.RateLimitConfig,
				Hits:            desc.Hits,
				Key:             key.B,
			})
			if err != nil {
				return nil, errors.Wrapf(err, "while fetching key '%s' from peer", string(key.B))
			}
		} else {
			// Make an RPC call to the peer that owns this rate limit
			resp, err = peer.GetRateLimitByKey(ctx, &pb.RateLimitKeyRequest_Entry{
				RateLimitConfig: desc.RateLimitConfig,
				Hits:            desc.Hits,
				Key:             key.B,
			})
			if err != nil {
				return nil, errors.Wrapf(err, "while fetching key '%s' from peer", string(key.B))
			}

			// Inform the client of the owner key of the key
			resp.Metadata = map[string]string{"owner": peer.host}
		}
		results = append(results, resp)
	}

	return &pb.RateLimitResponse{
		Statuses: results,
	}, nil
}

func (s *Server) GetRateLimitByKey(ctx context.Context, req *pb.RateLimitKeyRequest) (*pb.RateLimitResponse, error) {
	var results []*pb.DescriptorStatus
	for _, entry := range req.Entries {
		status, err := s.applyAlgorithm(entry)
		if err != nil {
			return nil, err
		}
		results = append(results, status)
	}
	return &pb.RateLimitResponse{Statuses: results}, nil
}

func (s *Server) GetPeers(ctx context.Context, in *pb.GetPeersRequest) (*pb.GetPeersResponse, error) {
	s.peerMutex.RLock()
	defer s.peerMutex.RUnlock()

	var results []string
	for _, peer := range s.conf.Picker.Peers() {
		results = append(results, peer.host)
	}

	return &pb.GetPeersResponse{
		Peers: results,
	}, nil
}

func (s *Server) applyAlgorithm(entry *pb.RateLimitKeyRequest_Entry) (*pb.DescriptorStatus, error) {
	if entry.Hits == 0 {
		entry.Hits = 1
	}

	if entry.RateLimitConfig == nil {
		return nil, errors.New("required field 'RateLimitConfig' missing from 'RateLimitKeyRequest_Entry'")
	}

	s.cacheMutex.Lock()
	defer s.cacheMutex.Unlock()

	switch entry.RateLimitConfig.Algorithm {
	case pb.RateLimitConfig_TOKEN_BUCKET:
		return tokenBucket(s.cache, entry)
	}
	return nil, errors.Errorf("invalid rate limit algorithm '%d'", entry.RateLimitConfig.Algorithm)
}

// TODO: Add a test for peer updates
// Called by PeerSyncer when the cluster config changes
func (s *Server) updateConfig(conf *PeerConfig) {
	picker := s.conf.Picker.New()

	for _, peer := range conf.Peers {
		peerInfo := NewPeerClient(peer)

		if info := s.conf.Picker.GetPeer(peer); info != nil {
			peerInfo = info
		}

		// If this peer refers to this server instance
		if peer == s.conf.AdvertiseAddress {
			peerInfo.isOwner = true
		}

		picker.Add(peerInfo)
	}

	// TODO: schedule a disconnect for old PeerClients once they are no longer in flight

	// Replace our current picker
	s.peerMutex.Lock()
	s.conf.Picker = picker
	s.peerMutex.Unlock()
}

func generateKey(b *bytebufferpool.ByteBuffer, domain string, descriptor *pb.Descriptor) error {

	// TODO: Check provided Domain
	// TODO: Check provided at least one entry

	b.Reset()
	b.WriteString(domain)
	b.WriteByte('_')

	for key, value := range descriptor.Values {
		b.WriteString(key)
		b.WriteByte('_')
		b.WriteString(value)
	}
	return nil
}
