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
	"flag"
	"os"
	"os/signal"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/mailgun/gubernator"
	"github.com/mailgun/holster/v3/setter"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"k8s.io/klog"
)

var log = logrus.WithField("category", "server")
var Version = "dev-build"

func main() {
	var configFile string

	flags := flag.NewFlagSet("gubernator", flag.ContinueOnError)
	flags.StringVar(&configFile, "config", "", "yaml config file")
	flags.BoolVar(&gubernator.DebugEnabled, "debug", false, "enable debug")
	checkErr(flags.Parse(os.Args[1:]), "while parsing flags")

	// Read our config from the environment or optional environment config file
	conf, err := confFromFile(configFile)
	checkErr(err, "while getting config")

	// Start the server
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	srv, err := gubernator.SpawnDaemon(ctx, conf)
	checkErr(err, "while starting server")
	cancel()

	// Wait here for signals to clean up our mess
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	for sig := range c {
		if sig == os.Interrupt {
			log.Info("caught interrupt; user requested premature exit")
			srv.Close()
			os.Exit(0)
		}
	}
}

func confFromFile(configFile string) (gubernator.DaemonConfig, error) {
	var conf gubernator.DaemonConfig

	// in order to prevent logging to /tmp by k8s.io/client-go
	// and other kubernetes related dependencies which are using
	// klog (https://github.com/kubernetes/klog), we need to
	// initialize klog in the way it prints to stderr only.
	klog.InitFlags(nil)
	flag.Set("logtostderr", "true")

	if gubernator.DebugEnabled || os.Getenv("GUBER_DEBUG") != "" {
		logrus.SetLevel(logrus.DebugLevel)
		logrus.Debug("Debug enabled")
		gubernator.DebugEnabled = true
	}

	if configFile != "" {
		log.Infof("Loading env config: %s", configFile)
		if err := fromEnvFile(configFile); err != nil {
			return conf, err
		}
	}

	// Main config
	setter.SetDefault(&conf.GRPCListenAddress, os.Getenv("GUBER_GRPC_ADDRESS"), "0.0.0.0:81")
	setter.SetDefault(&conf.HTTPListenAddress, os.Getenv("GUBER_HTTP_ADDRESS"), "0.0.0.0:80")
	setter.SetDefault(&conf.CacheSize, getEnvInteger("GUBER_CACHE_SIZE"), 50000)

	// Behaviors
	setter.SetDefault(&conf.Behaviors.BatchTimeout, getEnvDuration("GUBER_BATCH_TIMEOUT"))
	setter.SetDefault(&conf.Behaviors.BatchLimit, getEnvInteger("GUBER_BATCH_LIMIT"))
	setter.SetDefault(&conf.Behaviors.BatchWait, getEnvDuration("GUBER_BATCH_WAIT"))

	setter.SetDefault(&conf.Behaviors.GlobalTimeout, getEnvDuration("GUBER_GLOBAL_TIMEOUT"))
	setter.SetDefault(&conf.Behaviors.GlobalBatchLimit, getEnvInteger("GUBER_GLOBAL_BATCH_LIMIT"))
	setter.SetDefault(&conf.Behaviors.GlobalSyncWait, getEnvDuration("GUBER_GLOBAL_SYNC_WAIT"))

	// ETCD Config
	setter.SetDefault(&conf.EtcdAdvertiseAddress, os.Getenv("GUBER_ETCD_ADVERTISE_ADDRESS"), "127.0.0.1:81")
	setter.SetDefault(&conf.EtcdKeyPrefix, os.Getenv("GUBER_ETCD_KEY_PREFIX"), "/gubernator-peers")
	setter.SetDefault(&conf.EtcdConf.Endpoints, getEnvSlice("GUBER_ETCD_ENDPOINTS"), []string{"localhost:2379"})
	setter.SetDefault(&conf.EtcdConf.DialTimeout, getEnvDuration("GUBER_ETCD_DIAL_TIMEOUT"), time.Second*5)
	setter.SetDefault(&conf.EtcdConf.Username, os.Getenv("GUBER_ETCD_USER"))
	setter.SetDefault(&conf.EtcdConf.Password, os.Getenv("GUBER_ETCD_PASSWORD"))

	// Kubernetes Config
	setter.SetDefault(&conf.K8PoolConf.Namespace, os.Getenv("GUBER_K8S_NAMESPACE"), "default")
	conf.K8PoolConf.PodIP = os.Getenv("GUBER_K8S_POD_IP")
	conf.K8PoolConf.PodPort = os.Getenv("GUBER_K8S_POD_PORT")
	conf.K8PoolConf.Selector = os.Getenv("GUBER_K8S_ENDPOINTS_SELECTOR")

	if anyHasPrefix("GUBER_K8S_", os.Environ()) {
		logrus.Debug("K8s peer pool config found")
		conf.K8PoolConf.Enabled = true
		if conf.K8PoolConf.Selector == "" {
			return conf, errors.New("when using k8s for peer discovery, you MUST provide a " +
				"`GUBER_K8S_ENDPOINTS_SELECTOR` to select the gubernator peers from the endpoints listing")
		}
	}

	if anyHasPrefix("GUBER_ETCD_", os.Environ()) {
		logrus.Debug("ETCD peer pool config found")
		if conf.K8PoolConf.Enabled {
			return conf, errors.New("refusing to register gubernator peers with both etcd and k8s;" +
				" remove either `GUBER_ETCD_*` or `GUBER_K8S_*` variables from the environment")
		}
	}

	// If env contains any TLS configuration
	if anyHasPrefix("GUBER_ETCD_TLS_", os.Environ()) {
		if err := setupTLS(&conf.EtcdConf); err != nil {
			return conf, err
		}
	}

	if gubernator.DebugEnabled {
		spew.Dump(conf)
	}

	return conf, nil
}

func checkErr(err error, msg string) {
	if err != nil {
		log.WithError(err).Error(msg)
		os.Exit(1)
	}
}
