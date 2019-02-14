package metrics

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mailgun/gubernator/cache"
	"github.com/mailgun/holster"
	"github.com/sirupsen/logrus"
	"github.com/smira/go-statsd"
	"google.golang.org/grpc/stats"
)

type StatsdClient interface {
	Gauge(string, int64, ...statsd.Tag)
	Incr(string, int64, ...statsd.Tag)
	Close() error
}

type NullClient struct{}

func (n *NullClient) Gauge(string, int64, ...statsd.Tag) {}
func (n *NullClient) Incr(string, int64, ...statsd.Tag)  {}
func (n *NullClient) Close() error                       { return nil }

type StatsdMetrics struct {
	reqChan    chan *RequestStats
	cacheStats cache.CacheStats
	wg         holster.WaitGroup
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

func NewStatsdMetricsFromConf(conf Config) Collector {
	if conf.Host == "" || conf.Port == 0 {
		s := NewStatsdMetrics(&NullClient{})
		s.log.Info("Metrics config missing; metrics disabled")
		return s
	}

	if conf.Prefix == "" {
		hostname, _ := os.Hostname()
		normalizedHostname := strings.Replace(hostname, ".", "_", -1)
		conf.Prefix = fmt.Sprintf("gubernator.%v.", normalizedHostname)
	}

	log := logrus.WithField("category", "metrics")

	holster.SetDefault(&conf.Period.Duration, time.Second)

	client := statsd.NewClient(fmt.Sprintf("%v:%v", conf.Host, conf.Port),
		statsd.FlushInterval(conf.Period.Duration),
		statsd.MetricPrefix(conf.Prefix),
		statsd.Logger(log))

	return NewStatsdMetrics(client)
}

func (sd *StatsdMetrics) Start() error {
	sd.reqChan = make(chan *RequestStats, 10000)
	methods := make(map[string]*RequestStats)

	tick := time.NewTicker(time.Second)
	sd.wg.Until(func(done chan struct{}) bool {
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
				return true
			}
			stat.Called = 1
			methods[stat.Method] = stat
		case <-tick.C:
			// Emit stats about GRPC method calls
			for k, v := range methods {
				method := k[strings.LastIndex(k, "/")+1:]
				sd.client.Gauge(fmt.Sprintf("api.%s.duration", method), int64(v.Duration))
				sd.client.Incr(fmt.Sprintf("api.%s.total", method), v.Called)
				sd.client.Incr(fmt.Sprintf("api.%s.failed", method), v.Failed)
			}
			// Clear the current method stats
			methods = make(map[string]*RequestStats, len(methods))

			// Emit stats about our cache
			if sd.cacheStats != nil {
				stats := sd.cacheStats.Stats(true)
				sd.client.Gauge("cache.size", stats.Size)
				sd.client.Incr("cache.hit", stats.Hit)
				sd.client.Incr("cache.miss", stats.Miss)
			}
		case <-done:
			tick.Stop()
			sd.client.Close()
			return false
		}
		return true
	})
	return nil
}

func (sd *StatsdMetrics) Stop() {
	sd.wg.Stop()
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
