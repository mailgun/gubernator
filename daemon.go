/*
Copyright 2018-2020 Mailgun Technologies Inc

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
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/mailgun/holster/v3/etcdutil"
	"github.com/mailgun/holster/v3/setter"
	"github.com/mailgun/holster/v3/syncutil"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type Daemon struct {
	GRPCListener net.Listener
	HTTPListener net.Listener
	V1Server     *V1Instance

	log          logrus.FieldLogger
	pool         PoolInterface
	conf         DaemonConfig
	httpSrv      *http.Server
	grpcSrv      *grpc.Server
	wg           syncutil.WaitGroup
	statsHandler *GRPCStatsHandler
	promRegister *prometheus.Registry
	gwCancel     context.CancelFunc
}

// SpawnDaemon starts a new gubernator daemon according to the provided DaemonConfig.
// This function will block until the daemon responds to connections as specified
// by GRPCListenAddress and HTTPListenAddress
func SpawnDaemon(ctx context.Context, conf DaemonConfig) (*Daemon, error) {
	s := Daemon{
		log:  conf.Logger,
		conf: conf,
	}
	setter.SetDefault(&s.log, logrus.WithField("category", "gubernator"))

	if err := s.Start(ctx); err != nil {
		return nil, err
	}
	return &s, nil
}

func (s *Daemon) Start(ctx context.Context) error {
	var err error

	// The LRU cache we store rate limits in
	cache := NewLRUCache(s.conf.CacheSize)

	// cache also implements prometheus.Collector interface
	s.promRegister = prometheus.NewRegistry()
	s.promRegister.Register(cache)

	// Handler to collect duration and API access metrics for GRPC
	s.statsHandler = NewGRPCStatsHandler()
	s.promRegister.Register(s.statsHandler)

	opts := []grpc.ServerOption{
		grpc.StatsHandler(s.statsHandler),
		grpc.MaxRecvMsgSize(1024 * 1024),
	}

	if err := SetupTLS(s.conf.TLS); err != nil {
		return err
	}

	if s.conf.ServerTLS() != nil {
		creds := credentials.NewTLS(s.conf.ServerTLS())
		opts = append(opts, grpc.Creds(creds))
	}

	// New GRPC server
	s.grpcSrv = grpc.NewServer(opts...)

	// Registers a new gubernator instance with the GRPC server
	s.V1Server, err = NewV1Instance(Config{
		PeerTLS:     s.conf.ClientTLS(),
		DataCenter:  s.conf.DataCenter,
		LocalPicker: s.conf.Picker,
		GRPCServer:  s.grpcSrv,
		Logger:      s.log,
		Cache:       cache,
	})
	if err != nil {
		return errors.Wrap(err, "while creating new gubernator instance")
	}

	// V1Server instance also implements prometheus.Collector interface
	s.promRegister.Register(s.V1Server)

	s.GRPCListener, err = net.Listen("tcp", s.conf.GRPCListenAddress)
	if err != nil {
		return errors.Wrap(err, "while starting GRPC listener")
	}

	// Start serving GRPC Requests
	s.wg.Go(func() {
		s.log.Infof("GRPC Listening on %s ...", s.conf.GRPCListenAddress)
		if err := s.grpcSrv.Serve(s.GRPCListener); err != nil {
			s.log.WithError(err).Error("while starting GRPC server")
		}
	})

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
	case "member-list":
		s.conf.MemberListPoolConf.OnUpdate = s.V1Server.SetPeers
		s.conf.MemberListPoolConf.Logger = s.log

		// Register peer on member list
		s.pool, err = NewMemberListPool(ctx, s.conf.MemberListPoolConf)
		if err != nil {
			return errors.Wrap(err, "while creating member list pool")
		}
	}

	// Setup an JSON Gateway API for our GRPC methods
	gateway := runtime.NewServeMux()
	var gwCtx context.Context
	gwCtx, s.gwCancel = context.WithCancel(context.Background())
	err = RegisterV1HandlerFromEndpoint(gwCtx, gateway,
		s.conf.GRPCListenAddress, []grpc.DialOption{grpc.WithInsecure()})
	if err != nil {
		return errors.Wrap(err, "while registering GRPC gateway handler")
	}

	// Serve the JSON Gateway and metrics handlers via standard HTTP/1
	mux := http.NewServeMux()

	mux.Handle("/metrics", promhttp.InstrumentMetricHandler(
		s.promRegister, promhttp.HandlerFor(s.promRegister, promhttp.HandlerOpts{}),
	))
	mux.Handle("/", gateway)
	log := log.New(newLogWriter(s.log), "", 0)
	s.httpSrv = &http.Server{Addr: s.conf.HTTPListenAddress, Handler: mux, ErrorLog: log}

	s.HTTPListener, err = net.Listen("tcp", s.conf.HTTPListenAddress)
	if err != nil {
		return errors.Wrap(err, "while starting HTTP listener")
	}

	if s.conf.ServerTLS() != nil {
		// This is to avoid any race conditions that might occur
		// since the tls config is a shared pointer.
		s.httpSrv.TLSConfig = s.conf.ServerTLS().Clone()
		s.wg.Go(func() {
			s.log.Infof("HTTPS Gateway Listening on %s ...", s.conf.HTTPListenAddress)
			if err := s.httpSrv.ServeTLS(s.HTTPListener, "", ""); err != nil {
				if err != http.ErrServerClosed {
					s.log.WithError(err).Error("while starting TLS HTTP server")
				}
			}
		})
	} else {
		s.wg.Go(func() {
			s.log.Infof("HTTP Gateway Listening on %s ...", s.conf.HTTPListenAddress)
			if err := s.httpSrv.Serve(s.HTTPListener); err != nil {
				if err != http.ErrServerClosed {
					s.log.WithError(err).Error("while starting HTTP server")
				}
			}
		})
	}

	// Validate we can reach the GRPC and HTTP endpoints before returning
	if err := WaitForConnect(ctx, []string{s.conf.HTTPListenAddress, s.conf.GRPCListenAddress}); err != nil {
		return err
	}

	return nil
}

// Close gracefully closes all server connections and listening sockets
func (s *Daemon) Close() {
	if s.httpSrv == nil {
		return
	}

	if s.pool != nil {
		s.pool.Close()
	}

	s.log.Infof("HTTP Gateway close for %s ...", s.conf.HTTPListenAddress)
	s.httpSrv.Shutdown(context.Background())
	s.log.Infof("GRPC close for %s ...", s.conf.GRPCListenAddress)
	s.grpcSrv.GracefulStop()
	s.wg.Stop()
	s.statsHandler.Close()
	s.gwCancel()
	s.httpSrv = nil
	s.grpcSrv = nil
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

			// TODO: golang 15.3 introduces tls.DialContext(). When we are ready to drop
			//  support for older versions we can detect tls and use the tls.DialContext to
			//  avoid the `http: TLS handshake error` we get when using TLS.
			conn, err := d.DialContext(ctx, "tcp", addr)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			conn.Close()
		}

		if len(errs) == 0 {
			break
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		}
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
