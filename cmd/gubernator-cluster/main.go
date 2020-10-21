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
	"fmt"
	"os"
	"os/signal"

	"github.com/mailgun/gubernator"
	"github.com/mailgun/gubernator/cluster"
	"github.com/sirupsen/logrus"
)

// Start a cluster of gubernator instances for use in testing clients
func main() {
	logrus.SetLevel(logrus.InfoLevel)
	// Start a local cluster
	err := cluster.StartWith([]gubernator.PeerInfo{
		{GRPCAddress: "127.0.0.1:9990", HTTPAddress: "127.0.0.1:9980"},
		{GRPCAddress: "127.0.0.1:9991", HTTPAddress: "127.0.0.1:9981"},
		{GRPCAddress: "127.0.0.1:9992", HTTPAddress: "127.0.0.1:9982"},
		{GRPCAddress: "127.0.0.1:9993", HTTPAddress: "127.0.0.1:9983"},
		{GRPCAddress: "127.0.0.1:9994", HTTPAddress: "127.0.0.1:9984"},
		{GRPCAddress: "127.0.0.1:9995", HTTPAddress: "127.0.0.1:9985"},
	})
	if err != nil {
		panic(err)
	}

	fmt.Println("Running.....")

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
