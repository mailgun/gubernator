package main

import (
	"context"
	"net/http"
	"os"

	"github.com/mailgun/gubernator"
	"github.com/mailgun/gubernator/cache"
	"github.com/mailgun/gubernator/metrics"
	"github.com/mailgun/gubernator/pb"
	"github.com/mailgun/gubernator/sync"
	"github.com/mailgun/service"
	"github.com/mailgun/service/httpapi"
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

	err = s.AddHandler(httpapi.Spec{
		Method: "GET",
		Path:   "/healthz",
		Scope:  httpapi.ScopeProtected,
		Handler: func(w http.ResponseWriter, r *http.Request, p map[string]string) (interface{}, error) {
			return s.srv.HealthCheck(r.Context(), &pb.HealthCheckRequest{})
		},
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
	service.Run(&svc.conf, &svc,
		service.WithName("gubernator"),
		service.WithInstanceID(os.Getenv("GUBER_ID")),
	)
}
