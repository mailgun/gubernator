package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	etcd "github.com/coreos/etcd/clientv3"
	"github.com/ghodss/yaml"
	"github.com/gorilla/mux"
	"github.com/mailgun/holster"
	"github.com/mailgun/holster/etcdutil"
	"github.com/mailgun/service/httpapi"
	"github.com/mailgun/service/internal/debug"
	"github.com/mailgun/service/internal/logging"
	"github.com/mailgun/service/internal/metrics"
	"github.com/mailgun/service/internal/vulcand"
	"github.com/mailgun/service/testutils"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type featureMask int

const (
	EnvDev  = "dev"
	EnvTest = "test"

	startUpTimeout                = 60 * time.Second
	defaultVulcandRegistrationTTL = 30 * time.Second

	FeatureFetchConfig featureMask = 1 << iota
	FeatureEtcd
	FeatureMetrics
	FeatureVulcand
	featureMeta    // always disabled by StartInTest
	featureDebug   // always disabled by StartInTest
	featureSignals // always disabled by StartInTest

	// All features that make sense to disable in tests.
	FeaturesAll = FeatureFetchConfig | FeatureEtcd | FeatureMetrics | FeatureVulcand
)

var (
	etcdClt *etcd.Client
	flags   *flag.FlagSet
)

// Log returns the default logger. After service.Run is called the logger
// if properly initialized according to the service config and adorned with
// metadata.
func Log() *logrus.Entry {
	return logging.Log()
}

type MetricsClient = metrics.Client

// Metrics returns a metrics client. Before service.Run it operates as
// /dev/null, but afterwards it is properly initialized to whatever metrics
// target server is configured, including /dev/null.
func Metrics() MetricsClient {
	return metrics.Clt()
}

// Etcd returns an Etcd v3 client. It is initialized in service.Run to be
// available when Start method of a service.Service implementation passed via
// svc parameter is called.
func Etcd() *etcd.Client {
	return etcdClt
}

// Config is an interface that each Mailgun service config should implement.
type Config interface {
	BasicCfg() *BasicConfig
}

// BasicConfig is a configuration subset common for all Mailgun services. Each
// service should define its own Config struct that aggregates BasicConfig.
type BasicConfig struct {
	Logging logging.Config `json:"logging"`
	Metrics metrics.Config `json:"statsd"`

	// Deprecated: use HTTPPort instead
	BasePort         uint32 `json:"base_api_port"`
	HTTPPort         uint32 `json:"http_port"`
	GRPCPort         uint32 `json:"grpc_port"`
	PublicAPIHost    string `json:"public_api_host"`
	ProtectedAPIHost string `json:"protected_api_host"`
	ProtectedAPIURL  string `json:"protected_api_url"`
	VulcandNamespace string `json:"vulcand_namespace"`
	Features         struct {
		NoEtcd    bool `json:"no_etcd"`
		NoMetrics bool `json:"no_metrics"`
		NoVulcand bool `json:"no_vulcand"`
		NoDebug   bool `json:"no_debug"`
	} `json:"features"`
}

func (bc *BasicConfig) BasicCfg() *BasicConfig { return bc }

// Service is an interface that each Mailgun service should implement
type Service interface {
	BasicSvc() *BasicService
	Start(ctx context.Context) error
	Stop() error
}

// BasicService is a basic service implementation. Each service is supposed
// to aggregate it.
type BasicService struct {
	Meta struct {
		AppName    string
		Env        string
		PID        uint32
		CID        string
		Hostname   string
		FQDN       string
		InstanceID string
	}
	disabledFeatures featureMask

	basicCfg       *BasicConfig
	svc            Service
	httpRouter     *mux.Router
	httpListener   net.Listener
	httpSrv        *http.Server
	httpSrvErrorCh chan error
	vulcandReg     *vulcand.Registry
	args           []string

	preStopOnce sync.Once
	stopOnce    sync.Once
	stopCh      chan struct{}
	stopWG      sync.WaitGroup
	mutex       sync.Mutex
}

