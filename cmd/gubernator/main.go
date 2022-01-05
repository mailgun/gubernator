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

package main

import (
	"context"
	"flag"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/mailgun/gubernator/v2"
	"github.com/mailgun/gubernator/v2/tracing"
	"github.com/mailgun/holster/v4/clock"
	"github.com/opentracing/opentracing-go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	jaegerConfig "github.com/uber/jaeger-client-go/config"
	"k8s.io/klog"
)

var log = logrus.WithField("category", "gubernator")
var Version = "dev-build"
var tracerCloser io.Closer

func main() {
	var configFile string

	logrus.Infof("Gubernator %s (%s/%s)", Version, runtime.GOARCH, runtime.GOOS)
	flags := flag.NewFlagSet("gubernator", flag.ContinueOnError)
	flags.StringVar(&configFile, "config", "", "environment config file")
	flags.BoolVar(&gubernator.DebugEnabled, "debug", false, "enable debug")
	checkErr(flags.Parse(os.Args[1:]), "while parsing flags")

	// in order to prevent logging to /tmp by k8s.io/client-go
	// and other kubernetes related dependencies which are using
	// klog (https://github.com/kubernetes/klog), we need to
	// initialize klog in the way it prints to stderr only.
	klog.InitFlags(nil)
	flag.Set("logtostderr", "true")

	err := initTracing()
	if err != nil {
		log.WithError(err).Warn("Error in initTracing")
	}

	// Read our config from the environment or optional environment config file
	conf, err := gubernator.SetupDaemonConfig(logrus.StandardLogger(), configFile)
	checkErr(err, "while getting config")

	ctx, cancel := tracing.ContextWithTimeout(context.Background(), clock.Second*10)
	defer cancel()

	// Start the daemon
	daemon, err := gubernator.SpawnDaemon(ctx, conf)
	checkErr(err, "while spawning daemon")
	cancel()

	// Wait here for signals to clean up our mess
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	for range c {
		log.Info("caught signal; shutting down")
		daemon.Close()
		exit(0)
	}
}

func checkErr(err error, msg string) {
	if err != nil {
		log.WithError(err).Error(msg)
		exit(1)
	}
}

func exit(code int) {
	if tracerCloser != nil {
		tracerCloser.Close()
	}
	os.Exit(code)
}

// Configure tracer and set as global tracer.
// Be sure to call closer.Close() on application exit.
func initTracing() error {
	// Configure new tracer.
	cfg, err := jaegerConfig.FromEnv()
	if err != nil {
		return errors.Wrap(err, "Error in jaeger.FromEnv()")
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "gubernator"
	}

	var tracer opentracing.Tracer

	tracer, tracerCloser, err = cfg.NewTracer()
	if err != nil {
		return errors.Wrap(err, "Error in cfg.NewTracer")
	}

	// Set as global tracer.
	opentracing.SetGlobalTracer(tracer)

	return nil
}
