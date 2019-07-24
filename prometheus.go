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
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/grpc/stats"
	"time"
)

type GRPCStats struct {
	Duration time.Duration
	Method   string
	Failed   int64
	Success  int64
}

type contextKey struct{}

var statsContextKey = contextKey{}

// Implements the Prometheus collector interface. Such that when the /metrics handler is
// called this collector pulls all the stats from
type Collector struct {
	reqCh chan *GRPCStats
	wg    holster.WaitGroup

	// Metrics collectors
	grpcRequestCount    *prometheus.CounterVec
	grpcRequestDuration *prometheus.HistogramVec
}

func NewGRPCStatsHandler() *Collector {
	c := &Collector{
		grpcRequestCount: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "grpc_request_counts",
			Help: "GRPC requests by status."},
			[]string{"status", "method"}),
		grpcRequestDuration: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name: "grpc_request_duration_milliseconds",
			Help: "GRPC request durations in milliseconds.",
		}, []string{"method"}),
	}
	c.run()
	return c
}

func (c *Collector) run() {
	c.reqCh = make(chan *GRPCStats, 10000)

	c.wg.Until(func(done chan struct{}) bool {
		select {
		case stat := <-c.reqCh:
			c.grpcRequestCount.With(prometheus.Labels{"status": "failed", "method": stat.Method}).Add(float64(stat.Failed))
			c.grpcRequestCount.With(prometheus.Labels{"status": "success", "method": stat.Method}).Add(float64(stat.Success))
			c.grpcRequestDuration.With(prometheus.Labels{"method": stat.Method}).Observe(stat.Duration.Seconds() * 1000)

		/*case <-tick.C:
		// Emit stats about our cache
		if c.cacheStats != nil {
			stats := c.cacheStats.Stats(true)
			c.client.Gauge("cache.size", stats.Size)
			c.client.Incr("cache.hit", stats.Hit)
			c.client.Incr("cache.miss", stats.Miss)
		}

		// Emit stats about our global manager
		if c.serverStats != nil {
			stats := c.serverStats.Stats(true)
			c.client.Gauge("global-manager.broadcast-duration", stats.BroadcastDuration)
			c.client.Incr("global-manager.async-count", stats.AsyncGlobalsCount)
		}
		*/
		case <-done:
			//tick.Stop()
			//c.client.Close()
			return false
		}
		return true
	})
}

func (c *Collector) Close() {
	c.wg.Stop()
}

func (c *Collector) HandleRPC(ctx context.Context, s stats.RPCStats) {
	rs := StatsFromContext(ctx)
	if rs == nil {
		return
	}

	switch t := s.(type) {
	// case *stats.Begin:
	// case *stats.InPayload:
	// case *stats.InHeader:
	// case *stats.InTrailer:
	// case *stats.OutPayload:
	// case *stats.OutHeader:
	// case *stats.OutTrailer:
	case *stats.End:
		rs.Duration = t.EndTime.Sub(t.BeginTime)
		if t.Error != nil {
			rs.Failed = 1
		} else {
			rs.Success = 1
		}
		c.reqCh <- rs
	}
}

func (c *Collector) HandleConn(ctx context.Context, s stats.ConnStats) {}

func (c *Collector) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

func (c *Collector) TagRPC(ctx context.Context, tagInfo *stats.RPCTagInfo) context.Context {
	return ContextWithStats(ctx, &GRPCStats{Method: tagInfo.FullMethodName})
}

// Returns a new `context.Context` that holds a reference to `GRPCStats`.
func ContextWithStats(ctx context.Context, stats *GRPCStats) context.Context {
	return context.WithValue(ctx, statsContextKey, stats)
}

// Returns the `GRPCStats` previously associated with `ctx`.
func StatsFromContext(ctx context.Context) *GRPCStats {
	val := ctx.Value(statsContextKey)
	if rs, ok := val.(*GRPCStats); ok {
		return rs
	}
	return nil
}
