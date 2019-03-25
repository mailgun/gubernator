package gubernator

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/runtime"
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
	server   *http.Server
	conf     HTTPConfig
}

func NewHTTPServer(conf HTTPConfig) (*HTTPServer, error) {
	listener, err := net.Listen("tcp", conf.ListenAddress)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to listen on %s", conf.ListenAddress)
	}

	s := HTTPServer{
		log:      logrus.WithField("category", "http"),
		listener: listener,
		conf:     conf,
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())

	// Fetch the listener address if none was set
	holster.SetDefault(&s.conf.ListenAddress, s.Address())

	mux := http.NewServeMux()
	gateway := runtime.NewServeMux()
	err = RegisterRateLimitServiceV1HandlerFromEndpoint(s.ctx, gateway,
		conf.GubernatorAddress, []grpc.DialOption{grpc.WithInsecure()})
	if err != nil {
		return nil, errors.Wrap(err, "while registering GRPC gateway handler")
	}

	mux.Handle("/", gateway)
	s.server = &http.Server{Addr: conf.ListenAddress, Handler: mux}

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
