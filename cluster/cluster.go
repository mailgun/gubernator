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
	"context"
	"fmt"
	"math/rand"

	"github.com/mailgun/gubernator"
	"github.com/mailgun/holster/v4/clock"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const (
	DataCenterNone = ""
	DataCenterOne  = "datacenter-1"
	DataCenterTwo  = "datacenter-2"
)

var daemons []*gubernator.Daemon
var peers []gubernator.PeerInfo

// Returns a random peer from the cluster
func GetRandomPeer(dc string) gubernator.PeerInfo {
	var local []gubernator.PeerInfo

	for _, p := range peers {
		if p.DataCenter == dc {
			local = append(local, p)
		}
	}

	if len(local) == 0 {
		panic(fmt.Sprintf("failed to find random peer for dc '%s'", dc))
	}

	return local[rand.Intn(len(local))]
}

// Returns a list of all peers in the cluster
func GetPeers() []gubernator.PeerInfo {
	return peers
}

// Returns a list of all deamons in the cluster
func GetDaemons() []*gubernator.Daemon {
	return daemons
}

// Returns a specific peer
func PeerAt(idx int) gubernator.PeerInfo {
	return peers[idx]
}

// Returns a specific daemon
func DaemonAt(idx int) *gubernator.Daemon {
	return daemons[idx]
}

// Returns the number of instances
func NumOfDaemons() int {
	return len(daemons)
}

// Start a local cluster of gubernator servers
func Start(numInstances int) error {
	peers := make([]gubernator.PeerInfo, numInstances, numInstances)
	return StartWith(peers)
}

func Restart(ctx context.Context) {
	for i := 0; i < len(daemons); i++ {
		daemons[i].Close()
		daemons[i].Start(ctx)
		daemons[i].SetPeers(peers)
	}
}

// Start a local cluster with specific addresses
func StartWith(localPeers []gubernator.PeerInfo) error {
	for _, peer := range localPeers {
		ctx, cancel := context.WithTimeout(context.Background(), clock.Second*10)
		d, err := gubernator.SpawnDaemon(ctx, gubernator.DaemonConfig{
			Logger:            logrus.WithField("instance", peer.GRPCAddress),
			GRPCListenAddress: peer.GRPCAddress,
			HTTPListenAddress: peer.HTTPAddress,
			DataCenter:        peer.DataCenter,
			Behaviors: gubernator.BehaviorConfig{
				// Suitable for testing but not production
				GlobalSyncWait:     clock.Millisecond * 50,
				GlobalTimeout:      clock.Second * 5,
				BatchTimeout:       clock.Second * 5,
				MultiRegionTimeout: clock.Second * 5,
			},
		})
		cancel()
		if err != nil {
			return errors.Wrapf(err, "while starting server for addr '%s'", peer.GRPCAddress)
		}

		// Add the peers and daemons to the package level variables
		peers = append(peers, gubernator.PeerInfo{
			GRPCAddress: d.GRPCListeners[0].Addr().String(),
			HTTPAddress: d.HTTPListener.Addr().String(),
			DataCenter:  peer.DataCenter,
		})
		daemons = append(daemons, d)
	}

	// Tell each instance about the other peers
	for _, d := range daemons {
		d.SetPeers(peers)
	}
	return nil
}

func Stop() {
	for _, d := range daemons {
		d.Close()
	}
	peers = nil
	daemons = nil
}
