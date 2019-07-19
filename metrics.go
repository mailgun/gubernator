package gubernator

import (
	"context"
	"time"

	"github.com/mailgun/gubernator/cache"
	"google.golang.org/grpc/stats"
)

type MetricsCollector interface {
	GRPCStatsHandler() stats.Handler
	RegisterCacheStats(cache.Stater)
	RegisterServerStats(ServerStater)
	Close()
}

type ServerStater interface {
	Stats(bool) ServerStats
}

type ServerStats struct {
	// How long a Broadcast took to complete
	BroadcastDuration int64
	// How many async globals were handled by the manager
	AsyncGlobalsCount int64
}

type RequestStats struct {
	Method   string
	Duration time.Duration
	Failed   int64
	Called   int64
}

type contextKey struct{}

var statsContextKey = contextKey{}

// Returns a new `context.Context` that holds a reference to `RequestStats`.
func ContextWithStats(ctx context.Context, stats *RequestStats) context.Context {
	return context.WithValue(ctx, statsContextKey, stats)
}

// Returns the `RequestStats` previously associated with `ctx`.
func StatsFromContext(ctx context.Context) *RequestStats {
	val := ctx.Value(statsContextKey)
	if rs, ok := val.(*RequestStats); ok {
		return rs
	}
	return nil
}
