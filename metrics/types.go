package metrics

import (
	"github.com/mailgun/gubernator/cache"
	"google.golang.org/grpc/stats"
	"time"
)

type Collector interface {
	GRPCStatsHandler() stats.Handler
	RegisterCacheStats(cache.CacheStats)
	Start() error
	Stop()
}

type RequestStats struct {
	Method   string
	Duration time.Duration
	Failed   int64
	Called   int64
}
