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

	gubernator "github.com/mailgun/gubernator/v2"
	"github.com/mailgun/holster/v4/clock"
	"github.com/mailgun/holster/v4/tracing"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
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
	_ = flag.Set("logtostderr", "true")

	res, err := tracing.NewResource("gubernator", Version, resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceInstanceID(gubernator.GetInstanceID()),
	))
	if err != nil {
		log.WithError(err).Fatal("during tracing.NewResource()")
	}

	// Initialize tracing.
	ctx := context.Background()
	err = tracing.InitTracing(ctx,
		"github.com/mailgun/gubernator/v2",
		tracing.WithLevel(gubernator.GetTracingLevel()),
		tracing.WithResource(res),
	)
	if err != nil {
		log.WithError(err).Fatal("during tracing.InitTracing()")
	}

	// Read our config from the environment or optional environment config file
	conf, err := gubernator.SetupDaemonConfig(logrus.StandardLogger(), configFile)
	checkErr(err, "while getting config")

	ctx, cancel := context.WithTimeout(ctx, clock.Second*10)

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
		_ = tracing.CloseTracing(context.Background())
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
