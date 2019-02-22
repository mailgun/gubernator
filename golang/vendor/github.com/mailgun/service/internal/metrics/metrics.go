package metrics

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mailgun/holster"
	"github.com/mailgun/holster/clock"
	"github.com/mailgun/service/internal/logging"
	"github.com/smira/go-statsd"
)

var clt Client = &devNullClient{}

func Clt() Client {
	return clt
}

type Config struct {
	Period clock.DurationJSON `json:"period"`
	Host   string             `json:"host"`
	Port   int                `json:"port"`
}

type Client interface {
	CloneWithPrefix(prefix string) Client
	CloneWithPrefixExtension(extension string) Client
	Close() error

	Inc(stat string, value int64)
	Dec(stat string, value int64)
	Gauge(stat string, value int64)
	GaugeDelta(stat string, value int64)
	Timing(stat string, delta time.Duration)
	PrecisionTiming(stat string, delta time.Duration)
	UniqueString(stat string, value string)

	TrackRequest(stat string, status int, time time.Duration)
	TrackRequestTime(stat string, time time.Duration)
	TrackTotalRequests(stat string)
	TrackFailedRequests(stat string, status int)

	Stopwatch() *Stopwatch
}

type client struct {
	statsdClt *statsd.Client
}

func (c *client) CloneWithPrefix(prefix string) Client {
	return &client{statsdClt: c.statsdClt.CloneWithPrefix(prefix)}
}

func (c *client) CloneWithPrefixExtension(extension string) Client {
	return &client{statsdClt: c.statsdClt.CloneWithPrefixExtension(extension)}
}

func (c *client) Close() error {
	return c.statsdClt.Close()
}

func (c *client) Inc(stat string, value int64) {
	c.statsdClt.Incr(stat, value)
}

func (c *client) Dec(stat string, value int64) {
	c.statsdClt.Decr(stat, value)
}

func (c *client) Gauge(stat string, value int64) {
	c.statsdClt.Gauge(stat, value)
}

func (c *client) GaugeDelta(stat string, value int64) {
	c.statsdClt.GaugeDelta(stat, value)
}

func (c *client) Timing(stat string, delta time.Duration) {
	c.statsdClt.Timing(stat, int64(delta/time.Millisecond))
}

func (c *client) PrecisionTiming(stat string, delta time.Duration) {
	c.statsdClt.PrecisionTiming(stat, delta)
}

func (c *client) UniqueString(stat string, value string) {
	c.statsdClt.SetAdd(stat, value)
}

func (c *client) TrackRequest(stat string, status int, time time.Duration) {
	c.TrackRequestTime(stat, time)
	c.TrackTotalRequests(stat)
	if status != http.StatusOK {
		c.TrackFailedRequests(stat, status)
	}
}

func (c *client) TrackRequestTime(stat string, time time.Duration) {
	c.PrecisionTiming(fmt.Sprintf("api.%v.time", stat), time)
}

func (c *client) TrackTotalRequests(stat string) {
	c.Inc(fmt.Sprintf("api.%v.count.total", stat), 1)
}

func (c *client) TrackFailedRequests(stat string, status int) {
	c.Inc(fmt.Sprintf("api.%v.count.failed.%v", stat, status), 1)
}

func (c *client) Stopwatch() *Stopwatch {
	return NewStopwatch(c)
}

func Init(cfg Config, appName, hostname string) {
	if cfg.Host == "" || cfg.Port == 0 {
		return
	}
	endpoint := fmt.Sprintf("%v:%v", cfg.Host, cfg.Port)

	normalizedHostname := strings.Replace(hostname, ".", "_", -1)
	prefix := fmt.Sprintf("%s.%v.", appName, normalizedHostname)

	holster.SetDefault(&cfg.Period.Duration, time.Second)

	statsdClt := statsd.NewClient(endpoint,
		statsd.FlushInterval(cfg.Period.Duration),
		statsd.MetricPrefix(prefix),
		statsd.Logger(logging.Log()))
	clt = &client{statsdClt: statsdClt}
}

type devNullClient struct{}

func (c *devNullClient) CloneWithPrefix(prefix string) Client                     { return c }
func (c *devNullClient) CloneWithPrefixExtension(extension string) Client         { return c }
func (c *devNullClient) Close() error                                             { return nil }
func (c *devNullClient) Inc(stat string, value int64)                             {}
func (c *devNullClient) Dec(stat string, value int64)                             {}
func (c *devNullClient) Gauge(stat string, value int64)                           {}
func (c *devNullClient) GaugeDelta(stat string, value int64)                      {}
func (c *devNullClient) Timing(stat string, delta time.Duration)                  {}
func (c *devNullClient) PrecisionTiming(stat string, delta time.Duration)         {}
func (c *devNullClient) UniqueString(stat string, value string)                   {}
func (c *devNullClient) TrackRequest(stat string, status int, time time.Duration) {}
func (c *devNullClient) TrackRequestTime(stat string, time time.Duration)         {}
func (c *devNullClient) TrackTotalRequests(stat string)                           {}
func (c *devNullClient) TrackFailedRequests(stat string, status int)              {}

func (c *devNullClient) Stopwatch() *Stopwatch {
	return NewStopwatch(c)
}

// Stopwatch is a convenience primitive to send function execution time as a
// metric in one line, e.g.:
//
//     func foo() {
//         defer service.Metrics().Stopwatch().Timing('foo')
//         // Do something useful here
//     }
//
type Stopwatch struct {
	c          Client
	start      time.Time
	checkpoint time.Time
}

// NewStopwatch creates a new Stopwatch that is using the specified client
// to send timing metrics.
func NewStopwatch(c Client) *Stopwatch {
	now := time.Now()
	return &Stopwatch{
		c:          c,
		start:      now,
		checkpoint: now,
	}
}

// Timing sends a timing metric with a time period since the stop watch was
// created.
func (sw *Stopwatch) Timing(stat string) {
	sw.c.Timing(stat, time.Since(sw.start))
}

// Checkpoint sends a timing metric with the specified name and a time period
// since the last time Checkpoint was called or if this is the first time the
// function is called, since the stop watch create time.
func (sw *Stopwatch) Checkpoint(stat string) {
	now := time.Now()
	sw.c.Timing(stat, now.Sub(sw.checkpoint))
	sw.checkpoint = now
}
