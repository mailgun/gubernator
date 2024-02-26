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
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/mailgun/holster/v4/errors"
	"github.com/mailgun/holster/v4/etcdutil"
	"github.com/mailgun/holster/v4/setter"
	"github.com/mailgun/holster/v4/syncutil"
	"github.com/mailgun/holster/v4/tracing"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/protobuf/encoding/protojson"
)

type Daemon struct {
	GRPCListeners []net.Listener
	HTTPListener  net.Listener
	V1Server      *V1Instance
	InstanceID    string
	PeerInfo      PeerInfo

	log           FieldLogger
	logWriter     *io.PipeWriter
	pool          PoolInterface
	conf          DaemonConfig
	httpSrv       *http.Server
	httpSrvNoMTLS *http.Server
	grpcSrvs      []*grpc.Server
	wg            syncutil.WaitGroup
	statsHandler  *GRPCStatsHandler
	promRegister  *prometheus.Registry
	gwCancel      context.CancelFunc
	instanceConf  Config
	client        V1Client
}

// SpawnDaemon starts a new gubernator daemon according to the provided DaemonConfig.
// This function will block until the daemon responds to connections as specified
// by GRPCListenAddress and HTTPListenAddress
func SpawnDaemon(ctx context.Context, conf DaemonConfig) (*Daemon, error) {

	s := &Daemon{
		InstanceID: conf.InstanceID,
		log:        conf.Logger,
		conf:       conf,
	}
	return s, s.Start(ctx)
}

