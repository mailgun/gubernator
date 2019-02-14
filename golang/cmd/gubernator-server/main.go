package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"time"

	etcd "github.com/coreos/etcd/clientv3"
	"github.com/ghodss/yaml"
	"github.com/mailgun/gubernator/golang"
	"github.com/mailgun/gubernator/golang/cache"
	"github.com/mailgun/gubernator/golang/logging"
	"github.com/mailgun/gubernator/golang/metrics"
	"github.com/mailgun/gubernator/golang/sync"
	"github.com/mailgun/holster"
	"github.com/mailgun/holster/etcdutil"
	"github.com/sirupsen/logrus"
)

var log = logrus.WithField("category", "server")
var Version = "dev-build"

type Config struct {
	gubernator.ServerConfig

	LRUCache cache.LRUCacheConfig `json:"lru-cache"`
	Statsd   metrics.Config       `json:"statsd"`
	Logging  logging.Config       `json:"logging"`
	EtcdConf etcd.Config
}

func main() {
	var configFile string
	var conf Config

	flags := flag.NewFlagSet("gubernator-server", flag.ContinueOnError)
	flags.StringVar(&configFile, "config", "", "yaml config file")
	checkErr(flags.Parse(os.Args[1:]), "while parsing cli flags")

	if configFile != "" {
		log.Infof("Loading config: %s", configFile)
		checkErr(loadConfig(configFile, &conf), "while loading config")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	checkErr(logging.Init(ctx, conf.Logging), "while initializing logging")

	holster.SetDefault(&conf.HTTPListenAddress, "127.0.0.1:9090")
	holster.SetDefault(&conf.GRPCListenAddress, "127.0.0.1:9091")

	etcdClient, err := etcdutil.NewClient(&conf.EtcdConf)
	checkErr(err, "while connecting to etcd")

	grpcSrv, err := gubernator.NewGRPCServer(gubernator.ServerConfig{
		Metrics:              metrics.NewStatsdMetricsFromConf(conf.Statsd),
		Picker:               gubernator.NewConsistantHash(nil),
		Cache:                cache.NewLRUCache(conf.LRUCache),
		PeerSyncer:           sync.NewEtcdSync(etcdClient),
		GRPCAdvertiseAddress: conf.GRPCAdvertiseAddress,
		GRPCListenAddress:    conf.GRPCListenAddress,
	})
	checkErr(err, "while initializing GRPC server")

	checkErr(grpcSrv.Start(), "while starting GRPC server")

	httpSrv, err := gubernator.NewHTTPServer(gubernator.ServerConfig{
		HTTPListenAddress: conf.HTTPListenAddress,
		GRPCListenAddress: conf.GRPCListenAddress,
	})
	checkErr(err, "while initializing HTTP server")

	checkErr(httpSrv.Start(), "while starting HTTP server")

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	for sig := range c {
		if sig == os.Interrupt {
			log.Info("caught interrupt; user requested premature exit")
			httpSrv.Stop()
			grpcSrv.Stop()
			os.Exit(0)
		}
	}
}

func loadConfig(confFile string, conf *Config) error {
	fd, err := os.Open(confFile)
	if err != nil {
		return fmt.Errorf("while opening config file: %s", err)
	}

	content, err := ioutil.ReadAll(fd)
	if err != nil {
		return fmt.Errorf("while reading config file '%s': %s", confFile, err)
	}

	if err := yaml.Unmarshal(content, &conf); err != nil {
		return fmt.Errorf("while marshalling config file '%s': %s", confFile, err)
	}
	return nil
}

func checkErr(err error, msg string) {
	if err != nil {
		log.WithError(err).Error(msg)
		os.Exit(1)
	}
}
