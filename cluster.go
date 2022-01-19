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

package gubernator

import (
	"crypto/tls"

	"github.com/mailgun/holster/v4/syncutil"
)

type ReplicaConfig struct {
	// The name of the replica which will be referenced by the RateLimitReq.Replica field
	Name string

	// The name of all the clusters which are included in this replica.
	Clusters []string
}

// TODO: If the Host provided in the ClusterConfig is a DNS name, ensure we retry relay requests
//   at least twice ensuring we perform a non cached lookup of the DNS name.

type ClusterConfig struct {
	// The name of this cluster
	Name string

	// A list of DNS names or ip addresses that should be contacted when relaying
	// rate limits to the remote cluster. This list is merged with any addresses provided
	// by the Config.ClusterPicker
	Addresses []string

	// The TLS config used to contact the remote cluster
	TLS *tls.Config
}

type RemoteClusterConfig struct {
	// This is a list of replicas with a list of clusters included in the replicas.
	// These are the replicas rate limits can reference in the RateLimitReq.Replica field
	Replicas []ReplicaConfig

	// This is a static list of clusters Gubernator was provided at start up. This config
	// supersedes any peers or clusters dynamically detected by a custom implementation
	// of Config.ClusterPicker
	Clusters []ClusterConfig
}

type remoteClusterManager struct {
	reqQueue chan *RateLimitReq
	wg       syncutil.WaitGroup
	conf     BehaviorConfig
	log      FieldLogger
	instance *V1Instance
}

func newRemoteClusterManager(conf BehaviorConfig, instance *V1Instance) *remoteClusterManager {
	mm := remoteClusterManager{
		reqQueue: make(chan *RateLimitReq, conf.MultiClusterBatchLimit),
		log:      instance.log,
		instance: instance,
		conf:     conf,
	}
	mm.runAsyncReqs()
	return &mm
}

// QueueHits writes the RateLimitReq to be asynchronously sent to other clusters
func (m *remoteClusterManager) QueueHits(r *RateLimitReq) {
	m.reqQueue <- r
}

func (m *remoteClusterManager) runAsyncReqs() {
	// TODO: Get all the replicas in our config and create a list that we can traverse

	// TODO: I think we should merge the cluster config in with dynamic peer listing
	//   provided by the ClusterPicker when SetPeers() is called. Such that we can rely
	//   on the Picker to have all the Addresses of the remote cluster

	// Struct which holds all the hits for relay to a specific cluster
	type ClusterHits struct {
		// The name of the cluster
		Name string
		// A Map of all the rate limits
		RateLimits map[string]*RateLimitReq
	}

	var interval = NewInterval(m.conf.MultiClusterSyncWait)
	hits := make(map[string]*RateLimitReq)

	m.wg.Until(func(done chan struct{}) bool {
		select {
		case r := <-m.reqQueue:
			key := r.HashKey()

			// Aggregate the hits into a single request
			_, ok := hits[key]
			if ok {
				hits[key].Hits += r.Hits
			} else {
				hits[key] = r
			}

			// Send the hits if we reached our batch limit
			if len(hits) == m.conf.MultiClusterBatchLimit {
				for dc, picker := range m.instance.GetClusterPickers() {
					m.log.Debugf("Sending %v hit(s) to %s picker", len(hits), dc)
					go m.sendHits(hits, picker)
				}
				hits = make(map[string]*RateLimitReq)
			}

			// Queue next interval
			if len(hits) == 1 {
				interval.Next()
			}

		case <-interval.C:
			if len(hits) > 0 {
				for dc, picker := range m.instance.GetClusterPickers() {
					m.log.Debugf("Sending %v hit(s) to %s picker", len(hits), dc)
					go m.sendHits(hits, picker)
				}
				hits = make(map[string]*RateLimitReq)
			}

		case <-done:
			return false
		}
		return true
	})
}

// TODO: Setup the cluster pickers based on the cluster config set by the admin.
// TODO: Need to consider how SetPeer() behaves... as it currently over writes
//  all current peer information, so once the local cluster changes we will loose
//  the static config provided by the admin.

func (m *remoteClusterManager) sendHits(r map[string]*RateLimitReq, picker PeerPicker) {
	type Batch struct {
		Peer       *PeerClient
		RateLimits []*RateLimitReq
	}

	// TODO: Set ALWAYS_COUNT_HITS behavior so our hits are not ignored by the remote peer if the RL is
	//  over the limit.

	if picker.Size() == 1 {
		// TODO: In this configuration no need to constantly call picker.Get() as we know all the
		//   RL are going to the same destination.
	}

	// TODO: If the picker has more than one entry we might assume that we are NOT sending rate limits to a DNS and thus
	//  the remote instance will NOT need to look up the owning instances as the local instance of the picker
	//  should pick the correct remote instance. In this case we set HASH_COMPUTED behavior on the rate limits we
	//  send over. (Might consider adding an special flag in the cluster that indicates this is the case)

	byPeer := make(map[string]*Batch)
	// For each key in the map, build a batch request to send to each peer
	for key, req := range r {
		p, err := picker.Get(key)
		if err != nil {
			m.log.WithError(err).Errorf("while asking remote picker for peer for key '%s'", key)
			continue
		}
		batch, ok := byPeer[p.Info().HashKey()]
		if ok {
			batch.RateLimits = append(batch.RateLimits, req)
		} else {
			byPeer[p.Info().HashKey()] = &Batch{Peer: p, RateLimits: []*RateLimitReq{req}}
		}
	}

	//for _, batch := range byPeer {
	//	// TODO: Implement UpdateRateLimits()
	//	// Sends the batch of hits to the remote peer
	//	batch.Peer.UpdateRateLimits()
	//}
	// TODO: Implement a GRPC call to fetch a RL from a peer, or return nil if RL doesn't exist
	//       this is useful for debugging in production and will also be useful in tests
	//
	// TODO: Send the hits to the remote peer
}

func (m *remoteClusterManager) Close() {
	m.wg.Stop()
}
