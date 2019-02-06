package main

import (
	"fmt"
	"github.com/smira/go-statsd"
	"os"
	"os/signal"

	etcd "github.com/coreos/etcd/clientv3"
	"github.com/mailgun/gubernator"
	"github.com/mailgun/gubernator/cache"
	"github.com/mailgun/gubernator/metrics"
	"github.com/mailgun/gubernator/sync"
	"github.com/sirupsen/logrus"
	"github.com/x-cray/logrus-prefixed-formatter"
)

type Config struct {
	LRUCache cache.LRUCacheConfig
	Metrics  metrics.StatsdConfig
	Etcd     etcd.Config
	Debug    bool
}

func runServer(conf Config) (*gubernator.Server, error) {
	statsClient := statsd.NewClient(conf.Metrics.Endpoint,
		statsd.FlushInterval(conf.Metrics.Interval),
		statsd.MetricPrefix(conf.Metrics.Prefix),
		statsd.Logger(logrus.StandardLogger()))

	etcdClient, err := etcd.New(conf.Etcd)
	if err != nil {

	}

	return gubernator.NewServer(gubernator.ServerConfig{
		Metrics:    metrics.NewStatsdMetrics(&metrics.Adaptor{Client: statsClient}),
		Picker:     gubernator.NewConsistantHash(nil),
		Cache:      cache.NewLRUCache(conf.LRUCache),
		PeerSyncer: sync.NewEtcdSync(etcdClient),
	})

}

func main() {
	var conf Config

	conf.Debug = true

	formatter := new(prefixed.TextFormatter)
	formatter.FullTimestamp = true
	logrus.SetFormatter(formatter)
	if conf.Debug {
		logrus.SetLevel(logrus.DebugLevel)
	}

	// Run a local cluster of 5 servers
	var servers []*gubernator.Server
	for i := 0; i < 6; i++ {
		// TODO: Change address in config
		server, err := runServer(conf)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			os.Exit(1)
		}
		if err := server.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			os.Exit(1)
		}
		servers = append(servers, server)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	for sig := range c {
		if sig == os.Interrupt {
			fmt.Printf("caught interupt; user requested premature exit\n")
			for _, server := range servers {
				server.Stop()
			}
			os.Exit(0)
		}
	}
}
