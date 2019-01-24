package gubernator

import (
	"context"
	"github.com/mailgun/gubernator/proto"
	"github.com/mailgun/holster"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"net"
)

const (
	maxRequestSize = 1 * 1024 * 1024 // 1Mb
)

type Server struct {
	listener   net.Listener
	grpcServer *grpc.Server
	cache      *holster.LRUCache
}

// New creates a gRPC server instance.
func NewServer(address string) (*Server, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, errors.Wrap(err, "failed to listen")
	}

	server := grpc.NewServer(grpc.MaxRecvMsgSize(maxRequestSize))
	s := Server{
		listener:   listener,
		grpcServer: server,
		cache:      holster.NewLRUCache(0),
	}
	proto.RegisterRateLimitServiceServer(server, &s)
	return &s, nil
}

// Runs the gRPC server; blocks until server stops
func (s *Server) Run() error {
	// TODO: Run Cache Job that Purges the cache of TTL'd entries
	/*go func() {
		for {
			fmt.Printf("Size: %d\n", s.cache.Size())
			time.Sleep(time.Second)
		}
	}()*/

	return s.grpcServer.Serve(s.listener)
}

// Stops gRPC server
func (s *Server) Stop() {
	s.grpcServer.Stop()
}

// Return the address the server is listening too
func (s *Server) Address() string {
	return s.listener.Addr().String()
}

// Determine whether rate limiting should take place.
func (s *Server) ShouldRateLimit(ctx context.Context, req *proto.RateLimitRequest) (*proto.RateLimitResponse, error) {
	return nil, nil
}

// Client implementations should use this method since they calculate the key and know which peer to use.
func (s *Server) GetRateLimitByKey(ctx context.Context, req *proto.RateLimitKeyRequest) (*proto.RateLimitResponse, error) {
	// Get from cache
	item, ok := s.cache.Get(string(req.Key))
	if ok {
		status := item.(*proto.RateLimitResponse_DescriptorStatus)
		status.LimitRemaining = status.CurrentLimit.RequestsPerUnit - req.HitsAddend
		if status.LimitRemaining <= 0 {
			status.Code = proto.RateLimitResponse_OVER_LIMIT
		}
		return &proto.RateLimitResponse{
			OverallCode: status.Code,
			Statuses:    []*proto.RateLimitResponse_DescriptorStatus{status},
		}, nil
	}

	// Add a new rate limit
	status := &proto.RateLimitResponse_DescriptorStatus{
		Code: proto.RateLimitResponse_OK,
		CurrentLimit: &proto.RateLimit{
			RequestsPerUnit: 10,
		},
		LimitRemaining: req.HitsAddend - 10,
	}
	s.cache.Add(string(req.Key), status)

	return &proto.RateLimitResponse{
		OverallCode: status.Code,
		Statuses:    []*proto.RateLimitResponse_DescriptorStatus{status},
	}, nil
}
