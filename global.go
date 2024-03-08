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

	"github.com/mailgun/holster/v4/syncutil"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/proto"
)

// globalManager manages async hit queue and updates peers in
// the cluster periodically when a global rate limit we own updates.
type globalManager struct {
	hitsQueue                   chan *RateLimitReq
	updatesQueue                chan *RateLimitReq
	wg                          syncutil.WaitGroup
	conf                        BehaviorConfig
	log                         FieldLogger
	instance                    *V1Instance // todo circular import? V1Instance also holds a reference to globalManager
	metricGlobalSendDuration    prometheus.Summary
	metricGlobalSendQueueLength prometheus.Gauge
	metricBroadcastDuration     prometheus.Summary
	metricBroadcastCounter      *prometheus.CounterVec
	metricGlobalQueueLength     prometheus.Gauge
}

func newGlobalManager(conf BehaviorConfig, instance *V1Instance) *globalManager {
	gm := globalManager{
		log:          instance.log,
		hitsQueue:    make(chan *RateLimitReq, conf.GlobalBatchLimit),
		updatesQueue: make(chan *RateLimitReq, conf.GlobalBatchLimit),
		instance:     instance,
		conf:         conf,
		metricGlobalSendDuration: prometheus.NewSummary(prometheus.SummaryOpts{
			Name:       "gubernator_global_send_duration",
			Help:       "The duration of GLOBAL async sends in seconds.",
			Objectives: map[float64]float64{0.5: 0.05, 0.99: 0.001},
		}),
		metricGlobalSendQueueLength: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "gubernator_global_send_queue_length",
			Help: "The count of requests queued up for global broadcast.  This is only used for GetRateLimit requests using global behavior.",
		}),
		metricBroadcastDuration: prometheus.NewSummary(prometheus.SummaryOpts{
			Name:       "gubernator_broadcast_duration",
			Help:       "The duration of GLOBAL broadcasts to peers in seconds.",
			Objectives: map[float64]float64{0.5: 0.05, 0.99: 0.001},
		}),
		metricBroadcastCounter: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gubernator_broadcast_counter",
			Help: "The count of broadcasts.",
		}, []string{"condition"}),
		metricGlobalQueueLength: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "gubernator_global_queue_length",
			Help: "The count of requests queued up for global broadcast.  This is only used for GetRateLimit requests using global behavior.",
		}),
	}
	gm.runAsyncHits()
	gm.runBroadcasts()
	return &gm
}

func (gm *globalManager) QueueHit(r *RateLimitReq) {
	if r.Hits != 0 {
		gm.hitsQueue <- r
	}
}

func (gm *globalManager) QueueUpdate(r *RateLimitReq) {
	if r.Hits != 0 {
		gm.updatesQueue <- r
	}
}

