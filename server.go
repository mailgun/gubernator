package gubernator

import (
	"context"
	"github.com/mailgun/gubernator/lru"
	"github.com/mailgun/gubernator/pb"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"net"
	"sync"
	"time"
)

const (
	maxRequestSize = 1 * 1024 * 1024 // 1Mb
)

type Server struct {
	listener   net.Listener
	grpcServer *grpc.Server
	cache      *lru.Cache
	mutex      sync.Mutex
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
		// TODO: Set a limit on the size of the cache, so old entries expire
		cache: lru.NewLRUCache(0),
	}
	pb.RegisterRateLimitServiceServer(server, &s)
	return &s, nil
}

// Runs the gRPC server; blocks until server stops
func (s *Server) Run() error {
	// TODO: Perhaps allow resizing the cache on the fly depending on the number of cache hits
	// TODO: Emit metrics

	// TODO: Create PeerSync service that uses leader election in ETCD and can sync the list of peers
	// TODO: Implement a GRPC interface to retrieve the peer listing from the CH for rate limit clients

	// TODO: PeerSync (Custom RAFT Implementation)
	// TODO: Registering - server just came up and is attempting to find the leader to get the peer list
	// TODO: Follower - server has the peer list, but is not the leader, waits for peer list updates from the leader
	// TODO: Leader - server has the peer list and is authoritative, sends peer list to all followers
	// TODO: Implement a GRPC interface to Register and Send a Peer list from the leader to the followers

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

func (s *Server) GetRateLimit(ctx context.Context, req *pb.RateLimitRequest) (*pb.RateLimitResponse, error) {
	// TODO: Implement for generic clients

	// TODO: Optionally verify we are the owner of this key
	// TODO: Forward the request to the correct owner if needed
	return nil, nil
}

func (s *Server) GetRateLimitByKey(ctx context.Context, req *pb.RateLimitKeyRequest) (*pb.RateLimitResponse, error) {
	var results []*pb.DescriptorStatus
	for _, entry := range req.Entries {
		status, err := s.getEntryStatus(ctx, entry)
		if err != nil {
			return nil, err
		}
		results = append(results, status)
	}
	return &pb.RateLimitResponse{Statuses: results}, nil
}

func (s *Server) getEntryStatus(ctx context.Context, entry *pb.RateLimitKeyRequest_Entry) (*pb.DescriptorStatus, error) {
	if entry.Hits == 0 {
		entry.Hits = 1
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()
	item, ok := s.cache.Get(string(entry.Key))
	if ok {
		// The following semantic allows for requests of more than the limit to be rejected, but subsequent
		// requests within the same duration that are under the limit to succeed. IE: client attempts to
		// send 1000 emails but 100 is their limit. The request is rejected as over the limit, but since we
		// don't store OVER_LIMIT in the cache the client can retry within the same rate limit duration with
		// 100 emails and the request will succeed.

		status := item.(*pb.DescriptorStatus)
		// If we are already at the limit
		if status.LimitRemaining == 0 {
			status.Status = pb.DescriptorStatus_OVER_LIMIT
			return status, nil
		}

		// If requested hits takes the remainder
		if status.LimitRemaining == entry.Hits {
			status.LimitRemaining = 0
			return status, nil
		}

		// If requested is more than available, then return over the limit without updating the cache.
		if entry.Hits > status.LimitRemaining {
			retStatus := *status
			retStatus.Status = pb.DescriptorStatus_OVER_LIMIT
			return &retStatus, nil
		}

		status.LimitRemaining -= entry.Hits
		return status, nil
	}

	if entry.RateLimit == nil {
		return nil, errors.New("required field 'RateLimit' missing from 'RateLimitKeyRequest_Entry'")
	}

	// Add a new rate limit to the cache
	expire := time.Now().Add(time.Duration(entry.RateLimit.Duration) * time.Millisecond)
	status := &pb.DescriptorStatus{
		Status:         pb.DescriptorStatus_OK,
		CurrentLimit:   entry.RateLimit.Requests,
		LimitRemaining: entry.RateLimit.Requests - entry.Hits,
		ResetTime:      expire.Unix() / Second,
	}

	// Kind of a weird corner case, but the client could be dumb
	if entry.Hits > entry.RateLimit.Requests {
		status.LimitRemaining = 0
	}

	s.cache.Add(string(entry.Key), status, expire)

	return status, nil
}
