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
	"time"

	"github.com/mailgun/holster/v3/clock"
	"github.com/mailgun/holster/v3/syncutil"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

// globalManager manages async hit queue and updates peers in
// the cluster periodically when a global rate limit we own updates.
type globalManager struct {
	asyncQueue     chan *RateLimitReq
	broadcastQueue chan *RateLimitReq
	wg             syncutil.WaitGroup
	conf           BehaviorConfig
	log            logrus.FieldLogger
	instance       *V1Instance

	asyncMetrics     prometheus.Summary
	broadcastMetrics prometheus.Summary
}

func newGlobalManager(conf BehaviorConfig, instance *V1Instance) *globalManager {
	gm := globalManager{
		log: instance.log,
		asyncMetrics: prometheus.NewSummary(prometheus.SummaryOpts{
			Help:       "The duration of GLOBAL async sends in seconds.",
			Name:       "gubernator_async_durations",
			Objectives: map[float64]float64{0.5: 0.05, 0.99: 0.001},
		}),
		broadcastMetrics: prometheus.NewSummary(prometheus.SummaryOpts{
			Help:       "The duration of GLOBAL broadcasts to peers in seconds.",
			Name:       "gubernator_broadcast_durations",
			Objectives: map[float64]float64{0.5: 0.05, 0.99: 0.001},
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
	start := clock.Now()

	// Assign each request to a peer
	for _, r := range hits {
		peer, err := gm.instance.GetPeer(r.HashKey())
		if err != nil {
			gm.log.WithError(err).Errorf("while getting peer for hash key '%s'", r.HashKey())
			continue
		}

		p, ok := peerRequests[peer.Info().GRPCAddress]
		if ok {
			p.req.Requests = append(p.req.Requests, r)
		} else {
			peerRequests[peer.Info().GRPCAddress] = &pair{
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
				Errorf("error sending global hits to '%s'", p.client.Info().GRPCAddress)
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
				gm.broadcastPeers(updates)
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
				gm.broadcastPeers(updates)
				updates = make(map[string]*RateLimitReq)
			}
		case <-done:
			return false
		}
		return true
	})
}

// broadcastPeers broadcasts global rate limit statuses to all other peers
func (gm *globalManager) broadcastPeers(updates map[string]*RateLimitReq) {
	var req UpdatePeerGlobalsReq
	start := clock.Now()

	for _, r := range updates {
		// Copy the original since we removing the GLOBAL behavior
		rl := *r
		// We are only sending the status of the rate limit so
		// we clear the behavior flag so we don't get queued for update again.
		SetBehavior(&rl.Behavior, Behavior_GLOBAL, false)
		rl.Hits = 0

		status, err := gm.instance.getRateLimit(&rl)
		if err != nil {
			gm.log.WithError(err).Errorf("while broadcasting update to peers for: '%s'", rl.HashKey())
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
		if peer.Info().IsOwner {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), gm.conf.GlobalTimeout)
		_, err := peer.UpdatePeerGlobals(ctx, &req)
		cancel()

		if err != nil {
			// Skip peers that are not in a ready state
			if !IsNotReady(err) {
				gm.log.WithError(err).Errorf("while broadcasting global updates to '%s'", peer.Info().GRPCAddress)
			}
			continue
		}
	}

	gm.broadcastMetrics.Observe(time.Since(start).Seconds())
}