func (bs *BasicService) BasicSvc() *BasicService { return bs }

// AddHandler is supposed to be called by a service implementation in its
// Start method to define HTTP API endpoints.
func (bs *BasicService) AddHandler(spec httpapi.Spec) error {
	if err := httpapi.AddHandler(bs.httpRouter, spec); err != nil {
		return errors.Wrap(err, "while adding API handler to router")
	}
	// If the vulcand registry is not initialized then we are running it test
	// mode and should skip frontend registration.
	if bs.vulcandReg == nil {
		return nil
	}
	for _, path := range spec.Paths() {
		if spec.Scope <= httpapi.ScopePublic {
			bs.vulcandReg.AddFrontend(bs.basicCfg.PublicAPIHost, path, spec.Method, spec.Middlewares)
		}
		if spec.Scope <= httpapi.ScopeProtected {
			bs.vulcandReg.AddFrontend(bs.basicCfg.ProtectedAPIHost, path, spec.Method, nil)
		}
	}
	return nil
}

// Return the underlying mux router. Use `AddHandler()` instead.
// Only use this if you need to register local mux based middleware
func (bs *BasicService) Mux() *mux.Router { return bs.httpRouter }

// RunOption is an optional arguments type of the Run function.
type RunOption func(bs *BasicService) error

// WithName is a RunOption that allows to a service name to be used in logs,
// vulcand registration etc. By default the service name is set to be the name
// of the service binary file.
func WithName(appName string) RunOption {
	return func(bs *BasicService) error {
		bs.Meta.AppName = appName
		return nil
	}
}

// WithInstanceID is a RunOption that allows you to specify service instance id.
// if an instance id is not provided a unique instance id will be generated
func WithInstanceID(id string) RunOption {
	return func(bs *BasicService) error {
		bs.Meta.InstanceID = id
		return nil
	}
}

// NoFeatures is a RunOption that disables listed startup features. It is
// intended solely to be used in StartInTest. Returns an error if used in
// a any environment but EnvTest.
func NoFeatures(features featureMask) RunOption {
	return func(bs *BasicService) error {
		if bs.Meta.Env != EnvTest {
			return errors.Errorf("features can only be disabled in tests")
		}
		bs.disabledFeatures |= features
		return nil
	}
}

// Run runs a Mailgun service.
func Run(cfg Config, svc Service, opts ...RunOption) {
	if err := start(cfg, svc, opts...); err != nil {
		Log().WithError(err).Fatal("Crash boom bang!")
	}
	bs := svc.BasicSvc()

	// Now we block and wait for the HTTP server to terminate. That may be a
	// result of receiving a term signal and subsequent graceful shutdown or
	// due to an unexpected HTTP server error.
	err := <-bs.httpSrvErrorCh

	bs.preStop()
	bs.stop()
	bs.stopWG.Wait()

	if err != nil {
		Log().WithError(err).Fatal("Crash boom bang!")
	}
	Log().Infof("Over and out")
}

