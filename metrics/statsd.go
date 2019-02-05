package metrics

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mailgun/gubernator/cache"
	"github.com/sirupsen/logrus"
	"github.com/smira/go-statsd"
	"google.golang.org/grpc/stats"
)

type StatsdClient interface {
	PrecisionTiming(string, time.Duration)
	Inc(string, int64)
	Close() error
}

type NullClient struct{}

func (n *NullClient) PrecisionTiming(string, time.Duration) {}
func (n *NullClient) Incr(string, int64, ...statsd.Tag)     {}
func (n *NullClient) Inc(string, int64)                     {}
func (n *NullClient) Close() error                          { return nil }

type StatsdMetrics struct {
	reqChan    chan *RequestStats
	cacheStats cache.CacheStats
	done       chan struct{}
	client     StatsdClient
	log        *logrus.Entry
}

func NewStatsdMetrics(client StatsdClient) *StatsdMetrics {
	sd := StatsdMetrics{
		client: client,
		log:    logrus.WithField("category", "metrics"),
	}
	return &sd
}

func (sd *StatsdMetrics) Start() error {
	sd.reqChan = make(chan *RequestStats, 10000)
	methods := make(map[string]RequestStats)
	sd.done = make(chan struct{})

	tick := time.NewTicker(time.Second)
	go func() {
		for {
			select {
			case stat := <-sd.reqChan:
				// Aggregate GRPC method stats
				item, ok := methods[stat.Method]
				if ok {
					item.Failed += stat.Failed
					item.Called += 1
					if item.Duration > stat.Duration {
						item.Duration = stat.Duration
					}
					continue
				}
				methods[stat.Method] = *stat
			case <-tick.C:
				// Emit stats about GRPC method calls
				for k, v := range methods {
					method := k[strings.LastIndex(k, "/"):]
					sd.client.PrecisionTiming(fmt.Sprintf("api.%s.total", method), v.Duration)
					sd.client.Inc(fmt.Sprintf("api.%s.total", method), v.Called)
					sd.client.Inc(fmt.Sprintf("api.%s.failed", method), v.Failed)
					methods[k] = RequestStats{}
				}

				// Emit stats about our cache
				if sd.cacheStats != nil {
					stats := sd.cacheStats.Stats(true)
					sd.client.Inc("cache.size", stats.Size)
					sd.client.Inc("cache.hit", stats.Hit)
					sd.client.Inc("cache.miss", stats.Miss)
				}
			case <-sd.done:
				tick.Stop()
				sd.client.Close()
				return
			}
		}
	}()

	return nil
}

func (sd *StatsdMetrics) Stop() {
	if sd.done != nil {
		close(sd.done)
	}
}

func (sd *StatsdMetrics) HandleRPC(ctx context.Context, s stats.RPCStats) {
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
		}
		sd.reqChan <- rs
	}
}

func (sd *StatsdMetrics) GRPCStatsHandler() stats.Handler                   { return sd }
func (sd *StatsdMetrics) HandleConn(ctx context.Context, s stats.ConnStats) {}
func (sd *StatsdMetrics) RegisterCacheStats(c cache.CacheStats)             { sd.cacheStats = c }

func (sd *StatsdMetrics) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

func (sd *StatsdMetrics) TagRPC(ctx context.Context, tagInfo *stats.RPCTagInfo) context.Context {
	return ContextWithStats(ctx, &RequestStats{Method: tagInfo.FullMethodName})
}
