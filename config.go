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

package gubernator

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	etcd "github.com/coreos/etcd/clientv3"
	"github.com/davecgh/go-spew/spew"
	"github.com/mailgun/holster/v3/clock"
	"github.com/mailgun/holster/v3/setter"
	"github.com/mailgun/holster/v3/slice"
	"github.com/pkg/errors"
	"github.com/segmentio/fasthash/fnv1"
	"github.com/segmentio/fasthash/fnv1a"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

type BehaviorConfig struct {
	// How long we should wait for a batched response from a peer
	BatchTimeout time.Duration
	// How long we should wait before sending a batched request
	BatchWait time.Duration
	// The max number of requests we can batch into a single peer request
	BatchLimit int

	// How long a non-owning peer should wait before syncing hits to the owning peer
	GlobalSyncWait time.Duration
	// How long we should wait for global sync responses from peers
	GlobalTimeout time.Duration
	// The max number of global updates we can batch into a single peer request
	GlobalBatchLimit int

	// How long the current region will collect request before pushing them to other regions
	MultiRegionSyncWait time.Duration
	// How long the current region will wait for responses from other regions
	MultiRegionTimeout time.Duration
	// The max number of requests the current region will collect
	MultiRegionBatchLimit int
}

// config for a gubernator instance
type Config struct {
	// Required
	GRPCServer *grpc.Server

	// (Optional) Adjust how gubernator behaviors are configured
	Behaviors BehaviorConfig

	// (Optional) The cache implementation
	Cache Cache

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
	Logger logrus.FieldLogger

	// (Optional) The TLS config used when connecting to gubernator peers
	PeerTLS *tls.Config
}

func (c *Config) SetDefaults() error {
	setter.SetDefault(&c.Behaviors.BatchTimeout, time.Millisecond*500)
	setter.SetDefault(&c.Behaviors.BatchLimit, maxBatchSize)
	setter.SetDefault(&c.Behaviors.BatchWait, time.Microsecond*500)

	setter.SetDefault(&c.Behaviors.GlobalTimeout, time.Millisecond*500)
	setter.SetDefault(&c.Behaviors.GlobalBatchLimit, maxBatchSize)
	setter.SetDefault(&c.Behaviors.GlobalSyncWait, time.Microsecond*500)

	setter.SetDefault(&c.Behaviors.MultiRegionTimeout, time.Millisecond*500)
	setter.SetDefault(&c.Behaviors.MultiRegionBatchLimit, maxBatchSize)
	setter.SetDefault(&c.Behaviors.MultiRegionSyncWait, time.Second)

	setter.SetDefault(&c.LocalPicker, NewReplicatedConsistentHash(nil, DefaultReplicas))
	setter.SetDefault(&c.RegionPicker, NewRegionPicker(nil))
	setter.SetDefault(&c.Cache, NewLRUCache(0))

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
	DataCenter string
	// (Optional) The http address:port of the peer
	HTTPAddress string
	// (Required) The grpc address:port of the peer
	GRPCAddress string
	// (Optional) Is true if PeerInfo is for this instance of gubernator
	IsOwner bool
}

