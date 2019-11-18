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
	"fmt"
	"github.com/mailgun/gubernator/cache"
	"github.com/mailgun/holster"
	"google.golang.org/grpc"
	"time"
)

// config for a gubernator instance
type Config struct {
	// Required
	GRPCServer *grpc.Server

	// (Optional) Adjust how gubernator behaviors are configured
	Behaviors BehaviorConfig

	// (Optional) The cache implementation
	Cache cache.Cache

	// (Optional) A persistent store implementation. Allows the implementor the ability to store the rate limits this
	// instance of gubernator owns. It's up to the implementor to decide what rate limits to persist.
	// For instance an implementor might only persist rate limits that have an expiration of
	// longer than 1 hour. (Defaults to NullStore)
	Store Store

	// (Optional) A loader from a persistent store. Allows the implementor the ability to load and save
	// the contents of the cache of rate limits when the gubernator instance is started and stopped
	Loader Loader

	// (Optional) This is the peer picker algorithm the server will use decide which peer in the cluster
	// will coordinate a rate limit
	Picker PeerPicker
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
	// How long we should wait for a global sync responses from peers
	GlobalTimeout time.Duration
	// The max number of global updates we can batch into a single peer request
	GlobalBatchLimit int
}

func (c *Config) SetDefaults() error {
	holster.SetDefault(&c.Behaviors.BatchTimeout, time.Millisecond*500)
	holster.SetDefault(&c.Behaviors.BatchLimit, maxBatchSize)
	holster.SetDefault(&c.Behaviors.BatchWait, time.Microsecond*500)

	holster.SetDefault(&c.Behaviors.GlobalTimeout, time.Millisecond*500)
	holster.SetDefault(&c.Behaviors.GlobalBatchLimit, maxBatchSize)
	holster.SetDefault(&c.Behaviors.GlobalSyncWait, time.Microsecond*500)

	holster.SetDefault(&c.Picker, NewConsistantHash(nil))
	holster.SetDefault(&c.Cache, cache.NewLRUCache(0))
	holster.SetDefault(&c.Store, &NullStore{})
	holster.SetDefault(&c.Loader, &NullLoader{})

	if c.Behaviors.BatchLimit > maxBatchSize {
		return fmt.Errorf("Behaviors.BatchLimit cannot exceed '%d'", maxBatchSize)
	}
	return nil
}
