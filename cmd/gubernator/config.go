package main

import (
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"time"

	etcd "github.com/coreos/etcd/clientv3"
	"github.com/mailgun/gubernator"
	"github.com/mailgun/holster"
)

var debug = false

type ServerConfig struct {
	GRPCListenAddress string
	AdvertiseAddress  string
	HTTPListenAddress string
	EtcdKeyPrefix     string
	CacheSize         int

	// Etcd configuration used to find peers
	EtcdConf etcd.Config

	// Configure how behaviours behave
	Behaviors gubernator.BehaviorConfig
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

	if debug {
		logrus.SetLevel(logrus.DebugLevel)
	}

	if configFile != "" {
		log.Infof("Loading env config: %s", configFile)
		if err := fromEnvFile(configFile); err != nil {
			return conf, err
		}
	}

	// Main config
	holster.SetDefault(&conf.GRPCListenAddress, os.Getenv("GUBER_GRPC_ADDRESS"), "0.0.0.0:81")
	holster.SetDefault(&conf.HTTPListenAddress, os.Getenv("GUBER_HTTP_ADDRESS"), "0.0.0.0:80")
	holster.SetDefault(&conf.AdvertiseAddress, os.Getenv("GUBER_ADVERTISE_ADDRESS"), "127.0.0.1:81")
	holster.SetDefault(&conf.CacheSize, getEnvInteger("GUBER_CACHE_SIZE"), 50000)
	holster.SetDefault(&conf.EtcdKeyPrefix, os.Getenv("GUBER_ETCD_KEY_PREFIX"), "/gubernator-peers")

	// Behaviors
	holster.SetDefault(&conf.Behaviors.BatchTimeout, getEnvDuration("GUBER_BATCH_TIMEOUT"))
	holster.SetDefault(&conf.Behaviors.BatchLimit, getEnvInteger("GUBER_BATCH_LIMIT"))
	holster.SetDefault(&conf.Behaviors.BatchWait, getEnvDuration("GUBER_BATCH_WAIT"))

	holster.SetDefault(&conf.Behaviors.GlobalTimeout, getEnvDuration("GUBER_GLOBAL_TIMEOUT"))
	holster.SetDefault(&conf.Behaviors.GlobalBatchLimit, getEnvInteger("GUBER_GLOBAL_BATCH_LIMIT"))
	holster.SetDefault(&conf.Behaviors.GlobalSyncWait, getEnvDuration("GUBER_GLOBAL_SYNC_WAIT"))

	// ETCD Config
	holster.SetDefault(&conf.EtcdConf.Endpoints, getEnvSlice("GUBER_ETCD_ENDPOINTS"), []string{"localhost:2379"})
	holster.SetDefault(&conf.EtcdConf.DialTimeout, getEnvDuration("GUBER_ETCD_DIAL_TIMEOUT"), time.Second*5)
	holster.SetDefault(&conf.EtcdConf.Username, os.Getenv("GUBER_ETCD_USER"))
	holster.SetDefault(&conf.EtcdConf.Password, os.Getenv("GUBER_ETCD_PASSWORD"))

	// If env contains any TLS configuration
	if anyHasPrefix("GUBER_ETCD_TLS_", os.Environ()) {
		if err := setupTLS(&conf.EtcdConf); err != nil {
			return conf, err
		}
	}
	return conf, nil
}

func setupTLS(conf *etcd.Config) error {
	var tlsCertFile, tlsKeyFile, tlsCAFile string

	// set `GUBER_ETCD_TLS_ENABLE` and this line will
	// create a TLS config with no config.
	holster.SetDefault(&conf.TLS, &tls.Config{})

	holster.SetDefault(&tlsCertFile, os.Getenv("GUBER_ETCD_TLS_CERT"))
	holster.SetDefault(&tlsKeyFile, os.Getenv("GUBER_ETCD_TLS_KEY"))
	holster.SetDefault(&tlsCAFile, os.Getenv("GUBER_ETCD_TLS_CA"))

	// If the CA file was provided
	if tlsCAFile != "" {
		holster.SetDefault(&conf.TLS, &tls.Config{})

		var certPool *x509.CertPool = nil
		if pemBytes, err := ioutil.ReadFile(tlsCAFile); err == nil {
			certPool = x509.NewCertPool()
			certPool.AppendCertsFromPEM(pemBytes)
		} else {
			return errors.Wrapf(err, "while loading cert CA file '%s'", tlsCAFile)
		}
		holster.SetDefault(&conf.TLS.RootCAs, certPool)
		conf.TLS.InsecureSkipVerify = false
	}

	// If the cert and key files are provided attempt to load them
	if tlsCertFile != "" && tlsKeyFile != "" {
		tlsCert, err := tls.LoadX509KeyPair(tlsCertFile, tlsKeyFile)
		if err != nil {
			return errors.Wrapf(err, "while loading cert '%s' and key file '%s'",
				tlsCertFile, tlsKeyFile)
		}
		holster.SetDefault(&conf.TLS.Certificates, []tls.Certificate{tlsCert})
	}

	// If no other TLS config is provided this will force connecting with TLS,
	// without cert verification
	if os.Getenv("GUBER_ETCD_TLS_SKIP_VERIFY") != "" {
		holster.SetDefault(&conf.TLS, &tls.Config{})
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
		parts := strings.Split(line, "=")
		if len(parts) != 2 {
			return errors.Errorf("malformed key=value on line '%d'", i)
		}

		if err := os.Setenv(parts[0], parts[1]); err != nil {
			return errors.Wrapf(err, "while settings environ for '%s=%s'", parts[0], parts[1])
		}
	}
	return nil
}