// Returns the hash key used to identify this peer in the Picker.
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

	// (Optional) The `address:port` that is advertised to other Gubernator peers.
	// Defaults to `GRPCListenAddress`
	AdvertiseAddress string

	// (Optional) The number of items in the cache. Defaults to 50,000
	CacheSize int

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

	// (Optional) Member list configuration used for peer discovery
	MemberListPoolConf MemberListPoolConfig

	// (Optional) The PeerPicker as selected by `GUBER_PEER_PICKER`
	Picker PeerPicker

	// (Optional) A Logger which implements the declared logger interface (typically *logrus.Entry)
	Logger logrus.FieldLogger

	// (Optional) TLS Configuration; SpawnDaemon() will modify the passed TLS config in an
	// attempt to build a complete TLS config if one is not provided.
	TLS *TLSConfig
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

	if configFile != "" {
		log.Infof("Loading env config: %s", configFile)
		if err := fromEnvFile(log, configFile); err != nil {
			return conf, err
		}
	}

	setter.SetDefault(&DebugEnabled, getEnvBool(log, "GUBER_DEBUG"))
	if DebugEnabled {
		logger.SetLevel(logrus.DebugLevel)
		log.Debug("Debug enabled")
	}

	// Main config
	setter.SetDefault(&conf.GRPCListenAddress, os.Getenv("GUBER_GRPC_ADDRESS"), "localhost:81")
	setter.SetDefault(&conf.HTTPListenAddress, os.Getenv("GUBER_HTTP_ADDRESS"), "localhost:80")
	setter.SetDefault(&conf.CacheSize, getEnvInteger(log, "GUBER_CACHE_SIZE"), 50_000)
	setter.SetDefault(&conf.AdvertiseAddress, os.Getenv("GUBER_ADVERTISE_ADDRESS"), conf.GRPCListenAddress)
	setter.SetDefault(&conf.DataCenter, os.Getenv("GUBER_DATA_CENTER"), "")

	advAddr, advPort, err := net.SplitHostPort(conf.AdvertiseAddress)
	if err != nil {
		return conf, errors.Wrap(err, "GUBER_ADVERTISE_ADDRESS is invalid; expected format is `address:port`")
	}
	advAddr, err = ResolveHostIP(advAddr)
	if err != nil {
		return conf, errors.Wrap(err, "failed to discover host ip for GUBER_ADVERTISE_ADDRESS")
	}
	conf.AdvertiseAddress = net.JoinHostPort(advAddr, advPort)

	// Behaviors
	setter.SetDefault(&conf.Behaviors.BatchTimeout, getEnvDuration(log, "GUBER_BATCH_TIMEOUT"))
	setter.SetDefault(&conf.Behaviors.BatchLimit, getEnvInteger(log, "GUBER_BATCH_LIMIT"))
	setter.SetDefault(&conf.Behaviors.BatchWait, getEnvDuration(log, "GUBER_BATCH_WAIT"))

	setter.SetDefault(&conf.Behaviors.GlobalTimeout, getEnvDuration(log, "GUBER_GLOBAL_TIMEOUT"))
	setter.SetDefault(&conf.Behaviors.GlobalBatchLimit, getEnvInteger(log, "GUBER_GLOBAL_BATCH_LIMIT"))
	setter.SetDefault(&conf.Behaviors.GlobalSyncWait, getEnvDuration(log, "GUBER_GLOBAL_SYNC_WAIT"))

	setter.SetDefault(&conf.Behaviors.MultiRegionTimeout, getEnvDuration(log, "GUBER_MULTI_REGION_TIMEOUT"))
	setter.SetDefault(&conf.Behaviors.MultiRegionBatchLimit, getEnvInteger(log, "GUBER_MULTI_REGION_BATCH_LIMIT"))
	setter.SetDefault(&conf.Behaviors.MultiRegionSyncWait, getEnvDuration(log, "GUBER_MULTI_REGION_SYNC_WAIT"))

	choices := []string{"member-list", "k8s", "etcd"}
	setter.SetDefault(&conf.PeerDiscoveryType, os.Getenv("GUBER_PEER_DISCOVERY_TYPE"), "member-list")
	if !slice.ContainsString(conf.PeerDiscoveryType, choices, nil) {
		return conf, fmt.Errorf("GUBER_PEER_DISCOVERY_TYPE is invalid; choices are [%s]`", strings.Join(choices, ","))
	}

	// TLS Config
	setter.SetDefault(&conf.TLS, &TLSConfig{})
	setter.SetDefault(&conf.TLS.CaFile, os.Getenv("GUBER_TLS_CA"))
	setter.SetDefault(&conf.TLS.CaKeyFile, os.Getenv("GUBER_TLS_CA_KEY"))
	setter.SetDefault(&conf.TLS.KeyFile, os.Getenv("GUBER_TLS_KEY"))
	setter.SetDefault(&conf.TLS.CertFile, os.Getenv("GUBER_TLS_CERT"))
	setter.SetDefault(&conf.TLS.AutoTLS, getEnvBool(log, "GUBER_TLS_AUTO"))

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

	// ETCD Config
	setter.SetDefault(&conf.EtcdPoolConf.KeyPrefix, os.Getenv("GUBER_ETCD_KEY_PREFIX"), "/gubernator-peers")
	setter.SetDefault(&conf.EtcdPoolConf.EtcdConfig, &etcd.Config{})
	setter.SetDefault(&conf.EtcdPoolConf.EtcdConfig.Endpoints, getEnvSlice("GUBER_ETCD_ENDPOINTS"), []string{"localhost:2379"})
	setter.SetDefault(&conf.EtcdPoolConf.EtcdConfig.DialTimeout, getEnvDuration(log, "GUBER_ETCD_DIAL_TIMEOUT"), clock.Second*5)
	setter.SetDefault(&conf.EtcdPoolConf.EtcdConfig.Username, os.Getenv("GUBER_ETCD_USER"))
	setter.SetDefault(&conf.EtcdPoolConf.EtcdConfig.Password, os.Getenv("GUBER_ETCD_PASSWORD"))
	setter.SetDefault(&conf.EtcdPoolConf.AdvertiseAddress, os.Getenv("GUBER_ETCD_ADVERTISE_ADDRESS"), conf.AdvertiseAddress)
	setter.SetDefault(&conf.EtcdPoolConf.DataCenter, os.Getenv("GUBER_ETCD_DATA_CENTER"), conf.DataCenter)

	setter.SetDefault(&conf.MemberListPoolConf.AdvertiseAddress, os.Getenv("GUBER_MEMBERLIST_ADVERTISE_ADDRESS"), conf.AdvertiseAddress)
	setter.SetDefault(&conf.MemberListPoolConf.MemberListAddress, os.Getenv("GUBER_MEMBERLIST_ADDRESS"), fmt.Sprintf("%s:7946", advAddr))
	setter.SetDefault(&conf.MemberListPoolConf.KnownNodes, getEnvSlice("GUBER_MEMBERLIST_KNOWN_NODES"), []string{})
	setter.SetDefault(&conf.MemberListPoolConf.DataCenter, conf.DataCenter)

	// Kubernetes Config
	setter.SetDefault(&conf.K8PoolConf.Namespace, os.Getenv("GUBER_K8S_NAMESPACE"), "default")
	conf.K8PoolConf.PodIP = os.Getenv("GUBER_K8S_POD_IP")
	conf.K8PoolConf.PodPort = os.Getenv("GUBER_K8S_POD_PORT")
	conf.K8PoolConf.Selector = os.Getenv("GUBER_K8S_ENDPOINTS_SELECTOR")

	// PeerPicker Config
	if pp := os.Getenv("GUBER_PEER_PICKER"); pp != "" {
		var replicas int
		var hash string

		switch pp {
		case "consistent-hash":
			setter.SetDefault(&hash, os.Getenv("GUBER_PEER_PICKER_HASH"), "fnv1a")
			hashFuncs := map[string]HashFunc{
				"fnv1a": fnv1a.HashBytes32,
				"fnv1":  fnv1.HashBytes32,
				"crc32": nil,
			}
			fn, ok := hashFuncs[hash]
			if !ok {
				return conf, errors.Errorf("'GUBER_PEER_PICKER_HASH=%s' is invalid; choices are [%s]",
					hash, validHashKeys(hashFuncs))
			}
			conf.Picker = NewConsistentHash(fn)

		case "replicated-hash":
			setter.SetDefault(&replicas, getEnvInteger(log, "GUBER_REPLICATED_HASH_REPLICAS"), DefaultReplicas)
			conf.Picker = NewReplicatedConsistentHash(nil, replicas)
			setter.SetDefault(&hash, os.Getenv("GUBER_PEER_PICKER_HASH"), "fnv1a")
			hashFuncs := map[string]HashFunc64{
				"fnv1a": fnv1a.HashBytes64,
				"fnv1":  fnv1.HashBytes64,
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

	if anyHasPrefix("GUBER_ETCD_", os.Environ()) {
		log.Debug("ETCD peer pool config found")
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

	return conf, nil
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
	for k, _ := range m {
		rs = append(rs, k)
	}
	return strings.Join(rs, ",")
}

func validHashKeys(m map[string]HashFunc) string {
	var rs []string
	for k, _ := range m {
		rs = append(rs, k)
	}
	return strings.Join(rs, ",")
}

func validHash64Keys(m map[string]HashFunc64) string {
	var rs []string
	for k, _ := range m {
		rs = append(rs, k)
	}
	return strings.Join(rs, ",")
}
