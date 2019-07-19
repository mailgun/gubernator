package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	etcd "github.com/coreos/etcd/clientv3"
	"github.com/ghodss/yaml"
	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/mailgun/gubernator"
	"github.com/mailgun/gubernator/cache"
	"github.com/mailgun/holster"
	"github.com/mailgun/holster/clock"
	"github.com/mailgun/holster/etcdutil"
	"github.com/sirupsen/logrus"
	"github.com/smira/go-statsd"
	"google.golang.org/grpc"
)

var log = logrus.WithField("category", "server")
var Version = "dev-build"

type ServerConfig struct {
	GRPCListenAddress string `json:"grpc-listen-address"`
	AdvertiseAddress  string `json:"advertise-address"`
	HTTPListenAddress string `json:"http-listen-address"`
	CacheSize         int    `json:"cache-size"`

	// Statsd metric configuration
	StatsdConfig struct {
		Period   clock.DurationJSON `json:"period"`
		Endpoint string             `json:"endpoint"`
		Prefix   string             `json:"prefix"`
	} `json:"statsd"`

	// Etcd configuration used to find peers
	EtcdConf etcd.Config `json:"etcd-config"`
}

func main() {
	var metrics gubernator.MetricsCollector
	var opts []grpc.ServerOption
	var wg holster.WaitGroup
	var conf ServerConfig
	var err error

	conf, err = loadConfig()
	checkErr(err, "while getting config")

	holster.SetDefault(&conf.GRPCListenAddress, "0.0.0.0:81")
	holster.SetDefault(&conf.AdvertiseAddress, "127.0.0.1:81")
	opts = []grpc.ServerOption{
		grpc.MaxRecvMsgSize(1024 * 1024),
	}

	cache := cache.NewLRUCache(conf.CacheSize)

	grpcSrv := grpc.NewServer(opts...)

	guber, err := gubernator.New(gubernator.Config{
		GRPCServer: grpcSrv,
		Cache:      cache,
	})

	sClient := newStatsdClient(conf)
	if sClient != nil {
		metrics, err = gubernator.NewStatsdMetrics(sClient)
		checkErr(err, "while starting statsd metrics")
		opts = append(opts, grpc.StatsHandler(metrics.GRPCStatsHandler()))
		metrics.RegisterCacheStats(cache)
		metrics.RegisterServerStats(guber)
	}

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

func newStatsdClient(conf ServerConfig) *statsd.Client {
	if conf.StatsdConfig.Endpoint == "" {
		log.Info("Metrics config missing; metrics disabled")
		return nil
	}

	if conf.StatsdConfig.Prefix == "" {
		hostname, _ := os.Hostname()
		normalizedHostname := strings.Replace(hostname, ".", "_", -1)
		conf.StatsdConfig.Prefix = fmt.Sprintf("gubernator.%v.", normalizedHostname)
	}

	holster.SetDefault(&conf.StatsdConfig.Period.Duration, time.Second)

	return statsd.NewClient(conf.StatsdConfig.Endpoint,
		statsd.FlushInterval(conf.StatsdConfig.Period.Duration),
		statsd.MetricPrefix(conf.StatsdConfig.Prefix),
		statsd.Logger(log))
}

func loadConfig() (ServerConfig, error) {
	var configFile string
	var conf ServerConfig

	flags := flag.NewFlagSet("gubernator", flag.ContinueOnError)
	flags.StringVar(&configFile, "config", "", "yaml config file")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return conf, err
	}

	if configFile == "" {
		return conf, errors.New("config file is required; please provide a --config flag")
	}
	log.Infof("Loading config: %s", configFile)

	fd, err := os.Open(configFile)
	if err != nil {
		return conf, fmt.Errorf("while opening config file: %s", err)
	}

	content, err := ioutil.ReadAll(fd)
	if err != nil {
		return conf, fmt.Errorf("while reading config file '%s': %s", configFile, err)
	}

	if err := yaml.Unmarshal(content, &conf); err != nil {
		return conf, fmt.Errorf("while marshalling config file '%s': %s", configFile, err)
	}
	return conf, nil
}

func checkErr(err error, msg string) {
	if err != nil {
		log.WithError(err).Error(msg)
		os.Exit(1)
	}
}
