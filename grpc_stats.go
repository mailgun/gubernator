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

	"github.com/mailgun/holster/v3/clock"
	"github.com/mailgun/holster/v3/syncutil"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/stats"
)

type GRPCStats struct {
	Duration clock.Duration
	Method   string
	Failed   float64
	Success  float64
}

type contextKey struct{}

var statsContextKey = contextKey{}

// Implements the Prometheus collector interface. Such that when the /metrics handler is
// called this collector pulls all the stats from
type GRPCStatsHandler struct {
	reqCh chan *GRPCStats
	wg    syncutil.WaitGroup

	grpcRequestCount    *prometheus.CounterVec
	grpcRequestDuration *prometheus.SummaryVec
}

func NewGRPCStatsHandler() *GRPCStatsHandler {
	c := &GRPCStatsHandler{
		grpcRequestCount: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gubernator_grpc_request_counts",
			Help: "GRPC requests by status.",
		}, []string{"status", "method"}),
		grpcRequestDuration: prometheus.NewSummaryVec(prometheus.SummaryOpts{
			Name:       "gubernator_grpc_request_duration_milliseconds",
			Help:       "GRPC request durations in milliseconds.",
			Objectives: map[float64]float64{0.5: 0.05, 0.99: 0.001},
		}, []string{"method"}),
	}
	c.run()
	return c
}

func (c *GRPCStatsHandler) run() {
	c.reqCh = make(chan *GRPCStats, 10000)

	c.wg.Until(func(done chan struct{}) bool {
		select {
		case stat := <-c.reqCh:
			c.grpcRequestCount.With(prometheus.Labels{"status": "failed", "method": stat.Method}).Add(stat.Failed)
			c.grpcRequestCount.With(prometheus.Labels{"status": "success", "method": stat.Method}).Add(stat.Success)
			c.grpcRequestDuration.With(prometheus.Labels{"method": stat.Method}).Observe(stat.Duration.Seconds())
		case <-done:
			return false
		}
		return true
	})
}

func (c *GRPCStatsHandler) Describe(ch chan<- *prometheus.Desc) {
	c.grpcRequestCount.Describe(ch)
	c.grpcRequestDuration.Describe(ch)
}

func (c *GRPCStatsHandler) Collect(ch chan<- prometheus.Metric) {
	c.grpcRequestCount.Collect(ch)
	c.grpcRequestDuration.Collect(ch)
}

func (c *GRPCStatsHandler) Close() {
	c.wg.Stop()
}

func (c *GRPCStatsHandler) HandleRPC(ctx context.Context, s stats.RPCStats) {
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

func (c *GRPCStatsHandler) HandleConn(ctx context.Context, s stats.ConnStats) {}

func (c *GRPCStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

func (c *GRPCStatsHandler) TagRPC(ctx context.Context, tagInfo *stats.RPCTagInfo) context.Context {
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
