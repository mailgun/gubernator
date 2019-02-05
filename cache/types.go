package cache

import "github.com/mailgun/gubernator/pb"

// Interface accepts any cache which returns cache stats
type CacheStats interface {
	Stats(bool) Stats
}

// So algorithms can interface with different cache implementations
type Cache interface {
	// Access methods
	Add(req *pb.RateLimitRequest, value interface{}, expireAt int64) bool
	UpdateExpiration(req *pb.RateLimitRequest, expireAt int64) bool
	Get(req *pb.RateLimitRequest) (value interface{}, ok bool)
	Remove(req *pb.RateLimitRequest)

	// Controls init and shutdown of the cache
	Start() error
	Stop()

	// If the cache is exclusive, this will control access to the cache
	Unlock()
	Lock()

	// Returns stats collected about keys in the cache
	Stats(bool) Stats
}

// A Key may be any value that is comparable. See http://golang.org/ref/spec#Comparison_operators
type Key interface{}

// Holds stats collected about the cache
type Stats struct {
	Size int64
	Miss int64
	Hit  int64
}
