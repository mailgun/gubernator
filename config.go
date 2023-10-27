/*
Copyright 2018-2022 Mailgun Technologies Inc

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

package gubernator

import (
	"bufio"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/mailgun/holster/v4/clock"
	"github.com/mailgun/holster/v4/setter"
	"github.com/mailgun/holster/v4/slice"
	"github.com/mailgun/holster/v4/tracing"
	"github.com/pkg/errors"
	"github.com/segmentio/fasthash/fnv1"
	"github.com/segmentio/fasthash/fnv1a"
	"github.com/sirupsen/logrus"
	etcd "go.etcd.io/etcd/client/v3"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
)

// BehaviorConfig controls the handling of rate limits in the cluster
type BehaviorConfig struct {
	// How long we should wait for a batched response from a peer
	BatchTimeout time.Duration
	// How long we should wait before sending a batched request
	BatchWait time.Duration
	// The max number of requests we can batch into a single peer request
	BatchLimit int
	// DisableBatching disables batching behavior for all ratelimits.
	DisableBatching bool

	// How long a non-owning peer should wait before syncing hits to the owning peer
	GlobalSyncWait time.Duration
	// How long we should wait for global sync responses from peers
	GlobalTimeout time.Duration
	// The max number of global updates we can batch into a single peer request
	GlobalBatchLimit int
	// ForceGlobal forces global mode on all rate limit checks.
	ForceGlobal bool

	// Number of concurrent requests that will be made to peers. Defaults to 100
	PeerRequestsConcurrency int
}

// Config for a gubernator instance
type Config struct {
	// (Required) A list of GRPC servers to register our instance with
	GRPCServers []*grpc.Server

	// (Optional) Adjust how gubernator behaviors are configured
	Behaviors BehaviorConfig

	// (Optional) The cache implementation
	CacheFactory func(maxSize int) Cache

	// (Optional) A persistent store implementation. Allows the implementor the ability to store the rate limits this
	// instance of gubernator owns. It's up to the implementor to decide what rate limits to persist.
	// For instance an implementor might only persist rate limits that have an expiration of
	// longer than 1 hour.
	Store Store

	// (Optional) A loader from a persistent store. Allows the implementor the ability to load and save
	// the contents of the cache when the gubernator instance is started and stopped
	Loader Loader

	// (Optional) This is the peer picker algorithm the server will use decide which peer in the local cluster
	// will own the rate limit
	LocalPicker PeerPicker

	// (Optional) This is the peer picker algorithm the server will use when deciding which remote peer to forward
	// rate limits too when a `Config.DataCenter` is set to something other than empty string.
	RegionPicker RegionPeerPicker

	// (Optional) This is the name of our local data center. This value will be used by LocalPicker when
	// deciding who we should immediately connect too for our local picker. Should remain empty if not
	// using multi data center support.
	DataCenter string

	// (Optional) A Logger which implements the declared logger interface (typically *logrus.Entry)
	Logger FieldLogger

	// (Optional) The TLS config used when connecting to gubernator peers
	PeerTLS *tls.Config

	// (Optional) If true, will emit traces for GRPC client requests to other peers
	PeerTraceGRPC bool

	// (Optional) The number of go routine workers used to process concurrent rate limit requests
	// Default is set to number of CPUs.
	Workers int

	// (Optional) The total size of the cache used to store rate limits. Defaults to 50,000
	CacheSize int
}

func (c *Config) SetDefaults() error {
	setter.SetDefault(&c.Behaviors.BatchTimeout, time.Millisecond*500)
	setter.SetDefault(&c.Behaviors.BatchLimit, maxBatchSize)
	setter.SetDefault(&c.Behaviors.BatchWait, time.Microsecond*500)

	setter.SetDefault(&c.Behaviors.GlobalTimeout, time.Millisecond*500)
	setter.SetDefault(&c.Behaviors.GlobalBatchLimit, maxBatchSize)
	setter.SetDefault(&c.Behaviors.GlobalSyncWait, time.Millisecond*100)

	setter.SetDefault(&c.Behaviors.PeerRequestsConcurrency, 100)

	setter.SetDefault(&c.LocalPicker, NewReplicatedConsistentHash(nil, defaultReplicas))
	setter.SetDefault(&c.RegionPicker, NewRegionPicker(nil))

	setter.SetDefault(&c.CacheSize, 50_000)
	setter.SetDefault(&c.Workers, runtime.NumCPU())
	setter.SetDefault(&c.Logger, logrus.New().WithField("category", "gubernator"))

	if c.CacheFactory == nil {
		c.CacheFactory = func(maxSize int) Cache {
			return NewLRUCache(maxSize)
		}
	}

	if c.Behaviors.BatchLimit > maxBatchSize {
		return fmt.Errorf("Behaviors.BatchLimit cannot exceed '%d'", maxBatchSize)
	}

	// Make a copy of the TLS config in case our caller decides to make changes
	if c.PeerTLS != nil {
		c.PeerTLS = c.PeerTLS.Clone()
	}

	return nil
}

type PeerInfo struct {
	// (Optional) The name of the data center this peer is in. Leave blank if not using multi data center support.
	DataCenter string `json:"data-center"`
	// (Optional) The http address:port of the peer
	HTTPAddress string `json:"http-address"`
	// (Required) The grpc address:port of the peer
	GRPCAddress string `json:"grpc-address"`
	// (Optional) Is true if PeerInfo is for this instance of gubernator
	IsOwner bool `json:"is-owner,omitempty"`
}

// HashKey returns the hash key used to identify this peer in the Picker.
func (p PeerInfo) HashKey() string {
	return p.GRPCAddress
}

type UpdateFunc func([]PeerInfo)

var DebugEnabled = false

type DaemonConfig struct {
	// (Required) The `address:port` that will accept GRPC requests
	GRPCListenAddress string

	// (Required) The `address:port` that will accept HTTP requests
	HTTPListenAddress string

	// (Optional) The `address:port` that will accept HTTP requests for /v1/HealthCheck
	// without verifying client certificates. Only starts listener when TLS config is provided.
	// TLS config is identical to what is applied on HTTPListenAddress, except that server
	// Does not attempt to verify client certificate. Useful when your health probes cannot
	// provide client certificate but you want to enforce mTLS in other RPCs (like in K8s)
	HTTPStatusListenAddress string

	// (Optional) Defines the max age connection from client in seconds.
	// Default is infinity
	GRPCMaxConnectionAgeSeconds int

	// (Optional) The `address:port` that is advertised to other Gubernator peers.
	// Defaults to `GRPCListenAddress`
	AdvertiseAddress string

	// (Optional) The number of items in the cache. Defaults to 50,000
	CacheSize int

	// (Optional) The number of go routine workers used to process concurrent rate limit requests
	// Defaults to the number of CPUs returned by runtime.NumCPU()
	Workers int

	// (Optional) Configure how behaviours behave
	Behaviors BehaviorConfig

	// (Optional) Identifies the datacenter this instance is running in. For
	// use with multi-region support
	DataCenter string

	// (Optional) Which pool to use when discovering other Gubernator peers
	//  Valid options are [etcd, k8s, member-list] (Defaults to 'member-list')
	PeerDiscoveryType string

	// (Optional) Etcd configuration used for peer discovery
	EtcdPoolConf EtcdPoolConfig

	// (Optional) K8s configuration used for peer discovery
	K8PoolConf K8sPoolConfig

	// (Optional) DNS Configuration used for peer discovery
	DNSPoolConf DNSPoolConfig

	// (Optional) Member list configuration used for peer discovery
	MemberListPoolConf MemberListPoolConfig

	// (Optional) The PeerPicker as selected by `GUBER_PEER_PICKER`
	Picker PeerPicker

	// (Optional) A Logger which implements the declared logger interface (typically *logrus.Entry)
	Logger FieldLogger

	// (Optional) TLS Configuration; SpawnDaemon() will modify the passed TLS config in an
	// attempt to build a complete TLS config if one is not provided.
	TLS *TLSConfig

	// (Optional) Metrics Flags which enable or disable a collection of some metric types
	MetricFlags MetricFlags

	// (Optional) Instance ID which is a unique id that identifies this instance of gubernator
	InstanceID string

	// (Optional) TraceLevel sets the tracing level, this controls the number of spans included in a single trace.
	//  Valid options are (tracing.InfoLevel, tracing.DebugLevel) Defaults to tracing.InfoLevel
	TraceLevel tracing.Level
}

func (d *DaemonConfig) ClientTLS() *tls.Config {
	if d.TLS != nil {
		return d.TLS.ClientTLS
	}
	return nil
}

func (d *DaemonConfig) ServerTLS() *tls.Config {
	if d.TLS != nil {
		return d.TLS.ServerTLS
	}
	return nil
}

// SetupDaemonConfig returns a DaemonConfig object as configured by reading the provided config file
// and environment.
func SetupDaemonConfig(logger *logrus.Logger, configFile string) (DaemonConfig, error) {
	log := logrus.NewEntry(logger)
	var conf DaemonConfig
	var logLevel string
	var logFormat string
	var advAddr, advPort string
	var err error

	if configFile != "" {
		log.Infof("Loading env config: %s", configFile)
		if err := fromEnvFile(log, configFile); err != nil {
			return conf, err
		}
	}

	// Log config
	setter.SetDefault(&logFormat, os.Getenv("GUBER_LOG_FORMAT"))
	if logFormat != "" {
		switch logFormat {
		case "json":
			logger.SetFormatter(&logrus.JSONFormatter{})
		case "text":
			logger.SetFormatter(&logrus.TextFormatter{})
		default:
			return conf, errors.New("GUBER_LOG_FORMAT is invalid; expected value is either json or text")
		}
	}

	setter.SetDefault(&DebugEnabled, getEnvBool(log, "GUBER_DEBUG"))
	setter.SetDefault(&logLevel, os.Getenv("GUBER_LOG_LEVEL"))
	if DebugEnabled {
		logger.SetLevel(logrus.DebugLevel)
		log.Debug("Debug enabled")
	} else if logLevel != "" {
		logrusLogLevel, err := logrus.ParseLevel(logLevel)
		if err != nil {
			return conf, errors.Wrap(err, "invalid log level")
		}

		logger.SetLevel(logrusLogLevel)
	}

	// Main config
	setter.SetDefault(&conf.GRPCListenAddress, os.Getenv("GUBER_GRPC_ADDRESS"),
		fmt.Sprintf("%s:81", LocalHost()))
	setter.SetDefault(&conf.HTTPListenAddress, os.Getenv("GUBER_HTTP_ADDRESS"),
		fmt.Sprintf("%s:80", LocalHost()))
	setter.SetDefault(&conf.InstanceID, GetInstanceID())
	setter.SetDefault(&conf.HTTPStatusListenAddress, os.Getenv("GUBER_STATUS_HTTP_ADDRESS"), "")
	setter.SetDefault(&conf.GRPCMaxConnectionAgeSeconds, getEnvInteger(log, "GUBER_GRPC_MAX_CONN_AGE_SEC"), 0)
	setter.SetDefault(&conf.CacheSize, getEnvInteger(log, "GUBER_CACHE_SIZE"), 50_000)
	setter.SetDefault(&conf.Workers, getEnvInteger(log, "GUBER_WORKER_COUNT"), 0)
	setter.SetDefault(&conf.AdvertiseAddress, os.Getenv("GUBER_ADVERTISE_ADDRESS"), conf.GRPCListenAddress)
	setter.SetDefault(&conf.DataCenter, os.Getenv("GUBER_DATA_CENTER"), "")
	setter.SetDefault(&conf.MetricFlags, getEnvMetricFlags(log, "GUBER_METRIC_FLAGS"))

	choices := []string{"member-list", "k8s", "etcd", "dns"}
	setter.SetDefault(&conf.PeerDiscoveryType, os.Getenv("GUBER_PEER_DISCOVERY_TYPE"), "member-list")
	if !slice.ContainsString(conf.PeerDiscoveryType, choices, nil) {
		return conf, fmt.Errorf("GUBER_PEER_DISCOVERY_TYPE is invalid; choices are [%s]`", strings.Join(choices, ","))
	}

	// AdvertiseAddress is not used in k8s discovery method. Skip processing and auto-discovery
	if conf.PeerDiscoveryType != "k8s" {
		advAddr, advPort, err = net.SplitHostPort(conf.AdvertiseAddress)
		if err != nil {
			return conf, errors.Wrap(err, "GUBER_ADVERTISE_ADDRESS is invalid; expected format is `address:port`")
		}
		advAddr, err = ResolveHostIP(advAddr)
		if err != nil {
			return conf, errors.Wrap(err, "failed to discover host ip for GUBER_ADVERTISE_ADDRESS")
		}
		conf.AdvertiseAddress = net.JoinHostPort(advAddr, advPort)
	}

	// Behaviors
	setter.SetDefault(&conf.Behaviors.BatchTimeout, getEnvDuration(log, "GUBER_BATCH_TIMEOUT"))
	setter.SetDefault(&conf.Behaviors.BatchLimit, getEnvInteger(log, "GUBER_BATCH_LIMIT"))
	setter.SetDefault(&conf.Behaviors.BatchWait, getEnvDuration(log, "GUBER_BATCH_WAIT"))
	setter.SetDefault(&conf.Behaviors.DisableBatching, getEnvBool(log, "GUBER_DISABLE_BATCHING"))

	setter.SetDefault(&conf.Behaviors.GlobalTimeout, getEnvDuration(log, "GUBER_GLOBAL_TIMEOUT"))
	setter.SetDefault(&conf.Behaviors.GlobalBatchLimit, getEnvInteger(log, "GUBER_GLOBAL_BATCH_LIMIT"))
	setter.SetDefault(&conf.Behaviors.GlobalSyncWait, getEnvDuration(log, "GUBER_GLOBAL_SYNC_WAIT"))
	setter.SetDefault(&conf.Behaviors.ForceGlobal, getEnvBool(log, "GUBER_FORCE_GLOBAL"))

	// TLS Config
	if anyHasPrefix("GUBER_TLS_", os.Environ()) {
		conf.TLS = &TLSConfig{}
		setter.SetDefault(&conf.TLS.CaFile, os.Getenv("GUBER_TLS_CA"))
		setter.SetDefault(&conf.TLS.CaKeyFile, os.Getenv("GUBER_TLS_CA_KEY"))
		setter.SetDefault(&conf.TLS.KeyFile, os.Getenv("GUBER_TLS_KEY"))
		setter.SetDefault(&conf.TLS.CertFile, os.Getenv("GUBER_TLS_CERT"))
		setter.SetDefault(&conf.TLS.AutoTLS, getEnvBool(log, "GUBER_TLS_AUTO"))
		setter.SetDefault(&conf.TLS.MinVersion, getEnvMinVersion(log, "GUBER_TLS_MIN_VERSION"))

		clientAuth := os.Getenv("GUBER_TLS_CLIENT_AUTH")
		if clientAuth != "" {
			clientAuthTypes := map[string]tls.ClientAuthType{
				"request-cert":       tls.RequestClientCert,
				"verify-cert":        tls.VerifyClientCertIfGiven,
				"require-any-cert":   tls.RequireAnyClientCert,
				"require-and-verify": tls.RequireAndVerifyClientCert,
			}
			t, ok := clientAuthTypes[clientAuth]
			if !ok {
				return conf, errors.Errorf("'GUBER_TLS_CLIENT_AUTH=%s' is invalid; choices are [%s]",
					clientAuth, validClientAuthTypes(clientAuthTypes))
			}
			conf.TLS.ClientAuth = t
		}
		setter.SetDefault(&conf.TLS.ClientAuthKeyFile, os.Getenv("GUBER_TLS_CLIENT_AUTH_KEY"))
		setter.SetDefault(&conf.TLS.ClientAuthCertFile, os.Getenv("GUBER_TLS_CLIENT_AUTH_CERT"))
		setter.SetDefault(&conf.TLS.ClientAuthCaFile, os.Getenv("GUBER_TLS_CLIENT_AUTH_CA_CERT"))
		setter.SetDefault(&conf.TLS.InsecureSkipVerify, getEnvBool(log, "GUBER_TLS_INSECURE_SKIP_VERIFY"))
		setter.SetDefault(&conf.TLS.ClientAuthServerName, os.Getenv("GUBER_TLS_CLIENT_AUTH_SERVER_NAME"))
	}

	// ETCD Config
	setter.SetDefault(&conf.EtcdPoolConf.KeyPrefix, os.Getenv("GUBER_ETCD_KEY_PREFIX"), "/gubernator-peers")
	setter.SetDefault(&conf.EtcdPoolConf.EtcdConfig, &etcd.Config{})
	setter.SetDefault(&conf.EtcdPoolConf.EtcdConfig.Endpoints, getEnvSlice("GUBER_ETCD_ENDPOINTS"), []string{"localhost:2379"})
	setter.SetDefault(&conf.EtcdPoolConf.EtcdConfig.DialTimeout, getEnvDuration(log, "GUBER_ETCD_DIAL_TIMEOUT"), clock.Second*5)
	setter.SetDefault(&conf.EtcdPoolConf.EtcdConfig.Username, os.Getenv("GUBER_ETCD_USER"))
	setter.SetDefault(&conf.EtcdPoolConf.EtcdConfig.Password, os.Getenv("GUBER_ETCD_PASSWORD"))
	setter.SetDefault(&conf.EtcdPoolConf.Advertise.GRPCAddress, os.Getenv("GUBER_ETCD_ADVERTISE_ADDRESS"), conf.AdvertiseAddress)
	setter.SetDefault(&conf.EtcdPoolConf.Advertise.DataCenter, os.Getenv("GUBER_ETCD_DATA_CENTER"), conf.DataCenter)

	setter.SetDefault(&conf.MemberListPoolConf.Advertise.GRPCAddress, os.Getenv("GUBER_MEMBERLIST_ADVERTISE_ADDRESS"), conf.AdvertiseAddress)
	setter.SetDefault(&conf.MemberListPoolConf.MemberListAddress, os.Getenv("GUBER_MEMBERLIST_ADDRESS"), fmt.Sprintf("%s:7946", advAddr))
	setter.SetDefault(&conf.MemberListPoolConf.KnownNodes, getEnvSlice("GUBER_MEMBERLIST_KNOWN_NODES"), []string{})
	setter.SetDefault(&conf.MemberListPoolConf.Advertise.DataCenter, conf.DataCenter)

	// Kubernetes Config
	setter.SetDefault(&conf.K8PoolConf.Namespace, os.Getenv("GUBER_K8S_NAMESPACE"), "default")
	conf.K8PoolConf.PodIP = os.Getenv("GUBER_K8S_POD_IP")
	conf.K8PoolConf.PodPort = os.Getenv("GUBER_K8S_POD_PORT")
	conf.K8PoolConf.Selector = os.Getenv("GUBER_K8S_ENDPOINTS_SELECTOR")
	var assignErr error
	conf.K8PoolConf.Mechanism, assignErr = WatchMechanismFromString(os.Getenv("GUBER_K8S_WATCH_MECHANISM"))
	if assignErr != nil {
		return conf, errors.New("invalid value for watch mechanism " +
			"`GUBER_K8S_WATCH_MECHANISM` needs to be either 'endpoints' or 'pods' (defaults to 'endpoints')")
	}

	// DNS Config
	setter.SetDefault(&conf.DNSPoolConf.FQDN, os.Getenv("GUBER_DNS_FQDN"))
	setter.SetDefault(&conf.DNSPoolConf.ResolvConf, os.Getenv("GUBER_RESOLV_CONF"), "/etc/resolv.conf")
	setter.SetDefault(&conf.DNSPoolConf.OwnAddress, conf.AdvertiseAddress)

	// PeerPicker Config
	if pp := os.Getenv("GUBER_PEER_PICKER"); pp != "" {
		var replicas int
		var hash string

		switch pp {
		case "replicated-hash":
			setter.SetDefault(&replicas, getEnvInteger(log, "GUBER_REPLICATED_HASH_REPLICAS"), defaultReplicas)
			conf.Picker = NewReplicatedConsistentHash(nil, replicas)
			setter.SetDefault(&hash, os.Getenv("GUBER_PEER_PICKER_HASH"), "fnv1a")
			hashFuncs := map[string]HashString64{
				"fnv1a": fnv1a.HashString64,
				"fnv1":  fnv1.HashString64,
			}
			fn, ok := hashFuncs[hash]
			if !ok {
				return conf, errors.Errorf("'GUBER_PEER_PICKER_HASH=%s' is invalid; choices are [%s]",
					hash, validHash64Keys(hashFuncs))
			}
			conf.Picker = NewReplicatedConsistentHash(fn, replicas)
		default:
			return conf, errors.Errorf("'GUBER_PEER_PICKER=%s' is invalid; choices are ['replicated-hash', 'consistent-hash']", pp)
		}
	}

	if anyHasPrefix("GUBER_K8S_", os.Environ()) {
		log.Debug("K8s peer pool config found")
		if conf.K8PoolConf.Selector == "" {
			return conf, errors.New("when using k8s for peer discovery, you MUST provide a " +
				"`GUBER_K8S_ENDPOINTS_SELECTOR` to select the gubernator peers from the endpoints listing")
		}
	}

	if anyHasPrefix("GUBER_MEMBERLIST_", os.Environ()) {
		log.Debug("Memberlist pool config found")
		if len(conf.MemberListPoolConf.KnownNodes) == 0 {
			return conf, errors.New("when using `member-list` for peer discovery, you MUST provide a " +
				"hostname of a known host in the cluster via `GUBER_MEMBERLIST_KNOWN_NODES`")
		}
	}

	if anyHasPrefix("GUBER_DNS_", os.Environ()) {
		log.Debug("DNS peer pool config found")
	}

	// If env contains any TLS configuration
	if anyHasPrefix("GUBER_ETCD_TLS_", os.Environ()) {
		if err := setupEtcdTLS(conf.EtcdPoolConf.EtcdConfig); err != nil {
			return conf, err
		}
	}

	if DebugEnabled {
		log.Debug(spew.Sdump(conf))
	}

	setter.SetDefault(&conf.TraceLevel, GetTracingLevel())

	return conf, nil
}

// LocalHost returns the local IPV interface which Gubernator should bind to by default.
// There are a few reasons why the interface will be different on different platforms.
//
// ### Linux ###
// Gubernator should bind to either localhost IPV6 or IPV4 on Linux. In most cases using the DNS name "localhost"
// will result in binding to the correct interface depending on the Linux network configuration.
//
// ### GitHub Actions ###
// GHA does not support IPV6, as such trying to bind to `[::1]` will result in an
// error while running in GHA even if the linux runner defaults `localhost` to IPV6.
// As such we explicitly bind to 127.0.0.1 for GHA.
//
// ### Darwin (Apple OSX) ###
// Later versions of OSX bind to the publicly addressable interface if we use the DNS name "localhost"
// which is not the loop back interface. As a result OSX will warn the user with the message
// "Do you want the application to accept incoming network connections?" every time Gubernator is
// run, including when running unit tests. So for OSX we return 127.0.0.1.
func LocalHost() string {
	// If running on OSX
	if runtime.GOOS == "darwin" {
		return "127.0.0.1"
	}

	// If running on Github Actions
	if os.Getenv("RUNNER_OS") != "" {
		return "127.0.0.1"
	}

	// All others (Linux) return "localhost"
	return "localhost"
}

func setupEtcdTLS(conf *etcd.Config) error {
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
		if pemBytes, err := os.ReadFile(tlsCAFile); err == nil {
			certPool = x509.NewCertPool()
			certPool.AppendCertsFromPEM(pemBytes)
		} else {
			return errors.Wrapf(err, "while loading cert CA file '%s'", tlsCAFile)
		}
		setter.SetDefault(&conf.TLS.RootCAs, certPool)
		conf.TLS.InsecureSkipVerify = false
	}

	// If the cert and key files are provided, attempt to load them
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

func getEnvBool(log logrus.FieldLogger, name string) bool {
	v := os.Getenv(name)
	if v == "" {
		return false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		log.WithError(err).Errorf("while parsing '%s' as an boolean", name)
		return false
	}
	return b
}

func getEnvMinVersion(log logrus.FieldLogger, name string) uint16 {
	v := os.Getenv(name)
	if v == "" {
		return tls.VersionTLS13
	}
	minVersion := map[string]uint16{
		"1.0": tls.VersionTLS10,
		"1.1": tls.VersionTLS11,
		"1.2": tls.VersionTLS12,
		"1.3": tls.VersionTLS13,
	}
	version, ok := minVersion[v]
	if !ok {
		log.WithError(fmt.Errorf("unknown tls version: %s", v)).Errorf("while parsing '%s' as an min tls version, defaulting to 1.3", name)
		return tls.VersionTLS13
	}
	return version
}

func getEnvInteger(log logrus.FieldLogger, name string) int {
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

func getEnvDuration(log logrus.FieldLogger, name string) time.Duration {
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
func fromEnvFile(log logrus.FieldLogger, configFile string) error {
	fd, err := os.Open(configFile)
	if err != nil {
		return fmt.Errorf("while opening config file: %s", err)
	}

	contents, err := io.ReadAll(fd)
	if err != nil {
		return fmt.Errorf("while reading config file '%s': %s", configFile, err)
	}
	for i, line := range strings.Split(string(contents), "\n") {
		// Skip comments, empty lines or lines with tabs
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, " ") ||
			strings.HasPrefix(line, "\t") || len(line) == 0 {
			continue
		}

		log.Debugf("config: [%d] '%s'", i, line)
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return errors.Errorf("malformed key=value on line '%d'", i)
		}

		if err := os.Setenv(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])); err != nil {
			return errors.Wrapf(err, "while settings environ for '%s=%s'", parts[0], parts[1])
		}
	}
	return nil
}

func validClientAuthTypes(m map[string]tls.ClientAuthType) string {
	var rs []string
	for k := range m {
		rs = append(rs, k)
	}
	return strings.Join(rs, ",")
}

func validHash64Keys(m map[string]HashString64) string {
	var rs []string
	for k := range m {
		rs = append(rs, k)
	}
	return strings.Join(rs, ",")
}

// GetInstanceID attempts to source a unique id from the environment,
// if none is found, then it will generate a random instance id.
func GetInstanceID() string {
	var id string

	// Use in order, if the result is "" (empty string) then the next option will be used
	//  1. The environment variable `GUBER_INSTANCE_ID`
	//  2. The id of the docker container we are running in
	//  3. Generate a random id
	setter.SetDefault(&id, os.Getenv("GUBER_INSTANCE_ID"), getDockerCID(), generateID())

	return id
}

func generateID() string {
	token := make([]byte, 5)
	_, _ = rand.Read(token)
	return hex.EncodeToString(token)
}

func getDockerCID() string {
	f, err := os.Open("/proc/self/cgroup")
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, "/docker/")
		if len(parts) != 2 {
			continue
		}

		fullDockerCID := parts[1]
		return fullDockerCID[:12]
	}
	return ""
}

func GetTracingLevel() tracing.Level {
	s := os.Getenv("GUBER_TRACING_LEVEL")
	lvl, ok := map[string]tracing.Level{
		"ERROR": tracing.ErrorLevel,
		"INFO":  tracing.InfoLevel,
		"DEBUG": tracing.DebugLevel,
	}[s]
	if ok {
		return lvl
	}
	return tracing.InfoLevel
}

var TraceLevelInfoFilter = otelgrpc.Filter(func(info *otelgrpc.InterceptorInfo) bool {
	if info.UnaryServerInfo != nil {
		if info.UnaryServerInfo.FullMethod == "/pb.gubernator.PeersV1/GetPeerRateLimits" {
			return false
		}
		if info.UnaryServerInfo.FullMethod == "/pb.gubernator.V1/HealthCheck" {
			return false
		}
	}
	if info.Method == "/pb.gubernator.PeersV1/GetPeerRateLimits" {
		return false
	}
	if info.Method == "/pb.gubernator.V1/HealthCheck" {
		return false
	}
	return true
})
