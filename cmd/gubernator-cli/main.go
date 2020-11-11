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
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"os"

	"github.com/davecgh/go-spew/spew"
	guber "github.com/mailgun/gubernator"
	"github.com/mailgun/holster/v3/clock"
	"github.com/mailgun/holster/v3/setter"
	"github.com/mailgun/holster/v3/syncutil"
	"github.com/sirupsen/logrus"
)

var log *logrus.Logger

func checkErr(err error) {
	if err != nil {
		log.Errorf(err.Error())
		os.Exit(1)
	}
}

func randInt(min, max int) int64 {
	return int64(rand.Intn(max-min) + min)
}

func main() {
	var configFile, GRPCAddress string
	var err error

	log = logrus.StandardLogger()
	flags := flag.NewFlagSet("gubernator", flag.ContinueOnError)
	flags.StringVar(&configFile, "config", "", "environment config file")
	flags.StringVar(&GRPCAddress, "e", "", "the gubernator GRPC endpoint address")
	checkErr(flags.Parse(os.Args[1:]))

	conf, err := guber.SetupDaemonConfig(log, configFile)
	checkErr(err)
	setter.SetOverride(&conf.GRPCListenAddress, GRPCAddress)

	if configFile == "" && GRPCAddress == "" && os.Getenv("GUBER_GRPC_ADDRESS") == "" {
		checkErr(errors.New("please provide a GRPC endpoint via -e or from a config " +
			"file via -config or set the env GUBER_GRPC_ADDRESS"))
	}

	err = guber.SetupTLS(conf.TLS)
	checkErr(err)

	log.Infof("Connecting to '%s'...\n", conf.GRPCListenAddress)
	client, err := guber.DialV1Server(conf.GRPCListenAddress, conf.ClientTLS())
	checkErr(err)

	// Generate a selection of rate limits with random limits
	var rateLimits []*guber.RateLimitReq

	for i := 0; i < 2000; i++ {
		rateLimits = append(rateLimits, &guber.RateLimitReq{
			Name:      fmt.Sprintf("ID-%d", i),
			UniqueKey: guber.RandomString(10),
			Hits:      1,
			Limit:     randInt(1, 10),
			Duration:  randInt(int(clock.Millisecond*500), int(clock.Second*6)),
			Algorithm: guber.Algorithm_TOKEN_BUCKET,
		})
	}

	fan := syncutil.NewFanOut(10)
	for {
		for _, rateLimit := range rateLimits {
			fan.Run(func(obj interface{}) error {
				r := obj.(*guber.RateLimitReq)
				ctx, cancel := context.WithTimeout(context.Background(), clock.Millisecond*500)
				// Now hit our cluster with the rate limits
				resp, err := client.GetRateLimits(ctx, &guber.GetRateLimitsReq{
					Requests: []*guber.RateLimitReq{r},
				})
				checkErr(err)
				cancel()

				if resp.Responses[0].Status == guber.Status_OVER_LIMIT {
					spew.Dump(resp)
				}
				return nil
			}, rateLimit)
		}
	}
}