func (s *Daemon) Start(ctx context.Context) error {
	var err error

	setter.SetDefault(&s.log, logrus.WithFields(logrus.Fields{
		"instance": s.conf.InstanceID,
		"category": "gubernator",
	}))

	s.promRegister = prometheus.NewRegistry()

	// The LRU cache for storing rate limits.
	cacheCollector := NewLRUCacheCollector()
	if err := s.promRegister.Register(cacheCollector); err != nil {
		return errors.Wrap(err, "during call to promRegister.Register()")
	}

	cacheFactory := func(maxSize int) Cache {
		cache := NewLRUCache(maxSize)
		cacheCollector.AddCache(cache)
		return cache
	}

	// Handler to collect duration and API access metrics for GRPC
	s.statsHandler = NewGRPCStatsHandler()
	_ = s.promRegister.Register(s.statsHandler)

	var filters []otelgrpc.Option
	// otelgrpc deprecated use of interceptors in v0.45.0 in favor of stats
	// handlers to propagate trace context.
	// However, stats handlers do not have a filter feature.
	// See: https://github.com/open-telemetry/opentelemetry-go-contrib/issues/4575
	// if s.conf.TraceLevel != tracing.DebugLevel {
	// 	filters = []otelgrpc.Option{
	// 		otelgrpc.WithInterceptorFilter(TraceLevelInfoFilter),
	// 	}
	// }

	opts := []grpc.ServerOption{
		grpc.StatsHandler(s.statsHandler),
		grpc.MaxRecvMsgSize(1024 * 1024),

		// OpenTelemetry instrumentation on gRPC endpoints.
		grpc.StatsHandler(otelgrpc.NewServerHandler(filters...)),
	}

	if s.conf.GRPCMaxConnectionAgeSeconds > 0 {
		opts = append(opts, grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionAge:      time.Second * time.Duration(s.conf.GRPCMaxConnectionAgeSeconds),
			MaxConnectionAgeGrace: time.Second * time.Duration(s.conf.GRPCMaxConnectionAgeSeconds),
		}))
	}

	if err := SetupTLS(s.conf.TLS); err != nil {
		return err
	}

	if s.conf.ServerTLS() != nil {
		// Create two GRPC server instances, one for TLS and the other for the API Gateway
		opts2 := append(opts, grpc.Creds(credentials.NewTLS(s.conf.ServerTLS())))
		s.grpcSrvs = append(s.grpcSrvs, grpc.NewServer(opts2...))
	}
	s.grpcSrvs = append(s.grpcSrvs, grpc.NewServer(opts...))

	// Registers a new gubernator instance with the GRPC server
	s.instanceConf = Config{
		PeerTraceGRPC: s.conf.TraceLevel >= tracing.DebugLevel,
		PeerTLS:       s.conf.ClientTLS(),
		DataCenter:    s.conf.DataCenter,
		LocalPicker:   s.conf.Picker,
		GRPCServers:   s.grpcSrvs,
		Logger:        s.log,
		CacheFactory:  cacheFactory,
		Behaviors:     s.conf.Behaviors,
		CacheSize:     s.conf.CacheSize,
		Workers:       s.conf.Workers,
		InstanceID:    s.conf.InstanceID,
	}

	s.V1Server, err = NewV1Instance(s.instanceConf)
	if err != nil {
		return errors.Wrap(err, "while creating new gubernator instance")
	}

	// V1Server instance also implements prometheus.Collector interface
	_ = s.promRegister.Register(s.V1Server)

	l, err := net.Listen("tcp", s.conf.GRPCListenAddress)
	if err != nil {
		return errors.Wrap(err, "while starting GRPC listener")
	}
	s.GRPCListeners = append(s.GRPCListeners, l)

	// Start serving GRPC Requests
	s.wg.Go(func() {
		s.log.Infof("GRPC Listening on %s ...", l.Addr().String())
		if err := s.grpcSrvs[0].Serve(l); err != nil {
			s.log.WithError(err).Error("while starting GRPC server")
		}
	})

	var gatewayAddr string
	if s.conf.ServerTLS() != nil {
		// We start a new local GRPC instance because we can't guarantee the TLS cert provided by the
		// user has localhost or the local interface included in the certs' valid hostnames. If they are not
		//  included, it means the local gateway connections will not be able to connect.
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return errors.Wrap(err, "while starting GRPC Gateway listener")
		}
		s.GRPCListeners = append(s.GRPCListeners, l)

		s.wg.Go(func() {
			s.log.Infof("GRPC Gateway Listening on %s ...", l.Addr())
			if err := s.grpcSrvs[1].Serve(l); err != nil {
				s.log.WithError(err).Error("while starting GRPC Gateway server")
			}
		})
		gatewayAddr = l.Addr().String()
	} else {
		gatewayAddr, err = ResolveHostIP(s.conf.GRPCListenAddress)
		if err != nil {
			return errors.Wrap(err, "while resolving GRPC gateway client address")
		}
	}

	switch s.conf.PeerDiscoveryType {
	case "k8s":
		// Source our list of peers from kubernetes endpoint API
		s.conf.K8PoolConf.OnUpdate = s.V1Server.SetPeers
		s.pool, err = NewK8sPool(s.conf.K8PoolConf)
		if err != nil {
			return errors.Wrap(err, "while querying kubernetes API")
		}
	case "etcd":
		s.conf.EtcdPoolConf.OnUpdate = s.V1Server.SetPeers
		// Register ourselves with other peers via ETCD
		s.conf.EtcdPoolConf.Client, err = etcdutil.NewClient(s.conf.EtcdPoolConf.EtcdConfig)
		if err != nil {
			return errors.Wrap(err, "while connecting to etcd")
		}

		s.pool, err = NewEtcdPool(s.conf.EtcdPoolConf)
		if err != nil {
			return errors.Wrap(err, "while creating etcd pool")
		}
	case "dns":
		s.conf.DNSPoolConf.OnUpdate = s.V1Server.SetPeers
		s.pool, err = NewDNSPool(s.conf.DNSPoolConf)
		if err != nil {
			return errors.Wrap(err, "while creating the DNS pool")
		}
	case "member-list":
		s.conf.MemberListPoolConf.OnUpdate = s.V1Server.SetPeers
		s.conf.MemberListPoolConf.Logger = s.log

		// Register peer on the member list
		s.pool, err = NewMemberListPool(ctx, s.conf.MemberListPoolConf)
		if err != nil {
			return errors.Wrap(err, "while creating member list pool")
		}
	}

	// We override the default Marshaller to enable the `UseProtoNames` option.
	// We do this is because the default JSONPb in 2.5.0 marshals proto structs using
	// `camelCase`, while all the JSON annotations are `under_score`.
	// Our protobuf files follow the convention described here
	// https://developers.google.com/protocol-buffers/docs/style#message-and-field-names
	// Camel case breaks unmarshalling our GRPC gateway responses with protobuf structs.
	gateway := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{
			MarshalOptions: protojson.MarshalOptions{
				UseProtoNames:   true,
				EmitUnpopulated: true,
			},
			UnmarshalOptions: protojson.UnmarshalOptions{
				DiscardUnknown: true,
			},
		}),
	)

	// Set up an JSON Gateway API for our GRPC methods
	var gwCtx context.Context
	gwCtx, s.gwCancel = context.WithCancel(context.Background())
	err = RegisterV1HandlerFromEndpoint(gwCtx, gateway, gatewayAddr,
		[]grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())})
	if err != nil {
		return errors.Wrap(err, "while registering GRPC gateway handler")
	}

	// Serve the JSON Gateway and metrics handlers via standard HTTP/1
	mux := http.NewServeMux()

	// Optionally collect process metrics
	if s.conf.MetricFlags.Has(FlagOSMetrics) {
		s.log.Debug("Collecting OS Metrics")
		s.promRegister.MustRegister(collectors.NewProcessCollector(
			collectors.ProcessCollectorOpts{Namespace: "gubernator"},
		))
	}

	// Optionally collect golang internal metrics
	if s.conf.MetricFlags.Has(FlagGolangMetrics) {
		s.log.Debug("Collecting Golang Metrics")
		s.promRegister.MustRegister(collectors.NewGoCollector())
	}

	mux.Handle("/metrics", promhttp.InstrumentMetricHandler(
		s.promRegister, promhttp.HandlerFor(s.promRegister, promhttp.HandlerOpts{}),
	))
	mux.Handle("/", gateway)
	s.logWriter = newLogWriter(s.log)
	log := log.New(s.logWriter, "", 0)
	s.httpSrv = &http.Server{Addr: s.conf.HTTPListenAddress, Handler: mux, ErrorLog: log}

	s.HTTPListener, err = net.Listen("tcp", s.conf.HTTPListenAddress)
	if err != nil {
		return errors.Wrap(err, "while starting HTTP listener")
	}

	httpListenerAddr := s.HTTPListener.Addr().String()
	addrs := []string{httpListenerAddr}

	if s.conf.ServerTLS() != nil {

		// If configured, start another listener at configured address and server only
		// /v1/HealthCheck while not requesting or verifying client certificate.
		if s.conf.HTTPStatusListenAddress != "" {
			muxNoMTLS := http.NewServeMux()
			muxNoMTLS.Handle("/v1/HealthCheck", gateway)
			s.httpSrvNoMTLS = &http.Server{
				Addr:      s.conf.HTTPStatusListenAddress,
				Handler:   muxNoMTLS,
				ErrorLog:  log,
				TLSConfig: s.conf.ServerTLS().Clone(),
			}
			s.httpSrvNoMTLS.TLSConfig.ClientAuth = tls.NoClientCert
			httpListener, err := net.Listen("tcp", s.conf.HTTPStatusListenAddress)
			if err != nil {
				return errors.Wrap(err, "while starting HTTP listener for health metric")
			}
			httpAddr := httpListener.Addr().String()
			addrs = append(addrs, httpAddr)
			s.wg.Go(func() {
				s.log.Infof("HTTPS Status Handler Listening on %s ...", httpAddr)
				if err := s.httpSrvNoMTLS.ServeTLS(httpListener, "", ""); err != nil {
					if !errors.Is(err, http.ErrServerClosed) {
						s.log.WithError(err).Error("while starting TLS Status HTTP server")
					}
				}
			})
		}

		// This is to avoid any race conditions that might occur
		// since the tls config is a shared pointer.
		s.httpSrv.TLSConfig = s.conf.ServerTLS().Clone()
		s.wg.Go(func() {
			s.log.Infof("HTTPS Gateway Listening on %s ...", httpListenerAddr)
			if err := s.httpSrv.ServeTLS(s.HTTPListener, "", ""); err != nil {
				if !errors.Is(err, http.ErrServerClosed) {
					s.log.WithError(err).Error("while starting TLS HTTP server")
				}
			}
		})
	} else {
		s.wg.Go(func() {
			s.log.Infof("HTTP Gateway Listening on %s ...", httpListenerAddr)
			if err := s.httpSrv.Serve(s.HTTPListener); err != nil {
				if !errors.Is(err, http.ErrServerClosed) {
					s.log.WithError(err).Error("while starting HTTP server")
				}
			}
		})
	}

	// Validate we can reach the GRPC and HTTP endpoints before returning
	for _, l := range s.GRPCListeners {
		addrs = append(addrs, l.Addr().String())
	}
	if err := WaitForConnect(ctx, addrs); err != nil {
		return err
	}

	return nil
}

