package main

import (
	"context"

	"github.com/mailgun/gubernator"
	"github.com/mailgun/gubernator/cache"
	"github.com/mailgun/gubernator/metrics"
	"github.com/mailgun/gubernator/sync"
	"github.com/mailgun/service"
)

var Version = "dev-build"

type Config struct {
	service.BasicConfig

	LRUCache         cache.LRUCacheConfig
	ListenAddress    string
	AdvertiseAddress string
}

type Service struct {
	service.BasicService

	srv  *gubernator.Server
	conf Config
}

func (s *Service) Start(ctx context.Context) error {
	var err error

	s.srv, err = gubernator.NewServer(gubernator.ServerConfig{
		Metrics:          metrics.NewStatsdMetrics(service.Metrics()),
		Picker:           gubernator.NewConsistantHash(nil),
		Cache:            cache.NewLRUCache(s.conf.LRUCache),
		PeerSyncer:       sync.NewEtcdSync(service.Etcd()),
		AdvertiseAddress: s.conf.AdvertiseAddress,
		ListenAddress:    s.conf.ListenAddress,
	})
	if err != nil {
		return err
	}

	return s.srv.Start()
}

func (s *Service) Stop() error {
	s.srv.Stop()
	return nil
}

func main() {
	var svc Service
	service.Run(&svc.conf, &svc, service.WithName("gubernator"))
}
