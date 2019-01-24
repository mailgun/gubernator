package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/mailgun/gubernator"
	"github.com/mailgun/gubernator/proto"
)

// Given a comma separated string, return a slice of string items.
// Return the entire string as the first item if no comma is found.
func StringToSlice(value string, modifiers ...func(s string) string) []string {
	result := strings.Split(value, ",")
	// Apply the modifiers
	for _, modifier := range modifiers {
		for idx, item := range result {
			result[idx] = modifier(item)
		}
	}
	return result
}

func randomString(n int, prefix string) string {
	const alphanum = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	var bytes = make([]byte, n)
	rand.Read(bytes)
	for i, b := range bytes {
		bytes[i] = alphanum[b%byte(len(alphanum))]
	}
	return prefix + string(bytes)
}

func main() {
	switch os.Args[1] {
	case "server":
		server(os.Args[2])
	case "client":
		client(StringToSlice(os.Args[2]))
	}
}

func client(peers []string) {
	fmt.Printf("NewClient\n")
	// Create a new client
	client, errs := gubernator.NewClient(peers)
	if errs != nil {
		for host, err := range errs {
			fmt.Printf("peer error: %s - %s\n", host, err)
		}
	}

	fmt.Printf("one\n")
	// Ensure at least one peer is connected
	if !client.IsConnected() {
		fmt.Printf("Not connected to cluster")
		os.Exit(1)
	}
	fmt.Printf("connected\n")

	//var count uint32
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*1)
		_, err := client.RateLimit(ctx, "recipients_per_second", 1, []*proto.RateLimitDescriptor_Entry{
			{
				//Key:   "account",
				//Value: randomString(10, "ID-"),
				Key: randomString(10, "ID-"),
			},
		})
		if err != nil {
			fmt.Printf("error: %s\n", err)
			os.Exit(1)
		}
		cancel()
		//count++
	}

	//fmt.Printf("Code: %d\n", resp.GetOverallCode())
}

func server(address string) {
	// Create a new server
	server, err := gubernator.NewServer(address)
	if err != nil {
		fmt.Printf("server: %s\n", err)
		os.Exit(1)
	}

	fmt.Printf("Listening: %s....\n", address)
	server.Run()
}
