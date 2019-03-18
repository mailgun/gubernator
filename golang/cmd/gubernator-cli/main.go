package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/mailgun/gubernator/golang"
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
	client, err := gubernator.NewV1Client(os.Args[1])
	checkErr(err)

	// Generate a selection of rate limits with random limits
	var rateLimits []*gubernator.Request

	for i := 0; i < 2000; i++ {
		rateLimits = append(rateLimits, &gubernator.Request{
			Namespace: fmt.Sprintf("ID-%d", i),
			UniqueKey: gubernator.RandomString(10),
			Hits:      1,
			Limit:     randInt(1, 10),
			Duration:  time.Duration(randInt(int(time.Millisecond*500), int(time.Second*6))),
			Algorithm: gubernator.TokenBucket,
		})
	}

	fan := holster.NewFanOut(10)
	for {
		for _, rateLimit := range rateLimits {
			fan.Run(func(obj interface{}) error {
				r := obj.(*gubernator.Request)
				// Now hit our cluster with the rate limits
				_, err := client.GetRateLimit(context.Background(), r)
				checkErr(err)

				/*if resp.Status == gubernator.OverLimit {
					spew.Dump(resp)
				}*/
				return nil
			}, rateLimit)
		}
	}
}
