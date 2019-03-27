package main

import (
	"fmt"
	"os"
	"os/signal"

	"github.com/mailgun/gubernator/golang/cluster"
	"github.com/sirupsen/logrus"
)

// Start a cluster of gubernator instances for use in testing clients
func main() {
	logrus.SetLevel(logrus.InfoLevel)
	// Start a local cluster
	err := cluster.StartWith([]string{
		"127.0.0.1:9090",
		"127.0.0.1:9091",
		"127.0.0.1:9092",
		"127.0.0.1:9093",
		"127.0.0.1:9094",
		"127.0.0.1:9095",
	})
	if err != nil {
		fmt.Println(err)
	}

	fmt.Println("Ready")

	// Wait until we get a INT signal then shutdown the cluster
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	for sig := range c {
		if sig == os.Interrupt {
			cluster.Stop()
			os.Exit(0)
		}
	}
}
