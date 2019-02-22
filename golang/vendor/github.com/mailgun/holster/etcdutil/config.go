package etcdutil

import (
	"crypto/tls"
	"crypto/x509"
	"io/ioutil"
	"os"
	"strings"
	"time"

	etcd "github.com/coreos/etcd/clientv3"
	"github.com/mailgun/holster"
	"github.com/pkg/errors"
	"google.golang.org/grpc/grpclog"
)

const (
	localEtcdEndpoint = "127.0.0.1:2379"
)

func init() {
	// We check this here to avoid data race with GRPC go routines writing to the logger
	if os.Getenv("ETCD3_DEBUG") != "" {
		etcd.SetLogger(grpclog.NewLoggerV2WithVerbosity(os.Stderr, os.Stderr, os.Stderr, 4))
	}
}

// NewClient creates a new etcd.Client with the specified config where blanks
// are filled from environment variables by NewConfig.
//
// If the provided config is nil and no environment variables are set, it will
// return a client connecting without TLS via localhost:2379.
func NewClient(cfg *etcd.Config) (*etcd.Client, error) {
	var err error
	if cfg, err = NewConfig(cfg); err != nil {
		return nil, errors.Wrap(err, "failed to build etcd config")
	}

	etcdClt, err := etcd.New(*cfg)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create etcd client")
	}
	return etcdClt, nil
}

// NewConfig creates a new etcd.Config using environment variables. If an
// existing config is passed, it will fill in missing configuration using
// environment variables or defaults if they exists on the local system.
//
// If no environment variables are set, it will return a config set to
// connect without TLS via localhost:2379.
func NewConfig(cfg *etcd.Config) (*etcd.Config, error) {
	var envEndpoint, tlsCertFile, tlsKeyFile, tlsCAFile string

	holster.SetDefault(&cfg, &etcd.Config{})
	holster.SetDefault(&cfg.Username, os.Getenv("ETCD3_USER"))
	holster.SetDefault(&cfg.Password, os.Getenv("ETCD3_PASSWORD"))
	holster.SetDefault(&tlsCertFile, os.Getenv("ETCD3_TLS_CERT"))
	holster.SetDefault(&tlsKeyFile, os.Getenv("ETCD3_TLS_KEY"))
	holster.SetDefault(&tlsCAFile, os.Getenv("ETCD3_CA"))

	// Default to 5 second timeout, else connections hang indefinitely
	holster.SetDefault(&cfg.DialTimeout, time.Second*5)
	// Or if the user provided a timeout
	if timeout := os.Getenv("ETCD3_DIAL_TIMEOUT"); timeout != "" {
		duration, err := time.ParseDuration(timeout)
		if err != nil {
			return nil, errors.Errorf(
				"ETCD3_DIAL_TIMEOUT='%s' is not a duration (1m|15s|24h): %s", timeout, err)
		}
		cfg.DialTimeout = duration
	}

	// If the CA file was provided
	if tlsCAFile != "" {
		holster.SetDefault(&cfg.TLS, &tls.Config{})

		var certPool *x509.CertPool = nil
		if pemBytes, err := ioutil.ReadFile(tlsCAFile); err == nil {
			certPool = x509.NewCertPool()
			certPool.AppendCertsFromPEM(pemBytes)
		} else {
			return nil, errors.Errorf("while loading cert CA file '%s': %s", tlsCAFile, err)
		}
		holster.SetDefault(&cfg.TLS.RootCAs, certPool)
		cfg.TLS.InsecureSkipVerify = false
	}

	// If the cert and key files are provided attempt to load them
	if tlsCertFile != "" && tlsKeyFile != "" {
		holster.SetDefault(&cfg.TLS, &tls.Config{})
		tlsCert, err := tls.LoadX509KeyPair(tlsCertFile, tlsKeyFile)
		if err != nil {
			return nil, errors.Errorf("while loading cert '%s' and key file '%s': %s",
				tlsCertFile, tlsKeyFile, err)
		}
		holster.SetDefault(&cfg.TLS.Certificates, []tls.Certificate{tlsCert})
	}

	holster.SetDefault(&envEndpoint, os.Getenv("ETCD3_ENDPOINT"), localEtcdEndpoint)
	holster.SetDefault(&cfg.Endpoints, strings.Split(envEndpoint, ","))

	// If no other TLS config is provided this will force connecting with TLS,
	// without cert verification
	if os.Getenv("ETCD3_SKIP_VERIFY") != "" {
		holster.SetDefault(&cfg.TLS, &tls.Config{})
		cfg.TLS.InsecureSkipVerify = true
	}

	// Enable TLS with no additional configuration
	if os.Getenv("ETCD3_ENABLE_TLS") != "" {
		holster.SetDefault(&cfg.TLS, &tls.Config{})
	}

	return cfg, nil
}
