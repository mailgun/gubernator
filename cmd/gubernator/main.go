package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/mailgun/gubernator"
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
		clientCLI(StringToSlice(os.Args[2]))
	}
}

func clientCLI(peers []string) {
	client, err := gubernator.NewClient("test", peers)
	if err != nil {
		fmt.Printf("error: %s\n", err)
		os.Exit(1)
	}

	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*1)
		_, err := client.GetRateLimit(ctx, &gubernator.Request{
			Descriptors: map[string]string{
				"account": randomString(10, "ID-"),
			},
			Limit:    10,
			Duration: time.Second * 5,
			Hits:     1,
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
