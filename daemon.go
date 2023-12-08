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
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mailgun/errors"
	"github.com/mailgun/holster/v4/etcdutil"
	"github.com/mailgun/holster/v4/setter"
	"github.com/mailgun/holster/v4/syncutil"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"golang.org/x/net/proxy"
)

type Daemon struct {
	wg             syncutil.WaitGroup
	httpServers    []*http.Server
	pool           PoolInterface
	conf           DaemonConfig
	Listener       net.Listener
	HealthListener net.Listener
	log            FieldLogger
	Service        *Service
}

// SpawnDaemon starts a new gubernator daemon according to the provided DaemonConfig.
// This function will block until the daemon responds to connections to HTTPListenAddress
func SpawnDaemon(ctx context.Context, conf DaemonConfig) (*Daemon, error) {
	s := &Daemon{
		log:  conf.Logger,
		conf: conf,
	}
	return s, s.Start(ctx)
}

func (s *Daemon) Start(ctx context.Context) error {
	var err error

	// TODO: Then setup benchmarks and go trace

	setter.SetDefault(&s.log, logrus.WithFields(logrus.Fields{
		"service-id": s.conf.InstanceID,
		"category":   "gubernator",
	}))

	registry := prometheus.NewRegistry()

	// The LRU cache for storing rate limits.
	cacheCollector := NewLRUCacheCollector()
	registry.MustRegister(cacheCollector)

	if err := SetupTLS(s.conf.TLS); err != nil {
		return err
	}

	s.Service, err = NewService(Config{
		PeerClientFactory: func(info PeerInfo) PeerClient {
			return NewPeerClient(WithDaemonConfig(s.conf, info.HTTPAddress))
		},
		CacheFactory: func(maxSize int) Cache {
			cache := NewLRUCache(maxSize)
			cacheCollector.AddCache(cache)
			return cache
		},
		DataCenter:  s.conf.DataCenter,
		CacheSize:   s.conf.CacheSize,
		Behaviors:   s.conf.Behaviors,
		Workers:     s.conf.Workers,
		LocalPicker: s.conf.Picker,
		Loader:      s.conf.Loader,
		Store:       s.conf.Store,
		Logger:      s.log,
	})
	if err != nil {
		return errors.Wrap(err, "while creating new gubernator service")
	}

	// Service implements prometheus.Collector interface
	registry.MustRegister(s.Service)

	switch s.conf.PeerDiscoveryType {
	case "k8s":
		// Source our list of peers from kubernetes endpoint API
		s.conf.K8PoolConf.OnUpdate = s.Service.SetPeers
		s.pool, err = NewK8sPool(s.conf.K8PoolConf)
		if err != nil {
			return errors.Wrap(err, "while querying kubernetes API")
		}
	case "etcd":
		s.conf.EtcdPoolConf.OnUpdate = s.Service.SetPeers
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
		s.conf.DNSPoolConf.OnUpdate = s.Service.SetPeers
		s.pool, err = NewDNSPool(s.conf.DNSPoolConf)
		if err != nil {
			return errors.Wrap(err, "while creating the DNS pool")
		}
	case "member-list":
		s.conf.MemberListPoolConf.OnUpdate = s.Service.SetPeers
		s.conf.MemberListPoolConf.Logger = s.log

		// Register peer on the member list
		s.pool, err = NewMemberListPool(ctx, s.conf.MemberListPoolConf)
		if err != nil {
			return errors.Wrap(err, "while creating member list pool")
		}
	}

	// Optionally collect process metrics
	if s.conf.MetricFlags.Has(FlagOSMetrics) {
		s.log.Debug("Collecting OS Metrics")
		registry.MustRegister(collectors.NewProcessCollector(
			collectors.ProcessCollectorOpts{Namespace: "gubernator"},
		))
	}

	// Optionally collect golang internal metrics
	if s.conf.MetricFlags.Has(FlagGolangMetrics) {
		s.log.Debug("Collecting Golang Metrics")
		registry.MustRegister(collectors.NewGoCollector())
	}

	handler := NewHandler(s.Service, promhttp.InstrumentMetricHandler(
		registry, promhttp.HandlerFor(registry, promhttp.HandlerOpts{}),
	))
	registry.MustRegister(handler)

	if s.conf.ServerTLS() != nil {
		if err := s.spawnHTTPS(ctx, handler); err != nil {
			return err
		}
		if s.conf.HTTPStatusListenAddress != "" {
			if err := s.spawnHTTPHealthCheck(ctx, handler, registry); err != nil {
				return err
			}
		}
	} else {
		if err := s.spawnHTTP(ctx, handler); err != nil {
			return err
		}
	}
	return nil
}

