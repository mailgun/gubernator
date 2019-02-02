package main

import (
	"context"
	"github.com/mailgun/gubernator/cache"

	"github.com/mailgun/gubernator"
	"github.com/mailgun/gubernator/metrics"
	"github.com/mailgun/gubernator/sync"
	"github.com/mailgun/service"
)

type Config struct {
	service.BasicConfig
}

type Service struct {
	service.BasicService

	srv  *gubernator.Server
	conf Config
}

func (s *Service) Start(ctx context.Context) error {
	var err error

	// TODO: Get config from radar
	s.srv, err = gubernator.NewServer(gubernator.ServerConfig{
		Picker:     gubernator.NewConsistantHash(nil),
		PeerSyncer: sync.NewEtcdSync(service.Etcd()),
		Cache:      cache.NewLRUCache(cache.LRUCacheConfig{}),
		Metrics:    metrics.NewStatsdMetrics(metrics.StatsdConfig{}),
	})
	return err
}

func (s *Service) Stop() error {
	s.srv.Stop()
	return nil
}

func main() {
	var svc Service
	service.Run(&svc.conf, &svc, service.WithName("gubernator"))
}