// Close gracefully closes all server connections and listening sockets
func (s *Daemon) Close() {
	if s.httpSrv == nil && s.httpSrvNoMTLS == nil {
		return
	}

	if s.pool != nil {
		s.pool.Close()
	}

	s.log.Infof("HTTP Gateway close for %s ...", s.conf.HTTPListenAddress)
	_ = s.httpSrv.Shutdown(context.Background())
	if s.httpSrvNoMTLS != nil {
		s.log.Infof("HTTP Status Gateway close for %s ...", s.conf.HTTPStatusListenAddress)
		_ = s.httpSrvNoMTLS.Shutdown(context.Background())
	}
	for i, srv := range s.grpcSrvs {
		s.log.Infof("GRPC close for %s ...", s.GRPCListeners[i].Addr())
		srv.GracefulStop()
	}
	s.logWriter.Close()
	_ = s.V1Server.Close()
	s.wg.Stop()
	s.statsHandler.Close()
	s.gwCancel()
	s.httpSrv = nil
	s.httpSrvNoMTLS = nil
	s.grpcSrvs = nil
}

// SetPeers sets the peers for this daemon
func (s *Daemon) SetPeers(in []PeerInfo) {
	peers := make([]PeerInfo, len(in))
	copy(peers, in)

	for i, p := range peers {
		if s.conf.GRPCListenAddress == p.GRPCAddress {
			peers[i].IsOwner = true
		}
	}
	s.V1Server.SetPeers(peers)
}

