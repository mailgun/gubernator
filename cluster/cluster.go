package cluster

import (
	"fmt"
	"github.com/mailgun/gubernator"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"net"
	"time"
)

type instance struct {
	GRPC    *grpc.Server
	Guber   *gubernator.Instance
	Address string
}

func (i *instance) Peers() []gubernator.PeerInfo {
	var result []gubernator.PeerInfo
	for _, peer := range peers {
		info := gubernator.PeerInfo{Address: peer}
		if peer == i.Address {
			info.IsOwner = true
		}
		result = append(result, info)
	}
	return result
}

func (i *instance) Stop() {
	i.GRPC.GracefulStop()
}

var instances []*instance
var peers []string

// Returns a random peer from the cluster
func GetPeer() string {
	return gubernator.RandomPeer(peers)
}

// Returns a specific peer
func PeerAt(idx int) string {
	return peers[idx]
}

// Returns a specific instance
func InstanceAt(idx int) *instance {
	return instances[idx]
}

// Start a local cluster of gubernator servers
func Start(numInstances int) error {
	var addresses []string
	for i := 0; i < numInstances; i++ {
		addresses = append(addresses, "")
	}
	return StartWith(addresses)
}

// Start a local cluster with specific addresses
func StartWith(addresses []string) error {
	for _, address := range addresses {
		srv := grpc.NewServer()

		guber, err := gubernator.New(gubernator.Config{
			GRPCServer: srv,
			Behaviors: gubernator.BehaviorConfig{
				GlobalSyncWait: time.Millisecond * 50, // Suitable for testing but not production
				GlobalTimeout:  time.Second,
			},
		})
		if err != nil {
			return errors.Wrap(err, "while creating new gubernator instance")
		}

		listener, err := net.Listen("tcp", address)
		if err != nil {
			return errors.Wrap(err, "while listening on random interface")
		}

		go func() {
			logrus.Infof("Listening on %s", listener.Addr().String())
			if err := srv.Serve(listener); err != nil {
				fmt.Printf("while serving: %s\n", err)
			}
		}()

		peers = append(peers, listener.Addr().String())
		instances = append(instances, &instance{
			Address: listener.Addr().String(),
			Guber:   guber,
			GRPC:    srv,
		})
	}

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
