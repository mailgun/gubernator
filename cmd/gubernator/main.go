package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/mailgun/gubernator"
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
	client, err := gubernator.NewClient(os.Args[1])
	checkErr(err)

	// Generate a selection of rate limits with random limits
	var rateLimits []*gubernator.Request

	for i := 0; i < 10000; i++ {
		rateLimits = append(rateLimits, &gubernator.Request{
			Namespace: fmt.Sprintf("ID-%d", i),
			UniqueKey: gubernator.RandomString(10),
			Hits:      1,
			Limit:     randInt(10, 300),
			Duration:  time.Duration(randInt(500, 6000)),
			Algorithm: gubernator.TokenBucket,
		})
	}

	for _, rateLimit := range rateLimits {
		// Now hit our cluster with the rate limits
		resp, err := client.GetRateLimit(context.Background(), rateLimit)
		checkErr(err)

		if resp.Status == gubernator.OverLimit {
			spew.Dump(resp)
		}
	}
}
