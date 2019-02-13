package metrics

import (
	"github.com/mailgun/gubernator/cache"
	"github.com/mailgun/holster/clock"
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

type Config struct {
	Period clock.DurationJSON `json:"period"`
	Prefix string             `json:"prefix"`
	Host   string             `json:"host"`
	Port   int                `json:"port"`
}
