package main

import (
	"context"
	"fmt"
	"github.com/davecgh/go-spew/spew"
	"math/rand"
	"os"
	"time"

	guber "github.com/mailgun/gubernator"
	"github.com/mailgun/holster"
)

func checkErr(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
}

func randInt(min, max int) int64 {
	return int64(rand.Intn(max-min) + min)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Printf("Please provide an gubernator GRPC endpoint address\n")
		os.Exit(1)
	}

	client, err := guber.DialV1Server(os.Args[1])
	checkErr(err)

	// Generate a selection of rate limits with random limits
	var rateLimits []*guber.RateLimitReq

	for i := 0; i < 2000; i++ {
		rateLimits = append(rateLimits, &guber.RateLimitReq{
			Name:      fmt.Sprintf("ID-%d", i),
			UniqueKey: guber.RandomString(10),
			Hits:      1,
			Limit:     randInt(1, 10),
			Duration:  randInt(int(time.Millisecond*500), int(time.Second*6)),
			Algorithm: guber.Algorithm_TOKEN_BUCKET,
		})
	}

	fan := holster.NewFanOut(10)
	for {
		for _, rateLimit := range rateLimits {
			fan.Run(func(obj interface{}) error {
				r := obj.(*guber.RateLimitReq)
				ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*500)
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
