package metrics

import (
	"context"
	"fmt"
	"github.com/mailgun/gubernator/cache"
	"strings"
	"time"

	"github.com/mailgun/holster"
	"github.com/mailgun/holster/clock"
	"github.com/sirupsen/logrus"
	"github.com/smira/go-statsd"
	"google.golang.org/grpc/stats"
)

type StatsdClient interface {
	PrecisionTiming(string, time.Duration, ...statsd.Tag)
	Incr(string, int64, ...statsd.Tag)
	Close() error
}

type NullClient struct{}

func (n *NullClient) PrecisionTiming(string, time.Duration, ...statsd.Tag) {}
func (n *NullClient) Incr(string, int64, ...statsd.Tag)                    {}
func (n *NullClient) Close() error                                         { return nil }

type StatsdConfig struct {
	// The name of this server instance to be reported to statsd
	Name string
	// The stats flush interval
	FlushInterval clock.DurationJSON
	// The statsd service endpoint as `host:port`
	Endpoint string
}

type StatsdMetrics struct {
	reqChan    chan *RequestStats
	cacheStats cache.CacheStats
	done       chan struct{}
	conf       StatsdConfig
	client     StatsdClient
	log        *logrus.Entry
}

func NewStatsdMetrics(conf StatsdConfig) *StatsdMetrics {
	sd := StatsdMetrics{
		client: &NullClient{},
		conf:   conf,
		log:    logrus.WithField("category", "metrics"),
	}

	if conf.Endpoint != "" {
		prefix := fmt.Sprintf("gubernator.%v.", strings.Replace(conf.Name, ".", "_", -1))
		holster.SetDefault(&conf.FlushInterval.Duration, time.Second)

		sd.client = statsd.NewClient(conf.Endpoint,
			statsd.FlushInterval(conf.FlushInterval.Duration),
			statsd.MetricPrefix(prefix),
			statsd.Logger(sd.log))
		return nil
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
					method := k[strings.LastIndex(k, "|"):]
					sd.client.PrecisionTiming(fmt.Sprintf("api.%s.total", method), v.Duration)
					sd.client.Incr(fmt.Sprintf("api.%s.total", method), v.Called)
					sd.client.Incr(fmt.Sprintf("api.%s.failed", method), v.Failed)
					methods[k] = RequestStats{}
				}

				// Emit stats about our cache
				if sd.cacheStats != nil {
					stats := sd.cacheStats.Stats()
					sd.client.Incr("cache.size", stats.Size)
					sd.client.Incr("cache.hit", stats.Hit)
					sd.client.Incr("cache.miss", stats.Miss)
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

func (sd *StatsdMetrics) RegisterCacheStats(c cache.CacheStats) {
	sd.cacheStats = c
}

func (sd *StatsdMetrics) TagRPC(ctx context.Context, tagInfo *stats.RPCTagInfo) context.Context {
	return ContextWithStats(ctx, &RequestStats{Method: tagInfo.FullMethodName})
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

func (sd *StatsdMetrics) GRPCStatsHandler() stats.Handler { return sd }
func (sd *StatsdMetrics) TagConn(ctx context.Context, tagInfo *stats.ConnTagInfo) context.Context {
	return ctx
}
func (sd *StatsdMetrics) HandleConn(ctx context.Context, s stats.ConnStats) {}
