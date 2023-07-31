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
	"context"
	"time"

	"github.com/mailgun/holster/v4/clock"
	"github.com/mailgun/holster/v4/ctxutil"
	"github.com/mailgun/holster/v4/syncutil"
	"github.com/mailgun/holster/v4/tracing"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/proto"
)

// broadcastManager manages the distribution of rate limits within a single cluster.
// It is used by both multi cluster and broadcast behavior to distribute rate limits.
type broadcastManager struct {
	asyncQueue     chan *RateLimitReq
	broadcastQueue chan *RateLimitReq
	wg             syncutil.WaitGroup
	conf           BehaviorConfig
	log            FieldLogger
	instance       *V1Instance

	asyncMetrics     prometheus.Summary
	broadcastMetrics prometheus.Summary
}

func newBroadcastManager(conf BehaviorConfig, instance *V1Instance) *broadcastManager {
	gm := broadcastManager{
		log: instance.log,
		asyncMetrics: prometheus.NewSummary(prometheus.SummaryOpts{
			Help:       "The duration of async sends in seconds.",
			Name:       "gubernator_async_durations",
			Objectives: map[float64]float64{0.5: 0.05, 0.99: 0.001},
		}),
		broadcastMetrics: prometheus.NewSummary(prometheus.SummaryOpts{
			Help:       "The duration of broadcasts to peers in seconds.",
			Name:       "gubernator_broadcast_durations",
			Objectives: map[float64]float64{0.5: 0.05, 0.99: 0.001},
		}),
		asyncQueue:     make(chan *RateLimitReq, conf.GlobalBatchLimit),
		broadcastQueue: make(chan *RateLimitReq, conf.GlobalBatchLimit),
		instance:       instance,
		conf:           conf,
	}
	gm.runAsyncHits()
	gm.runBroadcasts()
	return &gm
}

func (gm *broadcastManager) QueueHit(r *RateLimitReq) {
	gm.asyncQueue <- r
}

func (gm *broadcastManager) QueueUpdate(r *RateLimitReq) {
	gm.broadcastQueue <- r
}

// runAsyncHits collects async hit requests and queues them to
// be sent to their owning peers.
func (gm *broadcastManager) runAsyncHits() {
	var interval = NewInterval(gm.conf.GlobalSyncWait)
	hits := make(map[string]*RateLimitReq)

	gm.wg.Until(func(done chan struct{}) bool {
		ctx := tracing.StartScopeDebug(context.Background())
		defer tracing.EndScope(ctx, nil)

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
				gm.sendHits(ctx, hits)
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
				gm.sendHits(ctx, hits)
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
func (gm *broadcastManager) sendHits(ctx context.Context, hits map[string]*RateLimitReq) {
	type pair struct {
		client *PeerClient
		req    GetPeerRateLimitsReq
	}
	peerRequests := make(map[string]*pair)
	start := clock.Now()

	// Assign each request to a peer
	for _, r := range hits {
		peer, err := gm.instance.GetPeer(ctx, r.HashKey())
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
		ctx, cancel := ctxutil.WithTimeout(context.Background(), gm.conf.GlobalTimeout)
		_, err := p.client.GetPeerRateLimits(ctx, &p.req)
		cancel()

		if err != nil {
			gm.log.WithError(err).
				Errorf("error sending broadcast hits to '%s'", p.client.Info().GRPCAddress)
			continue
		}
	}
	gm.asyncMetrics.Observe(time.Since(start).Seconds())
}

// runBroadcasts collects status changes for broadcast rate limits and broadcasts the changes to each peer in the cluster.
func (gm *broadcastManager) runBroadcasts() {
	var interval = NewInterval(gm.conf.GlobalSyncWait)
	updates := make(map[string]*RateLimitReq)

	gm.wg.Until(func(done chan struct{}) bool {
		ctx := tracing.StartScopeDebug(context.Background())
		defer tracing.EndScope(ctx, nil)

		select {
		case r := <-gm.broadcastQueue:
			updates[r.HashKey()] = r

			// Send the hits if we reached our batch limit
			if len(updates) == gm.conf.GlobalBatchLimit {
				gm.broadcastPeers(ctx, updates)
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
				gm.broadcastPeers(ctx, updates)
				updates = make(map[string]*RateLimitReq)
			}
		case <-done:
			return false
		}
		return true
	})
}

// broadcastPeers broadcasts rate limit statuses to all other peers
func (gm *broadcastManager) broadcastPeers(ctx context.Context, updates map[string]*RateLimitReq) {
	var req UpdatePeerGlobalsReq
	start := clock.Now()

	for _, r := range updates {
		// Copy the original since we are removing the GLOBAL behavior
		rl := proto.Clone(r).(*RateLimitReq)
		// We are only sending the status of the rate limit, so
		// we clear the behavior flag, so we don't get queued for update again.
		SetBehavior(&rl.Behavior, Behavior_GLOBAL, false)
		rl.Hits = 0

		status, err := gm.instance.getRateLimit(ctx, rl)
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

		ctx, cancel := ctxutil.WithTimeout(context.Background(), gm.conf.GlobalTimeout)
		_, err := peer.UpdatePeerGlobals(ctx, &req)
		cancel()

		if err != nil {
			// Skip peers that are not in a ready state
			if !IsNotReady(err) {
				gm.log.WithError(err).Errorf("while broadcasting broadcast updates to '%s'", peer.Info().GRPCAddress)
			}
			continue
		}
	}

	gm.broadcastMetrics.Observe(time.Since(start).Seconds())
}

func (gm *broadcastManager) Close() {
	gm.wg.Stop()
}
