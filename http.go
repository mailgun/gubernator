package gubernator

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/mailgun/gubernator/pb"
	"github.com/mailgun/holster"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

type HTTPServer struct {
	cancel   context.CancelFunc
	wg       holster.WaitGroup
	ctx      context.Context
	log      *logrus.Entry
	listener net.Listener
	conf     ServerConfig
	server   *http.Server
}

func NewHTTPServer(grpcSrv *GRPCServer, conf ServerConfig) (*HTTPServer, error) {
	listener, err := net.Listen("tcp", conf.HTTPListenAddress)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to listen on %s", conf.HTTPListenAddress)
	}

	s := HTTPServer{
		log:      logrus.WithField("category", "http"),
		listener: listener,
		conf:     conf,
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())

	// Fetch the listener address if none was set
	holster.SetDefault(&s.conf.HTTPListenAddress, listener.Addr())

	mux := http.NewServeMux()
	gateway := runtime.NewServeMux()
	err = pb.RegisterRateLimitServiceHandlerFromEndpoint(s.ctx, gateway,
		conf.GRPCListenAddress, []grpc.DialOption{grpc.WithInsecure()})
	if err != nil {
		return nil, err
	}

	mux.Handle("/", gateway)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		resp, err := grpcSrv.HealthCheck(r.Context(), &pb.HealthCheckRequest{})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		toJSON(w, resp)
	})
	mux.HandleFunc("/_ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("pong"))
	})

	s.server = &http.Server{Addr: conf.HTTPListenAddress, Handler: mux}

	return &s, nil
}

func (s *HTTPServer) Start() error {
	// Start the HTTP server
	errs := make(chan error, 1)
	s.wg.Go(func() {
		// After Shutdown or Close, the returned error is ErrServerClosed.
		errs <- s.server.Serve(s.listener)
	})

	// Ensure the server is running before we return
	go func() {
		errs <- retry(2, time.Millisecond*500, func() error {
			_, err := http.Get("http://" + s.Address() + "/v1/HealthCheck")
			return err
		})
	}()

	// Wait until the server starts or errors
	err := <-errs
	if err != nil {
		return errors.Wrap(err, "while waiting for server to pass health check")
	}

	s.log.Infof("HTTP Listening on %s ...", s.Address())
	return nil
}

func (s *HTTPServer) Address() string {
	return s.listener.Addr().String()
}

func (s *HTTPServer) Stop() {
	if err := s.server.Shutdown(s.ctx); err != nil {
		s.log.WithError(err).Error("during shutdown")
	}
	s.cancel()
	s.wg.Wait()
}

func toJSON(w http.ResponseWriter, obj interface{}) {
	resp, err := json.Marshal(obj)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}
