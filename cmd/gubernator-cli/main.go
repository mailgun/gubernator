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
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/davecgh/go-spew/spew"
	guber "github.com/mailgun/gubernator/v3"
	"github.com/mailgun/holster/v4/clock"
	"github.com/mailgun/holster/v4/setter"
	"github.com/mailgun/holster/v4/syncutil"
	"github.com/mailgun/holster/v4/tracing"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/time/rate"
)

var (
	log                     *logrus.Logger
	configFile, httpAddress string
	concurrency             uint64
	timeout                 time.Duration
	checksPerRequest        uint64
	reqRate                 float64
	quiet                   bool
)

func main() {
	log = logrus.StandardLogger()
	flag.StringVar(&configFile, "config", "", "Environment config file")
	flag.StringVar(&httpAddress, "e", "", "Gubernator HTTP endpoint address")
	flag.Uint64Var(&concurrency, "concurrency", 1, "Concurrent threads (default 1)")
	flag.DurationVar(&timeout, "timeout", 100*time.Millisecond, "Request timeout (default 100ms)")
	flag.Uint64Var(&checksPerRequest, "checks", 1, "Rate checks per request (default 1)")
	flag.Float64Var(&reqRate, "rate", 0, "Request rate overall, 0 = no rate limit")
	flag.BoolVar(&quiet, "q", false, "Quiet logging")
	flag.Parse()

	if quiet {
		log.SetLevel(logrus.ErrorLevel)
	}

	// Initialize tracing.
	res, err := tracing.NewResource("gubernator-cli", "")
	if err != nil {
		log.WithError(err).Fatal("Error in tracing.NewResource")
	}
	ctx := context.Background()
	err = tracing.InitTracing(ctx,
		"github.com/mailgun/gubernator/v2/cmd/gubernator-cli",
		tracing.WithResource(res),
	)
	if err != nil {
		log.WithError(err).Warn("Error in tracing.InitTracing")
	}

	// Print startup message.
	argsMsg := fmt.Sprintf("Command line: %s", strings.Join(os.Args[1:], " "))
	log.Info(argsMsg)

	var client guber.Client
	// Print startup message.
	cmdLine := strings.Join(os.Args[1:], " ")
	logrus.WithContext(ctx).Info("Command line: " + cmdLine)

	conf, err := guber.SetupDaemonConfig(log, configFile)
	checkErr(err)
	setter.SetOverride(&conf.HTTPListenAddress, httpAddress)

	if configFile == "" && httpAddress == "" && os.Getenv("GUBER_HTTP_ADDRESS") == "" {
		log.Fatal("please provide a endpoint via -e or from a config " +
			"file via -config or set the env GUBER_HTTP_ADDRESS")
	}

	err = guber.SetupTLS(conf.TLS)
	checkErr(err)

	log.WithContext(ctx).Infof("Connecting to '%s'...", conf.HTTPListenAddress)
	client, err = guber.NewClient(guber.WithDaemonConfig(conf, conf.HTTPListenAddress))
	checkErr(err)

	// Generate a selection of rate limits with random limits.
	var rateLimits []*guber.RateLimitRequest

	for i := 0; i < 2000; i++ {
		rateLimits = append(rateLimits, &guber.RateLimitRequest{
			Name:      fmt.Sprintf("gubernator-cli-%d", i),
			UniqueKey: guber.RandomString(10),
			Hits:      1,
			Limit:     int64(randInt(1, 1000)),
			Duration:  int64(randInt(int(clock.Millisecond*500), int(clock.Second*6))),
			Behavior:  guber.Behavior_BATCHING,
			Algorithm: guber.Algorithm_TOKEN_BUCKET,
		})
	}

	fan := syncutil.NewFanOut(int(concurrency))
	var limiter *rate.Limiter
	if reqRate > 0 {
		l := rate.Limit(reqRate)
		log.WithField("reqRate", reqRate).Info("")
		limiter = rate.NewLimiter(l, 1)
	}

	// Replay requests in endless loop.
	for {
		for i := int(0); i < len(rateLimits); i += int(checksPerRequest) {
			req := &guber.CheckRateLimitsRequest{
				Requests: rateLimits[i:min(i+int(checksPerRequest), len(rateLimits))],
			}

			fan.Run(func(obj interface{}) error {
				req := obj.(*guber.CheckRateLimitsRequest)

				if reqRate > 0 {
					_ = limiter.Wait(ctx)
				}

				sendRequest(ctx, client, req)

				return nil
			}, req)
		}
	}
}

func min(a, b int) int {
	if a <= b {
		return a
	}
	return b
}

func checkErr(err error) {
	if err != nil {
		log.Fatalf(err.Error())
	}
}

func randInt(min, max int) int {
	return rand.Intn(max-min) + min
}

func sendRequest(ctx context.Context, client guber.Client, req *guber.CheckRateLimitsRequest) {
	ctx = tracing.StartScope(ctx)
	defer tracing.EndScope(ctx, nil)

	ctx, cancel := context.WithTimeout(ctx, timeout)

	// Now hit our cluster with the rate limits
	var resp guber.CheckRateLimitsResponse
	err := client.CheckRateLimits(ctx, req, &resp)
	cancel()
	if err != nil {
		log.WithContext(ctx).WithError(err).Error("Error in client.CheckRateLimits")
		return
	}

	// Sanity check
	if resp.Responses == nil {
		log.WithContext(ctx).Error("Responses array is unexpectedly nil")
		return
	}

	// Check for over limit response.
	overLimit := false

	for itemNum, resp := range resp.Responses {
		if resp.Status == guber.Status_OVER_LIMIT {
			overLimit = true
			log.WithContext(ctx).WithField("name", req.Requests[itemNum].Name).
				Info("Overlimit!")
		}
	}

	if overLimit {
		span := trace.SpanFromContext(ctx)
		span.SetAttributes(
			attribute.Bool("overlimit", true),
		)

		if !quiet {
			dumpResp := spew.Sdump(&resp)
			log.WithContext(ctx).Info(dumpResp)
		}
	}
}
