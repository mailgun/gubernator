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
	"context"
	"github.com/mailgun/holster"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"time"
)

// globalManager manages async hit queue and updates peers in
// the cluster periodically when a global rate limit we own updates.
type globalManager struct {
	asyncQueue     chan *RateLimitReq
	broadcastQueue chan *RateLimitReq
	wg             holster.WaitGroup
	conf           BehaviorConfig
	log            *logrus.Entry
	instance       *Instance

	asyncMetrics     prometheus.Histogram
	broadcastMetrics prometheus.Histogram
}

func newGlobalManager(conf BehaviorConfig, instance *Instance) *globalManager {
	gm := globalManager{
		log: log.WithField("category", "global-manager"),
		asyncMetrics: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "async_durations",
			Help: "The duration of GLOBAL async sends in seconds.",
		}),
		broadcastMetrics: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "broadcast_durations",
			Help: "The duration of GLOBAL broadcasts to peers in seconds.",
		}),
		asyncQueue:     make(chan *RateLimitReq, 0),
		broadcastQueue: make(chan *RateLimitReq, 0),
		instance:       instance,
		conf:           conf,
	}
	gm.runAsyncHits()
	gm.runBroadcasts()
	return &gm
}

func (gm *globalManager) QueueHit(r *RateLimitReq) {
	gm.asyncQueue <- r
}

func (gm *globalManager) QueueUpdate(r *RateLimitReq) {
	gm.broadcastQueue <- r
}

// runAsyncHits collects async hit requests and queues them to
// be sent to their owning peers.
func (gm *globalManager) runAsyncHits() {
	var interval = NewInterval(gm.conf.GlobalSyncWait)
	hits := make(map[string]*RateLimitReq)

	gm.wg.Until(func(done chan struct{}) bool {
		select {
		case r := <-gm.asyncQueue:
			// Aggregate the hits into a single request
			key := r.HashKey()
			_, ok := hits[key]
			if ok {
				hits[key].Hits += r.Hits
			} else {
				hits[key] = r
			}

			// Send the hits if we reached our batch limit
			if len(hits) == gm.conf.GlobalBatchLimit {
				gm.sendHits(hits)
				hits = make(map[string]*RateLimitReq)
				return true
			}

			// If this is our first queued hit since last send
			// queue the next interval
			if len(hits) == 1 {
				interval.Next()
			}

		case <-interval.C:
			if len(hits) != 0 {
				gm.sendHits(hits)
				hits = make(map[string]*RateLimitReq)
			}
		case <-done:
			return false
		}
		return true
	})
}

// sendHits takes the hits collected by runAsyncHits and sends them to their
// owning peers
func (gm *globalManager) sendHits(hits map[string]*RateLimitReq) {
	type pair struct {
		client *PeerClient
		req    GetPeerRateLimitsReq
	}
	peerRequests := make(map[string]*pair)
	start := time.Now()

	// Assign each request to a peer
	for _, r := range hits {
		peer, err := gm.instance.GetPeer(r.HashKey())
		if err != nil {
			gm.log.WithError(err).Errorf("while getting peer for hash key '%s'", r.HashKey())
			continue
		}

		p, ok := peerRequests[peer.host]
		if ok {
			p.req.Requests = append(p.req.Requests, r)
		} else {
			peerRequests[peer.host] = &pair{
				client: peer,
				req:    GetPeerRateLimitsReq{Requests: []*RateLimitReq{r}},
			}
		}
	}

	// Send the rate limit requests to their respective owning peers.
	for _, p := range peerRequests {
		ctx, cancel := context.WithTimeout(context.Background(), gm.conf.GlobalTimeout)
		_, err := p.client.GetPeerRateLimits(ctx, &p.req)
		cancel()

		if err != nil {
			gm.log.WithError(err).
				Errorf("error sending global hits to '%s'", p.client.host)
			continue
		}
	}
	gm.asyncMetrics.Observe(time.Since(start).Seconds())
}

// runBroadcasts collects status changes for global rate limits and broadcasts the changes to each peer in the cluster.
func (gm *globalManager) runBroadcasts() {
	var interval = NewInterval(gm.conf.GlobalSyncWait)
	updates := make(map[string]*RateLimitReq)

	gm.wg.Until(func(done chan struct{}) bool {
		select {
		case r := <-gm.broadcastQueue:
			updates[r.HashKey()] = r

			// Send the hits if we reached our batch limit
			if len(updates) == gm.conf.GlobalBatchLimit {
				gm.updatePeers(updates)
				updates = make(map[string]*RateLimitReq)
				return true
			}

			// If this is our first queued hit since last send
			// queue the next interval
			if len(updates) == 1 {
				interval.Next()
			}

		case <-interval.C:
			if len(updates) != 0 {
				gm.updatePeers(updates)
				updates = make(map[string]*RateLimitReq)
			}
		case <-done:
			return false
		}
		return true
	})
}

// updatePeers broadcasts global rate limit statuses to all other peers
func (gm *globalManager) updatePeers(updates map[string]*RateLimitReq) {
	var req UpdatePeerGlobalsReq
	start := time.Now()

	for _, rl := range updates {
		// We are only sending the status of the rate limit so
		// we clear the behavior flag so we don't get queued for update again.
		rl.Behavior = 0
		rl.Hits = 0

		status, err := gm.instance.getRateLimit(rl)
		if err != nil {
			gm.log.WithError(err).Errorf("while sending global updates to peers for: '%s'", rl.HashKey())
			continue
		}
		// Build an UpdatePeerGlobalsReq
		req.Globals = append(req.Globals, &UpdatePeerGlobal{
			Algorithm: rl.Algorithm,
			Key:       rl.HashKey(),
			Status:    status,
		})
	}

	for _, peer := range gm.instance.GetPeerList() {
		// Exclude ourselves from the update
		if peer.isOwner {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), gm.conf.GlobalTimeout)
		_, err := peer.UpdatePeerGlobals(ctx, &req)
		cancel()

		if err != nil {
			if err != ErrClosing {
				gm.log.WithError(err).Errorf("error sending global updates to '%s'", peer.host)
			}
			continue
		}
	}

	gm.broadcastMetrics.Observe(time.Since(start).Seconds())
}
