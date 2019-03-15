package gubernator_test

import (
	"context"
	"github.com/mailgun/gubernator/golang"
	"github.com/mailgun/gubernator/golang/cluster"
	"github.com/mailgun/gubernator/golang/pb"
	"github.com/mailgun/holster"
	"net/http"
	"testing"
	"time"
)

func BenchmarkServer_GetRateLimitByKey(b *testing.B) {
	client, err := gubernator.NewPeerClient(cluster.GetPeer())
	if err != nil {
		b.Errorf("NewPeerClient err: %s", err)
	}

	b.Run("GetPeerRateLimits", func(b *testing.B) {
		for n := 0; n < b.N; n++ {
			_, err := client.GetPeerRateLimits(context.Background(), &pb.RateLimitRequest{
				Namespace: "get_peer_rate_limits_benchmark",
				UniqueKey: gubernator.RandomString(10),
				RateLimitConfig: &pb.RateLimitConfig{
					Limit:    10,
					Duration: 5,
				},
				Hits: 1,
			})
			if err != nil {
				b.Errorf("client.RateLimit() err: %s", err)
			}
		}
	})
}

func BenchmarkServer_GetRateLimit(b *testing.B) {
	client, err := gubernator.NewClient(cluster.GetPeer())
	if err != nil {
		b.Errorf("NewClient err: %s", err)
	}

	b.Run("GetRateLimit", func(b *testing.B) {
		for n := 0; n < b.N; n++ {
			_, err := client.GetRateLimit(context.Background(), &gubernator.Request{
				Namespace: "get_rate_limit_benchmark",
				UniqueKey: gubernator.RandomString(10),
				Limit:     10,
				Duration:  time.Second * 5,
				Hits:      1,
			})
			if err != nil {
				b.Errorf("client.RateLimit() err: %s", err)
			}
		}
	})
}

func BenchmarkServer_Ping(b *testing.B) {
	client, err := gubernator.NewClient(cluster.GetPeer())
	if err != nil {
		b.Errorf("NewClient err: %s", err)
	}

	//dur := time.Nanosecond * 117728
	//total := time.Second / dur
	//fmt.Printf("Total: %d\n", total)

	b.Run("HealthCheck", func(b *testing.B) {
		for n := 0; n < b.N; n++ {
			if err := client.Ping(context.Background()); err != nil {
				b.Errorf("client.HealthCheck() err: %s", err)
			}
		}
	})
}

func BenchmarkServer_GRPCGateway(b *testing.B) {
	for n := 0; n < b.N; n++ {
		_, err := http.Get("http://" + cluster.GetHTTPAddress() + "/v1/HealthCheck")
		if err != nil {
			b.Errorf("GRPCGateway() err: %s", err)
		}
	}
}

// TODO: Benchmark with fanout to simulate thundering heard of simultaneous requests from many clients
func BenchmarkServer_ThunderingHeard(b *testing.B) {
	client, err := gubernator.NewClient(cluster.GetPeer())
	if err != nil {
		b.Errorf("NewClient err: %s", err)
	}

	b.Run("ThunderingHeard", func(b *testing.B) {
		fan := holster.NewFanOut(100)
		for n := 0; n < b.N; n++ {
			fan.Run(func(o interface{}) error {
				_, err := client.GetRateLimit(context.Background(), &gubernator.Request{
					Namespace: "get_rate_limit_benchmark",
					UniqueKey: gubernator.RandomString(10),
					Limit:     10,
					Duration:  time.Second * 5,
					Hits:      1,
				})
				if err != nil {
					b.Errorf("client.RateLimit() err: %s", err)
				}
				return nil
			}, nil)
		}
	})
}
