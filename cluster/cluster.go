/*
Copyright 2018-2022 Mailgun Technologies Inc

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

	"github.com/mailgun/gubernator/v2"
	"github.com/mailgun/holster/v4/clock"
	"github.com/mailgun/holster/v4/ctxutil"
	"github.com/mailgun/holster/v4/errors"
	"github.com/sirupsen/logrus"
)

const (
	DataCenterNone = ""
	DataCenterOne  = "datacenter-1"
	DataCenterTwo  = "datacenter-2"
)

var daemons []*gubernator.Daemon
var peers []gubernator.PeerInfo

// GetRandomPeer returns a random peer from the cluster
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

// GetPeers returns a list of all peers in the cluster
func GetPeers() []gubernator.PeerInfo {
	return peers
}

// GetDaemons returns a list of all daemons in the cluster
func GetDaemons() []*gubernator.Daemon {
	return daemons
}

// PeerAt returns a specific peer
func PeerAt(idx int) gubernator.PeerInfo {
	return peers[idx]
}

// DaemonAt returns a specific daemon
func DaemonAt(idx int) *gubernator.Daemon {
	return daemons[idx]
}

// NumOfDaemons returns the number of instances
func NumOfDaemons() int {
	return len(daemons)
}

// Start a local cluster of gubernator servers
func Start(numInstances int) error {
	// Ideally we should let the socket choose the port, but then
	// some things like the logger will not be set correctly.
	var peers []gubernator.PeerInfo
	port := 1111
	for i := 0; i < numInstances; i++ {
		peers = append(peers, gubernator.PeerInfo{
			HTTPAddress: fmt.Sprintf("localhost:%d", port),
			GRPCAddress: fmt.Sprintf("localhost:%d", port+1),
		})
		port += 2
	}
	return StartWith(peers)
}

// Restart the cluster
func Restart(ctx context.Context) error {
	for i := 0; i < len(daemons); i++ {
		daemons[i].Close()
		if err := daemons[i].Start(ctx); err != nil {
			return err
		}
		daemons[i].SetPeers(peers)
	}
	return nil
}

// StartWith a local cluster with specific addresses
func StartWith(localPeers []gubernator.PeerInfo) error {
	for _, peer := range localPeers {
		ctx, cancel := ctxutil.WithTimeout(context.Background(), clock.Second*10)
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

// Stop all daemons in the cluster
func Stop() {
	for _, d := range daemons {
		d.Close()
	}
	peers = nil
	daemons = nil
}