func start(cfg Config, svc Service, opts ...RunOption) error {
	ctx, cancel := context.WithTimeout(context.Background(), startUpTimeout)
	defer cancel()
	var err error

	// Initialize BasicService internals.
	bs := svc.BasicSvc()

	bs.mutex.Lock()
	defer bs.mutex.Unlock()

	bs.svc = svc
	basicCfg := cfg.BasicCfg()
	bs.basicCfg = basicCfg
	bs.stopCh = make(chan struct{})
	bs.httpSrvErrorCh = make(chan error, 1)
	bs.httpRouter = mux.NewRouter()
	bs.httpRouter.UseEncodedPath()
	bs.Meta.AppName = getExecutableName()
	holster.SetDefault(&bs.Meta.Env, os.Getenv("MG_ENV"), EnvDev)

	// Apply optional parameters.
	for _, opt := range opts {
		if err := opt(bs); err != nil {
			return errors.Wrapf(err, "while applying option %v", opt)
		}
	}

	flags = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	argConfigFile := flags.String("config", "",
		"configuration file to use (in YAML format)")
	argNoEtcd := flags.Bool("no-etcd", false,
		"skip etcd client initialization")
	argNoMetrics := flags.Bool("no-metrics", false,
		"skip metrics initialization")
	argNoVulcand := flags.Bool("no-vulcand", false,
		"skip vulcand registration")
	argNoDebug := flags.Bool("no-debug", false,
		"don't include trace and profile API handlers")
	argPortHTTP := flags.Int("http-port", 0,
		"override the HTTP port number defined in the configuration")
	argPortGRPC := flags.Int("grpc-port", 0,
		"override the GRPC port number defined in the configuration")

	// Allow user to provide args for testing
	holster.SetDefault(&bs.args, os.Args[1:])
	// Parse command line arguments
	if err := flags.Parse(bs.args); err != nil {
		return errors.New("error parsing cli flags")
	}

	// Load config from a YAML file if it was specified on the command line.
	if len(*argConfigFile) != 0 {
		bs.disabledFeatures |= FeatureFetchConfig

		contents, err := ioutil.ReadFile(*argConfigFile)
		if err != nil {
			return errors.Wrapf(err, "while reading '%s'", *argConfigFile)
		}
		if err := yaml.Unmarshal(contents, cfg); err != nil {
			return errors.Wrapf(err, "while parsing '%s'", *argConfigFile)
		}
	}

	// Disable features on demand.
	if *argNoEtcd || bs.basicCfg.Features.NoEtcd {
		bs.disabledFeatures |= FeatureEtcd
	}
	if *argNoMetrics || bs.basicCfg.Features.NoMetrics {
		bs.disabledFeatures |= FeatureMetrics
	}
	if *argNoVulcand || bs.basicCfg.Features.NoVulcand {
		bs.disabledFeatures |= FeatureVulcand
	}
	if *argNoDebug || bs.basicCfg.Features.NoDebug {
		bs.disabledFeatures |= featureDebug
	}

	if bs.disabledFeatures&FeatureFetchConfig == 0 && bs.disabledFeatures&FeatureEtcd != 0 {
		return errors.New("Etcd is disabled and no config file was passed.")
	}

	// Create an Etcd client.
	if bs.disabledFeatures&FeatureFetchConfig == 0 || bs.disabledFeatures&FeatureEtcd == 0 {
		if etcdClt, err = etcdutil.NewClient(nil); err != nil {
			return errors.Wrap(err, "while creating Etcd client")
		}
	}

	// Fetch the service config from Etcd if it was not disabled.
	if bs.disabledFeatures&FeatureFetchConfig == 0 {
		if err := fetchConfig(ctx, cfg, bs.Meta.Env, bs.Meta.AppName, etcdClt); err != nil {
			return errors.Wrap(err, "while fetching config from Etcd")
		}
	}

	// If CLI or ENV is provided, override any existing port config
	holster.SetOverride(&bs.basicCfg.GRPCPort, uint32(*argPortGRPC),
		envGetUint32("MG_GRPC_PORT"))
	holster.SetOverride(&bs.basicCfg.HTTPPort, bs.basicCfg.BasePort, uint32(*argPortHTTP),
		envGetUint32("MG_HTTP_PORT"))

	// Initialize the global logger.
	if err := logging.Init(ctx, basicCfg.Logging, bs.Meta.AppName); err != nil {
		return errors.Wrap(err, "while initializing logging")
	}

	// Fill service metadata.
	if bs.disabledFeatures&featureMeta == 0 {
		bs.Meta.PID = uint32(os.Getpid())
		if bs.Meta.CID, err = getDockerCID(); err != nil {
			Log().WithError(err).Warnf("Failed to fetch docker id")
		}
		if bs.Meta.Hostname, err = os.Hostname(); err != nil {
			return errors.Wrap(err, "while getting hostname")
		}
		if bs.Meta.FQDN, err = getFQDN(); err != nil {
			return errors.Wrap(err, "while getting FQDN")
		}

		// Use docker container id if not empty, else generate an random ID
		var uniqueID string
		holster.SetDefault(&uniqueID, bs.Meta.CID, generateID())

		// If not already set, use environment variable, else create one
		holster.SetDefault(&bs.Meta.InstanceID, os.Getenv("MG_INSTANCE_ID"),
			bs.Meta.AppName+"_"+bs.Meta.Hostname+"_"+uniqueID)

	}

	// Initialize a global metrics client. If disabled or the statsd config is
	// a zero value, then the client is usable but discards submitted metrics.
	if bs.disabledFeatures&FeatureMetrics == 0 {
		metrics.Init(basicCfg.Metrics, bs.Meta.AppName, bs.Meta.Hostname)
	} else {
		Log().Info("Disabled Metrics Client")
	}

	if bs.disabledFeatures&FeatureVulcand == 0 {
		// Used by Flagman middleware.
		vulcand.InitFlagmanAPIBaseURL(basicCfg.ProtectedAPIURL)

		// Create Vulcand registry.
		if bs.vulcandReg, err = vulcand.NewRegistry(
			etcdClt,
			basicCfg.VulcandNamespace,
			bs.Meta.AppName,
			bs.Meta.Hostname,
			"0.0.0.0",
			int(basicCfg.HTTPPort),
			defaultVulcandRegistrationTTL,
		); err != nil {
			return errors.Wrap(err, "while creating Vulcand registry")
		}
	} else {
		Log().Info("Disabled Vulcand Registration")
	}

	if bs.disabledFeatures&featureDebug == 0 {
		debug.InitPprofAPI(bs.httpRouter)
		debug.InitHTTPTraceAPI(bs.httpRouter)
	}

	// Create HTTP router for API and initialize it with default endpoints.
	httpapi.InitDefaultAPI(bs.httpRouter)

	// Perform service specific initialization routine.
	if err = svc.Start(ctx); err != nil {
		return errors.Wrap(err, "while starting service")
	}

	// Start HTTP API server.
	httpAddr := fmt.Sprintf("0.0.0.0:%d", basicCfg.HTTPPort)
	if bs.httpListener, err = net.Listen("tcp", httpAddr); err != nil {
		return errors.Wrapf(err, "failed to listen on %v", basicCfg.HTTPPort)
	}
	bs.httpSrv = &http.Server{
		Handler: bs.httpRouter,
	}
	bs.stopWG.Add(1)
	go bs.serveHTTP()

	// Setup signal notifications and start a signal handler
	if bs.disabledFeatures&featureSignals == 0 {
		signalCh := make(chan os.Signal, 10)
		signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGUSR1)
		bs.stopWG.Add(1)
		go bs.handleSignals(signalCh)
	}

	// Start heartbeat with Vulcand.
	if bs.disabledFeatures&FeatureVulcand == 0 {
		if err = bs.vulcandReg.Start(ctx); err != nil {
			return errors.Wrap(err, "while starting heartbeat with Vucland")
		}
	}

	// Everything is up and running.
	Log().Infof("Up and running")
	return nil
}

