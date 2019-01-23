package main

import (
	"context"
	"fmt"
	"github.com/mailgun/gubernator"
	"github.com/mailgun/gubernator/proto"
	"os"
	"time"
)

func main() {

	// Create a new client
	client, errs := gubernator.NewClient([]string{"host1"})
	if errs != nil {
		for host, err := range errs {
			fmt.Printf("peer error: %s - %s", host, err)
		}
	}

	// Ensure at least one peer is connected
	if !client.IsConnected() {
		fmt.Printf("Not connected to cluster")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*1)
	defer cancel()
	resp, err := client.RateLimit(ctx, &proto.RateLimitRequest{
		Domain:      "recipients_per_second",
		Descriptors: []*proto.RateLimitDescriptor{
			// TODO: Make this less bad
		},
	})
	if err != nil {
		fmt.Printf("rate limit error")
		os.Exit(1)
	}

	fmt.Printf("Code: %d\n", resp.GetOverallCode())
}
