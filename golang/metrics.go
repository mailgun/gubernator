package gubernator

import (
	"context"
	"time"

	"github.com/mailgun/gubernator/golang/cache"
	"google.golang.org/grpc/stats"
)

type MetricsCollector interface {
	GRPCStatsHandler() stats.Handler
	RegisterCacheStats(cache.CacheStats)
	Close()
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