func (bs *BasicService) serveHTTP() {
	defer bs.stopWG.Done()
	defer close(bs.httpSrvErrorCh)
	if err := bs.httpSrv.Serve(bs.httpListener); err != http.ErrServerClosed {
		bs.httpSrvErrorCh <- errors.Wrap(err, "while serving HTTP")
	}
}

func (bs *BasicService) handleSignals(signalCh chan os.Signal) {
	defer bs.stopWG.Done()
	for {
		select {
		case sig := <-signalCh:
			switch sig {
			case syscall.SIGTERM:
				fallthrough
			case syscall.SIGINT:
				bs.preStop()
				bs.stop()
				return

			case syscall.SIGUSR1:
				bs.preStop()
			}
		}
	}
}

func (bs *BasicService) preStop() {
	bs.preStopOnce.Do(func() {
		bs.mutex.Lock()
		Log().Info("Executing pre-stop sequence...")
		if bs.vulcandReg != nil {
			bs.vulcandReg.Stop()
		}
		bs.mutex.Unlock()
	})
}

func (bs *BasicService) stop() {
	bs.stopOnce.Do(func() {
		Log().Info("Executing stop sequence...")

		if bs.httpSrv != nil {
			if err := bs.httpSrv.Shutdown(context.TODO()); err != nil {
				Log().WithError(err).Error("Failed to stop HTTP server")
			}
			Log().Info("HTTP server shutdown")
		} else {
			Log().Info("Skipped HTTP server shutdown: wasn't running")
		}

		bs.mutex.Lock()
		if err := bs.svc.Stop(); err != nil {
			Log().WithError(err).Error("Failed to stop service")
		}
		Log().Info("Business logic shutdown")
		if etcdClt != nil {
			if err := etcdClt.Close(); err != nil {
				Log().WithError(err).Error("Failed to close Etcd client")
			}
		}
		etcdClt = nil
		bs.mutex.Unlock()
	})
}