// Config returns the current config for this Daemon
func (s *Daemon) Config() DaemonConfig {
	return s.conf
}

// Peers returns the peers this daemon knows about
func (s *Daemon) Peers() []PeerInfo {
	var peers []PeerInfo
	for _, client := range s.V1Server.GetPeerList() {
		peers = append(peers, client.Info())
	}
	return peers
}

func (s *Daemon) MustClient() V1Client {
	c, err := s.Client()
	if err != nil {
		panic(fmt.Sprintf("[%s] failed to init daemon client - '%s'", s.InstanceID, err))
	}
	return c
}

func (s *Daemon) Client() (V1Client, error) {
	if s.client != nil {
		return s.client, nil
	}

	conn, err := grpc.DialContext(context.Background(),
		fmt.Sprintf("static:///%s", s.PeerInfo.GRPCAddress),
		grpc.WithResolvers(NewStaticBuilder()),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	s.client = NewV1Client(conn)
	return s.client, nil
}

// WaitForConnect returns nil if the list of addresses is listening
// for connections; will block until context is cancelled.
func WaitForConnect(ctx context.Context, addresses []string) error {
	var d net.Dialer
	var errs []error
	for {
		errs = nil
		for _, addr := range addresses {
			if addr == "" {
				continue
			}

			// TODO: golang 1.15.3 introduces tls.DialContext(). When we are ready to drop
			//  support for older versions we can detect tls and use the tls.DialContext to
			//  avoid the `http: TLS handshake error` we get when using TLS.
			conn, err := d.DialContext(ctx, "tcp", addr)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			_ = conn.Close()
		}

		if len(errs) == 0 {
			break
		}

		<-ctx.Done()
		return ctx.Err()
	}

	if len(errs) != 0 {
		var errStrings []string
		for _, err := range errs {
			errStrings = append(errStrings, err.Error())
		}
		return errors.New(strings.Join(errStrings, "\n"))
	}
	return nil
}
