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
	guber "github.com/mailgun/gubernator/v2"
	"github.com/mailgun/holster/v4/clock"
	"github.com/mailgun/holster/v4/ctxutil"
	"github.com/mailgun/holster/v4/errors"
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
	configFile, grpcAddress string
	concurrency             uint64
	timeout                 time.Duration
	checksPerRequest        uint64
	reqRate                 float64
	quiet                   bool
)

func main() {
	log = logrus.StandardLogger()
	flag.StringVar(&configFile, "config", "", "Environment config file")
	flag.StringVar(&grpcAddress, "e", "", "Gubernator GRPC endpoint address")
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
	startCtx := tracing.StartScope(ctx)
	argsMsg := fmt.Sprintf("Command line: %s", strings.Join(os.Args[1:], " "))
	log.Info(argsMsg)
	tracing.EndScope(startCtx, nil)

	var client guber.V1Client
	err = tracing.CallScope(ctx, func(ctx context.Context) error {
		// Print startup message.
		cmdLine := strings.Join(os.Args[1:], " ")
		logrus.WithContext(ctx).Info("Command line: " + cmdLine)

		conf, err := guber.SetupDaemonConfig(log, configFile)
		if err != nil {
			return err
		}
		setter.SetOverride(&conf.GRPCListenAddress, grpcAddress)

		if configFile == "" && grpcAddress == "" && os.Getenv("GUBER_GRPC_ADDRESS") == "" {
			return errors.New("please provide a GRPC endpoint via -e or from a config " +
				"file via -config or set the env GUBER_GRPC_ADDRESS")
		}

		err = guber.SetupTLS(conf.TLS)
		if err != nil {
			return err
		}

		log.WithContext(ctx).Infof("Connecting to '%s'...", conf.GRPCListenAddress)
		client, err = guber.DialV1Server(conf.GRPCListenAddress, conf.ClientTLS())
		return err
	})

	checkErr(err)

	// Generate a selection of rate limits with random limits.
	var rateLimits []*guber.RateLimitReq

	for i := 0; i < 2000; i++ {
		rateLimits = append(rateLimits, &guber.RateLimitReq{
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
			req := &guber.GetRateLimitsReq{
				Requests: rateLimits[i:min(i+int(checksPerRequest), len(rateLimits))],
			}

			fan.Run(func(obj interface{}) error {
				req := obj.(*guber.GetRateLimitsReq)

				if reqRate > 0 {
					limiter.Wait(ctx)
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

func sendRequest(ctx context.Context, client guber.V1Client, req *guber.GetRateLimitsReq) {
	ctx = tracing.StartScope(ctx)
	defer tracing.EndScope(ctx, nil)

	ctx, cancel := ctxutil.WithTimeout(ctx, timeout)

	// Now hit our cluster with the rate limits
	resp, err := client.GetRateLimits(ctx, req)
	cancel()
	if err != nil {
		log.WithContext(ctx).WithError(err).Error("Error in client.GetRateLimits")
		return
	}

	// Sanity checks.
	if resp == nil {
		log.WithContext(ctx).Error("Response object is unexpectedly nil")
		return
	}
	if resp.Responses == nil {
		log.WithContext(ctx).Error("Responses array is unexpectedly nil")
		return
	}

	// Check for overlimit response.
	overlimit := false

	for itemNum, resp := range resp.Responses {
		if resp.Status == guber.Status_OVER_LIMIT {
			overlimit = true
			log.WithContext(ctx).WithField("name", req.Requests[itemNum].Name).
				Info("Overlimit!")
		}
	}

	if overlimit {
		span := trace.SpanFromContext(ctx)
		span.SetAttributes(
			attribute.Bool("overlimit", true),
		)

		if !quiet {
			dumpResp := spew.Sdump(resp)
			log.WithContext(ctx).Info(dumpResp)
		}
	}
}
