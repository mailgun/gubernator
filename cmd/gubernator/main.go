package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/signal"

	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/mailgun/gubernator"
	"github.com/mailgun/gubernator/cache"
	"github.com/mailgun/holster"
	"github.com/mailgun/holster/etcdutil"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

var log = logrus.WithField("category", "server")
var Version = "dev-build"

func main() {
	var metrics gubernator.MetricsCollector
	var opts []grpc.ServerOption
	var wg holster.WaitGroup
	var conf ServerConfig
	var err error

	// Read our config from the environment or optional environment config file
	conf, err = confFromEnv()
	checkErr(err, "while getting config")

	opts = []grpc.ServerOption{
		grpc.MaxRecvMsgSize(1024 * 1024),
	}

	cache := cache.NewLRUCache(conf.CacheSize)

	grpcSrv := grpc.NewServer(opts...)

	guber, err := gubernator.New(gubernator.Config{
		GRPCServer: grpcSrv,
		Cache:      cache,
	})

	/*sClient := newStatsdClient(conf)
	if sClient != nil {
		metrics, err = gubernator.NewStatsdMetrics(sClient)
		checkErr(err, "while starting statsd metrics")
		opts = append(opts, grpc.StatsHandler(metrics.GRPCStatsHandler()))
		metrics.RegisterCacheStats(cache)
		metrics.RegisterServerStats(guber)
	}*/

	checkErr(err, "while creating new gubernator instance")

	wg.Go(func() {
		listener, err := net.Listen("tcp", conf.GRPCListenAddress)
		checkErr(err, "while starting GRPC listener")

		log.Infof("Gubernator Listening on %s ...", conf.GRPCListenAddress)
		checkErr(grpcSrv.Serve(listener), "while starting GRPC server")
	})

	etcdClient, err := etcdutil.NewClient(&conf.EtcdConf)
	checkErr(err, "while connecting to etcd")

	pool, err := gubernator.NewEtcdPool(gubernator.EtcdPoolConfig{
		AdvertiseAddress: conf.AdvertiseAddress,
		OnUpdate:         guber.SetPeers,
		Client:           etcdClient,
		BaseKey:          "/foo",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gateway := runtime.NewServeMux()
	err = gubernator.RegisterV1HandlerFromEndpoint(ctx, gateway,
		conf.AdvertiseAddress, []grpc.DialOption{grpc.WithInsecure()})
	checkErr(err, "while registering GRPC gateway handler")

	mux := http.NewServeMux()
	mux.Handle("/", gateway)
	httpSrv := &http.Server{Addr: conf.GRPCListenAddress, Handler: mux}

	wg.Go(func() {
		listener, err := net.Listen("tcp", conf.HTTPListenAddress)
		checkErr(err, "while starting HTTP listener")

		log.Infof("HTTP Gateway Listening on %s ...", conf.HTTPListenAddress)
		checkErr(httpSrv.Serve(listener), "while starting HTTP server")
	})

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	for sig := range c {
		if sig == os.Interrupt {
			log.Info("caught interrupt; user requested premature exit")
			pool.Close()
			httpSrv.Shutdown(ctx)
			grpcSrv.GracefulStop()
			wg.Stop()
			metrics.Close()
			os.Exit(0)
		}
	}
}

func checkErr(err error, msg string) {
	if err != nil {
		log.WithError(err).Error(msg)
		os.Exit(1)
	}
}