// spawnHTTPHealthCheck spawns a plan HTTP listener for use by orchestration systems to preform health checks and
// collect metrics when TLS and client certs are in use.
func (s *Daemon) spawnHTTPHealthCheck(ctx context.Context, h *Handler, r *prometheus.Registry) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", h.HealthZ)
	mux.Handle("/metrics", promhttp.InstrumentMetricHandler(
		r, promhttp.HandlerFor(r, promhttp.HandlerOpts{}),
	))

	srv := &http.Server{
		ErrorLog:  log.New(newLogWriter(s.log), "", 0),
		Addr:      s.conf.HTTPStatusListenAddress,
		TLSConfig: s.conf.ServerTLS().Clone(),
		Handler:   mux,
	}

	srv.TLSConfig.ClientAuth = tls.NoClientCert
	var err error
	s.HealthListener, err = net.Listen("tcp", s.conf.HTTPStatusListenAddress)
	if err != nil {
		return errors.Wrap(err, "while starting HTTP listener for health metric")
	}

	s.wg.Go(func() {
		s.log.Infof("HTTPS Health Check Listening on %s ...", s.conf.HTTPStatusListenAddress)
		if err := srv.ServeTLS(s.HealthListener, "", ""); err != nil {
			if !errors.Is(err, http.ErrServerClosed) {
				s.log.WithError(err).Error("while starting TLS Status HTTP server")
			}
		}
	})

	if err := WaitForConnect(ctx, s.HealthListener.Addr().String(), nil); err != nil {
		return err
	}

	s.httpServers = append(s.httpServers, srv)

	return nil
}

func (s *Daemon) spawnHTTPS(ctx context.Context, mux http.Handler) error {
	srv := &http.Server{
		ErrorLog:  log.New(newLogWriter(s.log), "", 0),
		TLSConfig: s.conf.ServerTLS().Clone(),
		Addr:      s.conf.HTTPListenAddress,
		Handler:   mux,
	}

	var err error
	s.Listener, err = net.Listen("tcp", s.conf.HTTPListenAddress)
	if err != nil {
		return errors.Wrap(err, "while starting HTTPS listener")
	}

	s.wg.Go(func() {
		s.log.Infof("HTTPS Listening on %s ...", s.conf.HTTPListenAddress)
		if err := srv.ServeTLS(s.Listener, "", ""); err != nil {
			if !errors.Is(err, http.ErrServerClosed) {
				s.log.WithError(err).Error("while starting TLS HTTP server")
			}
		}
	})

	if err := WaitForConnect(ctx, s.Listener.Addr().String(), s.conf.ClientTLS()); err != nil {
		return err
	}

	s.httpServers = append(s.httpServers, srv)

	return nil
}

func (s *Daemon) spawnHTTP(ctx context.Context, h http.Handler) error {
	// Support H2C (HTTP/2 ClearText)
	// See https://github.com/thrawn01/h2c-golang-example
	h2s := &http2.Server{}

	srv := &http.Server{
		ErrorLog: log.New(newLogWriter(s.log), "", 0),
		Addr:     s.conf.HTTPListenAddress,
		Handler:  h2c.NewHandler(h, h2s),
	}

	var err error
	s.Listener, err = net.Listen("tcp", s.conf.HTTPListenAddress)
	if err != nil {
		return errors.Wrap(err, "while starting HTTP listener")
	}

	s.wg.Go(func() {
		s.log.Infof("HTTP Listening on %s ...", s.conf.HTTPListenAddress)
		if err := srv.Serve(s.Listener); err != nil {
			if !errors.Is(err, http.ErrServerClosed) {
				s.log.WithError(err).Error("while starting HTTP server")
			}
		}
	})

	if err := WaitForConnect(ctx, s.Listener.Addr().String(), nil); err != nil {
		return err
	}

	s.httpServers = append(s.httpServers, srv)

	return nil
}

// Close gracefully closes all server connections and listening sockets
func (s *Daemon) Close(ctx context.Context) error {
	if len(s.httpServers) == 0 {
		return nil
	}

	if s.pool != nil {
		s.pool.Close()
	}

	for _, srv := range s.httpServers {
		s.log.Infof("Shutting down server %s ...", srv.Addr)
		_ = srv.Shutdown(ctx)
	}

	if err := s.Service.Close(ctx); err != nil {
		return err
	}

	s.wg.Stop()
	s.httpServers = nil
	return nil
}

// SetPeers sets the peers for this daemon
func (s *Daemon) SetPeers(in []PeerInfo) {
	peers := make([]PeerInfo, len(in))
	copy(peers, in)

	for i, p := range peers {
		if s.conf.AdvertiseAddress == p.HTTPAddress {
			peers[i].IsOwner = true
		}
	}
	s.Service.SetPeers(peers)
}

// Config returns the current config for this Daemon
func (s *Daemon) Config() DaemonConfig {
	return s.conf
}

// Peers returns the peers this daemon knows about
func (s *Daemon) Peers() []PeerInfo {
	var peers []PeerInfo
	for _, client := range s.Service.GetPeerList() {
		peers = append(peers, client.Info())
	}
	return peers
}

// WaitForConnect waits until the passed address is accepting connections.
// It will continue to attempt a connection until context is canceled.
func WaitForConnect(ctx context.Context, address string, cfg *tls.Config) error {
	if address == "" {
		return fmt.Errorf("WaitForConnect() requires a valid address")
	}

	var errs []string
	for {
		var d proxy.ContextDialer
		if cfg != nil {
			d = &tls.Dialer{Config: cfg}
		} else {
			d = &net.Dialer{}
		}
		conn, err := d.DialContext(ctx, "tcp", address)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		errs = append(errs, err.Error())
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err().Error())
			return errors.New(strings.Join(errs, "\n"))
		}
		time.Sleep(time.Millisecond * 100)
		continue
	}
}
