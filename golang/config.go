package gubernator

import (
	"fmt"
	"github.com/mailgun/gubernator/golang/cache"
	"github.com/mailgun/gubernator/golang/metrics"
	"github.com/mailgun/holster"
	"time"
)

type UpdateFunc func(*PeerConfig)

// Syncs configs and peer listings between peers
type PeerSyncer interface {
	RegisterOnUpdate(UpdateFunc)
	Start(string) error
	Stop()
}

// Config shared across peers via the PeerSyncer
type PeerConfig struct {
	Peers []string
}

type HTTPConfig struct {
	// This is the interface and port number the service will listen to for HTTP connections
	ListenAddress string `json:"listen-address"`

	// This is the interface and port number the http service will forward GRPC requests too
	GubernatorAddress string `json:"gubernator-address"`
}

// Local config for our service instance
type Config struct {
	// This is the interface and port number the service will listen to for GRPC connections
	// If unset, the server will pick a random port on localhost. You can retrieve the listening
	// address and port by calling `server.Address()`.
	ListenAddress string `json:"listen-address"`

	// This is the address and port number the service will advertise to other peers in the cluster
	// If unset, defaults to `GRPCListenAddress`.
	AdvertiseAddress string `json:"advertise-address"`

	// The cache implementation
	Cache cache.Cache

	// This is the implementation of peer syncer this server will use to keep all the peer
	// configurations in sync across the cluster.
	PeerSyncer PeerSyncer

	// This is the peer picker algorithm the server will use decide which peer in the cluster
	// will coordinate a rate limit
	Picker PeerPicker

	// Metrics collector
	Metrics metrics.Collector

	// Adjust how gubernator behaviors are configured
	Behaviors BehaviorConfig
}

type BehaviorConfig struct {
	// How long we should wait for a batched response from a peer
	BatchTimeout time.Duration
	// How long we should wait before sending a batched request
	BatchWait time.Duration
	// The max number of requests we can batch into a single peer request
	BatchLimit int
}

func ApplyConfigDefaults(c *Config) error {
	holster.SetDefault(&c.Behaviors.BatchTimeout, time.Millisecond*500)
	holster.SetDefault(&c.Behaviors.BatchLimit, maxBatchSize)
	holster.SetDefault(&c.Behaviors.BatchWait, time.Microsecond*500)
	holster.SetDefault(&c.Picker, NewConsistantHash(nil))
	holster.SetDefault(&c.Cache, cache.NewLRUCache(cache.LRUCacheConfig{}))

	if c.Behaviors.BatchLimit > maxBatchSize {
		return fmt.Errorf("Behaviors.BatchLimit cannot exceed '%d'", maxBatchSize)
	}
	return nil
}

// An implementation of PeerSyncer suitable for testing local clusters
type LocalPeerSyncer struct {
	callbacks []UpdateFunc
}

// Emits a new cluster config to all registered callbacks
func (sc *LocalPeerSyncer) Update(config PeerConfig) {
	for _, cb := range sc.callbacks {
		cb(&config)
	}
}

func (sc *LocalPeerSyncer) RegisterOnUpdate(cb UpdateFunc) {
	sc.callbacks = append(sc.callbacks, cb)
}

func (sc *LocalPeerSyncer) Start(addr string) error { return nil }
func (sc *LocalPeerSyncer) Stop()                   {}
