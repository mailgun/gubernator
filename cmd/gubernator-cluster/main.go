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

	"github.com/mailgun/gubernator/v2/cluster"
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
