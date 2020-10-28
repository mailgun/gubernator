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

package gubernator

import (
	"crypto/tls"
	"fmt"
	"time"

	"github.com/mailgun/holster/v3/setter"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

// config for a gubernator instance
type Config struct {
	// Required
	GRPCServer *grpc.Server

	// (Optional) Adjust how gubernator behaviors are configured
	Behaviors BehaviorConfig

	// (Optional) The cache implementation
	Cache Cache

	// (Optional) A persistent store implementation. Allows the implementor the ability to store the rate limits this
	// instance of gubernator owns. It's up to the implementor to decide what rate limits to persist.
	// For instance an implementor might only persist rate limits that have an expiration of
	// longer than 1 hour.
	Store Store

	// (Optional) A loader from a persistent store. Allows the implementor the ability to load and save
	// the contents of the cache when the gubernator instance is started and stopped
	Loader Loader

	// (Optional) This is the peer picker algorithm the server will use decide which peer in the local cluster
	// will own the rate limit
	LocalPicker PeerPicker

	// (Optional) This is the peer picker algorithm the server will use when deciding which remote peer to forward
	// rate limits too when a `Config.DataCenter` is set to something other than empty string.
	RegionPicker RegionPeerPicker

	// (Optional) This is the name of our local data center. This value will be used by LocalPicker when
	// deciding who we should immediately connect too for our local picker. Should remain empty if not
	// using multi data center support.
	DataCenter string

	// (Optional) A Logger which implements the declared logger interface (typically *logrus.Entry)
	Logger logrus.FieldLogger

	// (Optional) The TLS config used when connecting to gubernator peers
	PeerTLS *tls.Config
}

type BehaviorConfig struct {
	// How long we should wait for a batched response from a peer
	BatchTimeout time.Duration
	// How long we should wait before sending a batched request
	BatchWait time.Duration
	// The max number of requests we can batch into a single peer request
	BatchLimit int

	// How long a non-owning peer should wait before syncing hits to the owning peer
	GlobalSyncWait time.Duration
	// How long we should wait for global sync responses from peers
	GlobalTimeout time.Duration
	// The max number of global updates we can batch into a single peer request
	GlobalBatchLimit int

	// How long the current region will collect request before pushing them to other regions
	MultiRegionSyncWait time.Duration
	// How long the current region will wait for responses from other regions
	MultiRegionTimeout time.Duration
	// The max number of requests the current region will collect
	MultiRegionBatchLimit int
}

type PeerInfo struct {
	// (Optional) The name of the data center this peer is in. Leave blank if not using multi data center support.
	DataCenter string
	// (Optional) The http address:port of the peer
	HTTPAddress string
	// (Required) The grpc address:port of the peer
	GRPCAddress string
	// (Optional) Is true if PeerInfo is for this instance of gubernator
	IsOwner bool
}

// Returns the hash key used to identify this peer in the Picker.
func (p PeerInfo) HashKey() string {
	return p.GRPCAddress
}

type UpdateFunc func([]PeerInfo)

func (c *Config) SetDefaults() error {
	setter.SetDefault(&c.Behaviors.BatchTimeout, time.Millisecond*500)
	setter.SetDefault(&c.Behaviors.BatchLimit, maxBatchSize)
	setter.SetDefault(&c.Behaviors.BatchWait, time.Microsecond*500)

	setter.SetDefault(&c.Behaviors.GlobalTimeout, time.Millisecond*500)
	setter.SetDefault(&c.Behaviors.GlobalBatchLimit, maxBatchSize)
	setter.SetDefault(&c.Behaviors.GlobalSyncWait, time.Microsecond*500)

	setter.SetDefault(&c.Behaviors.MultiRegionTimeout, time.Millisecond*500)
	setter.SetDefault(&c.Behaviors.MultiRegionBatchLimit, maxBatchSize)
	setter.SetDefault(&c.Behaviors.MultiRegionSyncWait, time.Second)

	setter.SetDefault(&c.LocalPicker, NewReplicatedConsistentHash(nil, DefaultReplicas))
	setter.SetDefault(&c.RegionPicker, NewRegionPicker(nil))
	setter.SetDefault(&c.Cache, NewLRUCache(0))

	if c.Behaviors.BatchLimit > maxBatchSize {
		return fmt.Errorf("Behaviors.BatchLimit cannot exceed '%d'", maxBatchSize)
	}

	// Make a copy of the TLS config in case our caller decides to make changes
	if c.PeerTLS != nil {
		c.PeerTLS = c.PeerTLS.Clone()
	}

	return nil
}
