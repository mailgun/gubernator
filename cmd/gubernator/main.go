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

package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"runtime"

	"github.com/mailgun/gubernator"
	"github.com/mailgun/holster/v3/clock"
	"github.com/sirupsen/logrus"
	"k8s.io/klog"
)

var log = logrus.WithField("category", "gubernator")
var Version = "dev-build"

func main() {
	var configFile string
	var err error

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

	// Read our config from the environment or optional environment config file
	conf, err := gubernator.SetupDaemonConfig(logrus.StandardLogger(), configFile)
	checkErr(err, "while getting config")

	ctx, cancel := context.WithTimeout(context.Background(), clock.Second*10)
	defer cancel()

	// Start the daemon
	daemon, err := gubernator.SpawnDaemon(ctx, conf)
	checkErr(err, "while spawning daemon")

	// Wait here for signals to clean up our mess
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	for sig := range c {
		if sig == os.Interrupt {
			log.Info("caught interrupt; user requested premature exit")
			daemon.Close()
			os.Exit(0)
		}
	}
}

func checkErr(err error, msg string) {
	if err != nil {
		log.WithError(err).Error(msg)
		os.Exit(1)
	}
}
