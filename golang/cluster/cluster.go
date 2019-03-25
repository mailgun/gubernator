package cluster

import (
	"github.com/mailgun/gubernator/golang"
	"github.com/mailgun/gubernator/golang/cache"
	"github.com/mailgun/gubernator/golang/metrics"
	"github.com/pkg/errors"
)

var peers []string
var grpcServers []*gubernator.Instance
var httpServer *gubernator.HTTPServer

// Returns a random peer from the cluster
func GetPeer() string {
	return gubernator.RandomPeer(peers)
}

// Return the HTTP address for the clusters GRPC gateway
func GetHTTPAddress() string {
	return httpServer.Address()
}

// Start a local cluster of gubernator servers
func Start(numInstances int) error {
	var addresses []string
	for i := 0; i < numInstances; i++ {
		addresses = append(addresses, "")
	}
	return StartWith(addresses, "")
}

// Start a local cluster with specific addresses
func StartWith(addresses []string, httpAddress string) error {
	syncer := gubernator.LocalPeerSyncer{}
	var err error

	for _, address := range addresses {
		srv, err := gubernator.New(gubernator.Config{
			Metrics:       metrics.NewStatsdMetrics(&metrics.NullClient{}),
			Cache:         cache.NewLRUCache(cache.LRUCacheConfig{}),
			Picker:        gubernator.NewConsistantHash(nil),
			ListenAddress: address,
			PeerSyncer:    &syncer,
		})
		if err != nil {
			return errors.Wrap(err, "NewGRPCServer()")
		}
		peers = append(peers, srv.Address())
		if err := srv.Start(); err != nil {
			return errors.Wrap(err, "GRPCServer.Start()")
		}
		grpcServers = append(grpcServers, srv)
	}

	httpServer, err = gubernator.NewHTTPServer(gubernator.HTTPConfig{
		GubernatorAddress: grpcServers[0].Address(),
		ListenAddress:     httpAddress,
	})
	if err != nil {
		return errors.Wrap(err, "NewHTTPServer()")
	}

	if err := httpServer.Start(); err != nil {
		return errors.Wrap(err, "HTTPServer.Start()")
	}

	syncer.Update(gubernator.PeerConfig{
		Peers: peers,
	})

	return nil
}

func Stop() {
	httpServer.Stop()
	for _, srv := range grpcServers {
		srv.Stop()
	}
}
