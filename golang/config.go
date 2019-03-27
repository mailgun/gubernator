package gubernator

import (
	"fmt"
	"github.com/mailgun/gubernator/golang/cache"
	"github.com/mailgun/holster"
	"google.golang.org/grpc"
	"time"
)

// config for a gubernator instance
type Config struct {
	// Required
	GRPCServer *grpc.Server

	// (Optional) Adjust how gubernator behaviors are configured
	Behaviors BehaviorConfig

	// (Optional) The cache implementation
	Cache cache.Cache

	// (Optional) This is the peer picker algorithm the server will use decide which peer in the cluster
	// will coordinate a rate limit
	Picker PeerPicker
}

type BehaviorConfig struct {
	// How long we should wait for a batched response from a peer
	BatchTimeout time.Duration
	// How long we should wait before sending a batched request
	BatchWait time.Duration
	// The max number of requests we can batch into a single peer request
	BatchLimit int
}

func (c *Config) SetDefaults() error {
	holster.SetDefault(&c.Behaviors.BatchTimeout, time.Millisecond*500)
	holster.SetDefault(&c.Behaviors.BatchLimit, maxBatchSize)
	holster.SetDefault(&c.Behaviors.BatchWait, time.Microsecond*500)
	holster.SetDefault(&c.Picker, NewConsistantHash(nil))
	holster.SetDefault(&c.Cache, cache.NewLRUCache(0))

	if c.Behaviors.BatchLimit > maxBatchSize {
		return fmt.Errorf("Behaviors.BatchLimit cannot exceed '%d'", maxBatchSize)
	}
	return nil
}
