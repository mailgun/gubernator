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
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"time"

	etcd "github.com/coreos/etcd/clientv3"
	"github.com/davecgh/go-spew/spew"
	"github.com/mailgun/gubernator"
	"github.com/mailgun/holster/v3/setter"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"k8s.io/klog"
)

var debug = false

type ServerConfig struct {
	GRPCListenAddress    string
	EtcdAdvertiseAddress string
	HTTPListenAddress    string
	EtcdKeyPrefix        string
	CacheSize            int

	// Etcd configuration used to find peers
	EtcdConf etcd.Config

	// Configure how behaviours behave
	Behaviors gubernator.BehaviorConfig

	// K8s configuration used to find peers inside a K8s cluster
	K8PoolConf gubernator.K8sPoolConfig

	// The PeerPicker as selected by `GUBER_PEER_PICKER`
	Picker gubernator.PeerPicker
}

func confFromEnv() (ServerConfig, error) {
	var configFile string
	var conf ServerConfig

	flags := flag.NewFlagSet("gubernator", flag.ContinueOnError)
	flags.StringVar(&configFile, "config", "", "yaml config file")
	flags.BoolVar(&debug, "debug", false, "enable debug")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return conf, err
	}

	// in order to prevent logging to /tmp by k8s.io/client-go
	// and other kubernetes related dependencies which are using
	// klog (https://github.com/kubernetes/klog), we need to
	// initialize klog in the way it prints to stderr only.
	klog.InitFlags(nil)
	flag.Set("logtostderr", "true")

	if debug || os.Getenv("GUBER_DEBUG") != "" {
		logrus.SetLevel(logrus.DebugLevel)
		logrus.Debug("Debug enabled")
		debug = true
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

	// PeerPicker Config
	if pp := os.Getenv("GUBER_PEER_PICKER"); pp != "" {
		switch pp {
		case "consistent-hash":
			// Gubernator defaults to this picker if not defined
			conf.Picker = nil
		case "replicated-hash":
			conf.Picker = nil
			var replicas int
			setter.SetDefault(&replicas, getEnvInteger("GUBER_REPLICATED_HASH_REPLICAS"), 1)
			conf.Picker = gubernator.NewReplicatedConsistantHash(nil, replicas)
		}
	}

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

	if debug {
		spew.Dump(conf)
	}

	return conf, nil
}

func setupTLS(conf *etcd.Config) error {
	var tlsCertFile, tlsKeyFile, tlsCAFile string

	// set `GUBER_ETCD_TLS_ENABLE` and this line will
	// create a TLS config with no config.
	setter.SetDefault(&conf.TLS, &tls.Config{})

	setter.SetDefault(&tlsCertFile, os.Getenv("GUBER_ETCD_TLS_CERT"))
	setter.SetDefault(&tlsKeyFile, os.Getenv("GUBER_ETCD_TLS_KEY"))
	setter.SetDefault(&tlsCAFile, os.Getenv("GUBER_ETCD_TLS_CA"))

	// If the CA file was provided
	if tlsCAFile != "" {
		setter.SetDefault(&conf.TLS, &tls.Config{})

		var certPool *x509.CertPool = nil
		if pemBytes, err := ioutil.ReadFile(tlsCAFile); err == nil {
			certPool = x509.NewCertPool()
			certPool.AppendCertsFromPEM(pemBytes)
		} else {
			return errors.Wrapf(err, "while loading cert CA file '%s'", tlsCAFile)
		}
		setter.SetDefault(&conf.TLS.RootCAs, certPool)
		conf.TLS.InsecureSkipVerify = false
	}

	// If the cert and key files are provided attempt to load them
	if tlsCertFile != "" && tlsKeyFile != "" {
		tlsCert, err := tls.LoadX509KeyPair(tlsCertFile, tlsKeyFile)
		if err != nil {
			return errors.Wrapf(err, "while loading cert '%s' and key file '%s'",
				tlsCertFile, tlsKeyFile)
		}
		setter.SetDefault(&conf.TLS.Certificates, []tls.Certificate{tlsCert})
	}

	// If no other TLS config is provided this will force connecting with TLS,
	// without cert verification
	if os.Getenv("GUBER_ETCD_TLS_SKIP_VERIFY") != "" {
		setter.SetDefault(&conf.TLS, &tls.Config{})
		conf.TLS.InsecureSkipVerify = true
	}
	return nil
}

func anyHasPrefix(prefix string, items []string) bool {
	for _, i := range items {
		if strings.HasPrefix(i, prefix) {
			return true
		}
	}
	return false
}

func getEnvInteger(name string) int {
	v := os.Getenv(name)
	if v == "" {
		return 0
	}
	i, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		log.WithError(err).Errorf("while parsing '%s' as an integer", name)
		return 0
	}
	return int(i)
}

func getEnvDuration(name string) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		log.WithError(err).Errorf("while parsing '%s' as a duration", name)
		return 0
	}
	return d
}

func getEnvSlice(name string) []string {
	v := os.Getenv(name)
	if v == "" {
		return nil
	}
	return strings.Split(v, ",")
}

// Take values from a file in the format `GUBER_CONF_ITEM=my-value` and put them into the environment
// lines that begin with `#` are ignored
func fromEnvFile(configFile string) error {
	fd, err := os.Open(configFile)
	if err != nil {
		return fmt.Errorf("while opening config file: %s", err)
	}

	contents, err := ioutil.ReadAll(fd)
	if err != nil {
		return fmt.Errorf("while reading config file '%s': %s", configFile, err)
	}
	for i, line := range strings.Split(string(contents), "\n") {
		// Skip comments, empty lines or lines with tabs
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, " ") ||
			strings.HasPrefix(line, "\t") || len(line) == 0 {
			continue
		}

		logrus.Debugf("config: [%d] '%s'", i, line)
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return errors.Errorf("malformed key=value on line '%d'", i)
		}

		if err := os.Setenv(parts[0], parts[1]); err != nil {
			return errors.Wrapf(err, "while settings environ for '%s=%s'", parts[0], parts[1])
		}
	}
	return nil
}