// runAsyncHits collects async hit requests in a forever loop,
// aggregates them in one request, and sends them to
// the owning peers.
// The updates are sent both when the batch limit is hit
// and in a periodic frequency determined by GlobalSyncWait.
func (gm *globalManager) runAsyncHits() {
	var interval = NewInterval(gm.conf.GlobalSyncWait)
	hits := make(map[string]*RateLimitReq)

	gm.wg.Until(func(done chan struct{}) bool {

		select {
		case r := <-gm.hitsQueue:
			gm.metricGlobalSendQueueLength.Set(float64(len(hits)))
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
				gm.metricGlobalSendQueueLength.Set(0)
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
				gm.metricGlobalSendQueueLength.Set(0)
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
	// fmt.Printf("sendHits() %s, hits: %d\n", gm.instance.conf.InstanceID, len(hits))
	type pair struct {
		client *PeerClient
		req    GetPeerRateLimitsReq
	}
	defer prometheus.NewTimer(gm.metricGlobalSendDuration).ObserveDuration()
	peerRequests := make(map[string]*pair)

	// Assign each request to a peer
	for _, r := range hits {
		peer, err := gm.instance.GetPeer(context.Background(), r.HashKey())
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

	fan := syncutil.NewFanOut(gm.conf.GlobalPeerRequestsConcurrency)
	// Send the rate limit requests to their respective owning peers.
	for _, p := range peerRequests {
		fan.Run(func(in interface{}) error {
			p := in.(*pair)
			ctx, cancel := context.WithTimeout(context.Background(), gm.conf.GlobalTimeout)
			_, err := p.client.GetPeerRateLimits(ctx, &p.req)
			cancel()

			if err != nil {
				gm.log.WithError(err).
					Errorf("while sending global hits to '%s'", p.client.Info().GRPCAddress)
			}
			return nil
		}, p)
	}
	fan.Wait()
}

// runBroadcasts collects status changes for global rate limits in a forever loop,
// and broadcasts the changes to each peer in the cluster.
// The updates are sent both when the batch limit is hit
// and in a periodic frequency determined by GlobalSyncWait.
func (gm *globalManager) runBroadcasts() {
	var interval = NewInterval(gm.conf.GlobalSyncWait)
	updates := make(map[string]*RateLimitReq)

	gm.wg.Until(func(done chan struct{}) bool {
		select {
		case r := <-gm.updatesQueue:
			updates[r.HashKey()] = r
			gm.metricGlobalQueueLength.Set(float64(len(updates)))

			// Send the broadcast if we reached our batch limit
			if len(updates) >= gm.conf.GlobalBatchLimit {
				gm.metricBroadcastCounter.WithLabelValues("queue_full").Inc()
				gm.broadcastPeers(updates)
				updates = make(map[string]*RateLimitReq)
				gm.metricGlobalQueueLength.Set(0)
				return true
			}

			// If this is our first queued updated since last send
			// queue the next interval
			if len(updates) == 1 {
				interval.Next()
			}

		case <-interval.C:
			if len(updates) == 0 {
				break
			}
			gm.metricBroadcastCounter.WithLabelValues("timer").Inc()
			gm.broadcastPeers(updates)
			updates = make(map[string]*RateLimitReq)
			gm.metricGlobalQueueLength.Set(0)

		case <-done:
			return false
		}
		return true
	})
}

// broadcastPeers broadcasts global rate limit statuses to all other peers
func (gm *globalManager) broadcastPeers(updates map[string]*RateLimitReq) {
	// fmt.Printf("broadcastPeers() %s, updates: %d\n", gm.instance.conf.InstanceID, len(updates))
	defer prometheus.NewTimer(gm.metricBroadcastDuration).ObserveDuration()
	ctx := context.Background()
	var req UpdatePeerGlobalsReq

	for _, r := range updates {
		// Copy the original since we are removing the GLOBAL behavior
		rl := proto.Clone(r).(*RateLimitReq)
		// We are only sending the status of the rate limit so, we
		// clear the behavior flag, so we don't get queued for update again.
		SetBehavior(&rl.Behavior, Behavior_GLOBAL, false)
		rl.Hits = 0

		status, err := gm.instance.getLocalRateLimit(ctx, rl)
		if err != nil {
			gm.log.WithError(err).Errorf("while getting local rate limit for: '%s'", rl.HashKey())
			continue
		}
		req.Globals = append(req.Globals, &UpdatePeerGlobal{
			Algorithm: rl.Algorithm,
			Key:       rl.HashKey(),
			Status:    status,
		})
	}

	fan := syncutil.NewFanOut(gm.conf.GlobalPeerRequestsConcurrency)
	for _, peer := range gm.instance.GetPeerList() {
		// Exclude ourselves from the update
		if peer.Info().IsOwner {
			continue
		}

		fan.Run(func(in interface{}) error {
			peer := in.(*PeerClient)
			ctx, cancel := context.WithTimeout(ctx, gm.conf.GlobalTimeout)
			_, err := peer.UpdatePeerGlobals(ctx, &req)
			cancel()

			if err != nil {
				// Skip peers that are not in a ready state
				if !IsNotReady(err) {
					gm.log.WithError(err).Errorf("while broadcasting global updates to '%s'", peer.Info().GRPCAddress)
				}
			}
			return nil
		}, peer)
	}
	fan.Wait()
}

func (gm *globalManager) Close() {
	gm.wg.Stop()
}