func fetchConfig(ctx context.Context, cfg Config, env, app string, etcdClt *etcd.Client) error {
	key := fmt.Sprintf("/mailgun/configs/%s/%s", env, app)
	rs, err := etcdClt.Get(ctx, key)
	if err != nil {
		return errors.Wrap(err, "while fetching config from etcd")
	}
	if len(rs.Kvs) == 0 {
		return errors.Errorf("config '%s' missing in etcd", key)
	}
	if err := json.Unmarshal(rs.Kvs[0].Value, cfg); err != nil {
		return errors.Wrap(err, "during unmarshal of config")
	}
	return nil
}

func getExecutableName() string {
	parts := strings.Split(os.Args[0], "/")
	if len(parts) == 0 {
		panic("Must never happen")
	}
	return parts[len(parts)-1]
}

func getDockerCID() (string, error) {
	f, err := os.Open("/proc/self/cgroup")
	if err != nil {
		return "", errors.Wrap(err, "cannot open /proc/self/cgroup")
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
		return fullDockerCID[:12], nil
	}
	return "", errors.New("cannot find Docker cgroup")
}

func getFQDN() (string, error) {
	cmd := exec.Command("/bin/hostname", "-f")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return "", err
	}
	cmdOutput := out.String()
	return cmdOutput[:len(cmdOutput)-1], nil
}

func generateID() string {
	token := make([]byte, 5)
	_, _ = rand.Read(token)
	return hex.EncodeToString(token)
}

func envGetUint32(name string) uint32 {
	if val := os.Getenv(name); val != "" {
		integer, _ := strconv.ParseInt(val, 10, 32)
		return uint32(integer)
	}
	return 0
}

// StartInTest starts a
func StartInTest(cfg Config, svc Service, opts ...RunOption) (*testutils.HTTPClient, error) {
	bs := svc.BasicSvc()
	bs.Meta.Env = EnvTest
	bs.Meta.AppName = "test"
	bs.Meta.Hostname = "localhost"
	bs.disabledFeatures = featureSignals | featureDebug | featureMeta

	if err := start(cfg, svc, opts...); err != nil {
		bs.preStop()
		bs.stop()
		return nil, errors.Wrap(err, "while starting service")
	}
	baseURL := "http://" + bs.httpListener.Addr().String()
	return testutils.NewHTTPClient(baseURL), nil
}

// StopInTest stops a service started with StartInTest. As the name suggests it
// must only be used in tests
func (bs *BasicService) StopInTest() {
	if bs.Meta.Env != EnvTest {
		panic("Must only be used in tests")
	}
	bs.preStop()
	bs.stop()
	Log().Infof("Live long and prosper!")
}
