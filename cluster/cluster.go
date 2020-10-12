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

package cluster

import (
	"fmt"
	"net"
	"time"

	"github.com/mailgun/gubernator"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

type instance struct {
	GRPC    *grpc.Server
	Guber   *gubernator.Instance
	Address string
}

func (i *instance) Peers() []gubernator.PeerInfo {
	var result []gubernator.PeerInfo
	for _, peer := range peers {
		if peer.Address == i.Address {
			peer.IsOwner = true
		}
		result = append(result, peer)
	}
	return result
}

func (i *instance) Stop() error {
	err := i.Guber.Close()
	i.GRPC.GracefulStop()
	return err
}

var instances []*instance
var peers []gubernator.PeerInfo

// Returns default testing configuration
func GetDefaultConfig() gubernator.Config {
	return gubernator.Config{
		Behaviors: gubernator.BehaviorConfig{
			GlobalSyncWait:      time.Millisecond * 50, // Suitable for testing but not production
			GlobalTimeout:       time.Second,
			MultiRegionSyncWait: time.Millisecond * 50, // Suitable for testing but not production
			MultiRegionTimeout:  time.Second,
		},
	}
}

// Returns a random peer from the cluster
func GetRandomPeer() gubernator.PeerInfo {
	return gubernator.RandomPeer(peers)
}

// Returns a specific peer
func PeerAt(idx int) gubernator.PeerInfo {
	return peers[idx]
}

// Returns a specific instance
func InstanceAt(idx int) *instance {
	return instances[idx]
}

// Return the specific instance for a host
func InstanceForHost(host string) *instance {
	for i := range instances {
		if instances[i].Address == host {
			return instances[i]
		}
	}
	return nil
}

// Stop an instance without updating peers, used to cause connection errors
func StopInstanceAt(idx int) {
	instances[idx].Stop()
}

// Returns the number of instances
func NumOfInstances() int {
	return len(instances)
}

// Start a local cluster of gubernator servers
func Start(numInstances int) error {
	addresses := make([]string, numInstances, numInstances)
	return StartWith(addresses)
}

// Start a local cluster with specific addresses
func StartWith(addresses []string) error {
	config := GetDefaultConfig()
	for _, address := range addresses {
		ins, err := StartInstance(address, config)
		if err != nil {
			return errors.Wrapf(err, "while starting instance for addr '%s'", address)
		}

		// Add the peers and instances to the package level variables
		peers = append(peers, gubernator.PeerInfo{Address: ins.Address})
		instances = append(instances, ins)
	}

	// Tell each instance about the other peers
	for _, ins := range instances {
		ins.Guber.SetPeers(ins.Peers())
	}
	return nil
}

func Stop() {
	for _, ins := range instances {
		ins.Stop()
	}
}

// Start a single instance of gubernator with the provided config and listening address.
// If address is empty string a random port on the loopback device will be chosen.
func StartInstance(address string, conf gubernator.Config) (*instance, error) {
	conf.GRPCServer = grpc.NewServer()

	guber, err := gubernator.New(conf)
	if err != nil {
		return nil, errors.Wrap(err, "while creating new gubernator instance")
	}

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, errors.Wrap(err, "while listening on random interface")
	}

	go func() {
		logrus.Infof("Listening on %s", listener.Addr().String())
		if err := conf.GRPCServer.Serve(listener); err != nil {
			fmt.Printf("while serving: %s\n", err)
		}
	}()

	// Wait until the instance responds to connect
	for i := 0; i < 10; i++ {
		conn, err := net.Dial("tcp", address)
		if err != nil {
			break
		}
		conn.Close()
		time.Sleep(time.Millisecond * 50)
	}

	guber.SetPeers([]gubernator.PeerInfo{{Address: listener.Addr().String(), IsOwner: true}})

	return &instance{
		Address: listener.Addr().String(),
		GRPC:    conf.GRPCServer,
		Guber:   guber,
	}, nil
}
