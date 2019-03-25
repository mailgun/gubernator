package main

import (
	"context"
	"fmt"
	"github.com/mailgun/holster"
	"os"

	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/mailgun/gubernator/golang"
	"github.com/mailgun/gubernator/golang/cache"
	"github.com/mailgun/gubernator/golang/metrics"
	"github.com/mailgun/gubernator/golang/sync"
	"github.com/mailgun/service"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
)

var Version = "dev-build"

type Config struct {
	service.BasicConfig

	GRPCAdvertiseAddress string               `json:"grpc-advertise-address"`
	LRUCache             cache.LRUCacheConfig `json:"lru-cache"`
}

type Service struct {
	service.BasicService

	grpcSrv *gubernator.Instance
	cancel  context.CancelFunc
	conf    Config
}

func (s *Service) Start(ctx context.Context) error {
	grpcAddress := fmt.Sprintf("0.0.0.0:%d", s.conf.GRPCPort)
	var err error

	// If not provided in the config, the advertise address should be the hostname:9091 port
	holster.SetDefault(&s.conf.GRPCAdvertiseAddress, fmt.Sprintf("%s:%d", s.Meta.FQDN, s.conf.GRPCPort))

	// TODO: Make Picker optional
	// TODO: Make PeerSyncer optional
	// TODO: Make metrics optional
	// TODO: Separate the library config from the server config
	// TODO: Rename `Namespace` to 'name' or 'ratelimit'

	s.grpcSrv, err = gubernator.New(gubernator.Config{
		Metrics:          metrics.NewStatsdMetrics(metrics.NewClientAdaptor(service.Metrics())),
		Picker:           gubernator.NewConsistantHash(nil),
		Cache:            cache.NewLRUCache(s.conf.LRUCache),
		PeerSyncer:       sync.NewEtcdSync("", service.Etcd()),
		AdvertiseAddress: s.conf.GRPCAdvertiseAddress,
		ListenAddress:    grpcAddress,
	})
	if err != nil {
		return errors.Wrap(err, "while initializing GRPC server")
	}

	// Register GRPC Gateway
	ctx, s.cancel = context.WithCancel(context.Background())

	mux := s.Mux()
	gateway := runtime.NewServeMux()
	err = gubernator.RegisterRateLimitServiceV1HandlerFromEndpoint(ctx, gateway,
		grpcAddress, []grpc.DialOption{grpc.WithInsecure()})
	if err != nil {
		return errors.Wrap(err, "while registering GRPC gateway handler")
	}

	// TODO: Add some metrics collecting middleware for gateway requests
	mux.Handle("/v1/{wildcard}", gateway)

	return s.grpcSrv.Start()
}

func (s *Service) Stop() error {
	s.cancel()
	s.grpcSrv.Stop()
	return nil
}

func main() {
	var svc Service
	service.Run(&svc.conf, &svc,
		service.WithName("gubernator"),
		service.WithInstanceID(os.Getenv("GUBER_ID")),
	)
}
