package gubernator

import (
	"context"
	"github.com/mailgun/gubernator/pb"
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
	pb.RegisterRateLimitServiceServer(server, &s)
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
func (s *Server) ShouldRateLimit(ctx context.Context, req *pb.RateLimitRequest) (*pb.RateLimitResponse, error) {
	return nil, nil
}

// Client implementations should use this method since they calculate the key and know which peer to use.
func (s *Server) ShouldRateLimitByKey(ctx context.Context, req *pb.RateLimitKeyRequest) (*pb.RateLimitResponse, error) {

	/*for _, entry := range req.Entries {

	}*/
	return &pb.RateLimitResponse{}, nil
}

func (s *Server) getRateLimt(ctx context.Context, entry *pb.KeyRequestEntry) (*pb.DescriptorStatus, error) {
	// Get from cache
	item, ok := s.cache.Get(string(entry.Key))
	if ok {
		status := item.(*pb.DescriptorStatus)
		status.LimitRemaining = status.CurrentLimit - entry.Hits
		if status.LimitRemaining <= 0 {
			status.Code = pb.DescriptorStatus_OVER_LIMIT
		}
		return status, nil
	}

	// Add a new rate limit
	status := &pb.DescriptorStatus{
		Code:           pb.DescriptorStatus_OK,
		CurrentLimit:   10,
		LimitRemaining: entry.Hits - 10,
	}
	s.cache.Add(string(entry.Key), status)

	return status, nil
}
