/*
Copyright 2018-2019 Mailgun Technologies Inc

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/signal"

	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/mailgun/gubernator"
	"github.com/mailgun/holster"
	"github.com/mailgun/holster/etcdutil"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

var log = logrus.WithField("category", "server")
var Version = "dev-build"

func main() {
	var wg holster.WaitGroup
	var conf ServerConfig
	var err error

	// Read our config from the environment or optional environment config file
	conf, err = confFromEnv()
	checkErr(err, "while getting config")

	// The LRU cache we store rate limits in
	cache := gubernator.NewLRUCache(conf.CacheSize)

	// cache also implements prometheus.Collector interface
	prometheus.MustRegister(cache)

	// Handler to collect duration and API access metrics for GRPC
	statsHandler := gubernator.NewGRPCStatsHandler()

	// New GRPC server
	grpcSrv := grpc.NewServer(
		grpc.StatsHandler(statsHandler),
		grpc.MaxRecvMsgSize(1024*1024))

	// Registers a new gubernator instance with the GRPC server
	guber, err := gubernator.New(gubernator.Config{
		GRPCServer: grpcSrv,
		Cache:      cache,
	})
	checkErr(err, "while creating new gubernator instance")

	// guber instance also implements prometheus.Collector interface
	prometheus.MustRegister(guber)

	// Start serving GRPC Requests
	wg.Go(func() {
		listener, err := net.Listen("tcp", conf.GRPCListenAddress)
		checkErr(err, "while starting GRPC listener")

		log.Infof("Gubernator Listening on %s ...", conf.GRPCListenAddress)
		checkErr(grpcSrv.Serve(listener), "while starting GRPC server")
	})

	var pool gubernator.PoolInterface

	if conf.K8PoolConf.Enabled {
		// Source our list of peers from kubernetes endpoint API
		conf.K8PoolConf.OnUpdate = guber.SetPeers
		pool, err = gubernator.NewK8sPool(conf.K8PoolConf)
		checkErr(err, "while querying kubernetes API")
	} else {
		// Register ourselves with other peers via ETCD
		etcdClient, err := etcdutil.NewClient(&conf.EtcdConf)
		checkErr(err, "while connecting to etcd")

		pool, err = gubernator.NewEtcdPool(gubernator.EtcdPoolConfig{
			AdvertiseAddress: conf.EtcdAdvertiseAddress,
			OnUpdate:         guber.SetPeers,
			Client:           etcdClient,
			BaseKey:          conf.EtcdKeyPrefix,
		})
		checkErr(err, "while registering with ETCD pool")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup an JSON Gateway API for our GRPC methods
	gateway := runtime.NewServeMux()
	err = gubernator.RegisterV1HandlerFromEndpoint(ctx, gateway,
		conf.EtcdAdvertiseAddress, []grpc.DialOption{grpc.WithInsecure()})
	checkErr(err, "while registering GRPC gateway handler")

	// Serve the JSON Gateway and metrics handlers via standard HTTP/1
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.Handle("/", gateway)
	httpSrv := &http.Server{Addr: conf.GRPCListenAddress, Handler: mux}

	wg.Go(func() {
		listener, err := net.Listen("tcp", conf.HTTPListenAddress)
		checkErr(err, "while starting HTTP listener")

		log.Infof("HTTP Gateway Listening on %s ...", conf.HTTPListenAddress)
		checkErr(httpSrv.Serve(listener), "while starting HTTP server")
	})

	// Wait here for signals to clean up our mess
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	for sig := range c {
		if sig == os.Interrupt {
			log.Info("caught interrupt; user requested premature exit")
			pool.Close()
			httpSrv.Shutdown(ctx)
			grpcSrv.GracefulStop()
			wg.Stop()
			statsHandler.Close()
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
