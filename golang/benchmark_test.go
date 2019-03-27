package gubernator_test

import (
	"context"
	guber "github.com/mailgun/gubernator/golang"
	"github.com/mailgun/gubernator/golang/cluster"
	"github.com/mailgun/holster"
	"testing"
)

func BenchmarkServer_GetPeerRateLimitNoBatching(b *testing.B) {
	conf := guber.Config{}
	if err := conf.SetDefaults(); err != nil {
		b.Errorf("SetDefaults err: %s", err)
	}

	client, err := guber.NewPeerClient(conf.Behaviors, cluster.GetPeer())
	if err != nil {
		b.Errorf("NewPeerClient err: %s", err)
	}

	b.Run("GetPeerRateLimitNoBatching", func(b *testing.B) {
		for n := 0; n < b.N; n++ {
			_, err := client.GetPeerRateLimit(context.Background(), &guber.RateLimitReq{
				Name:      "get_peer_rate_limits_benchmark",
				UniqueKey: guber.RandomString(10),
				Behavior:  guber.Behavior_NO_BATCHING,
				Limit:     10,
				Duration:  5,
				Hits:      1,
			})
			if err != nil {
				b.Errorf("client.RateLimit() err: %s", err)
			}
		}
	})
}

func BenchmarkServer_GetRateLimit(b *testing.B) {
	client, err := guber.NewV1Client(cluster.GetPeer())
	if err != nil {
		b.Errorf("NewV1Client err: %s", err)
	}

	b.Run("GetRateLimit", func(b *testing.B) {
		for n := 0; n < b.N; n++ {
			_, err := client.GetRateLimits(context.Background(), &guber.GetRateLimitsReq{
				Requests: []*guber.RateLimitReq{
					{
						Name:      "get_rate_limit_benchmark",
						UniqueKey: guber.RandomString(10),
						Limit:     10,
						Duration:  guber.Second * 5,
						Hits:      1,
					},
				},
			})
			if err != nil {
				b.Errorf("client.RateLimit() err: %s", err)
			}
		}
	})
}

func BenchmarkServer_Ping(b *testing.B) {
	client, err := guber.NewV1Client(cluster.GetPeer())
	if err != nil {
		b.Errorf("NewV1Client err: %s", err)
	}

	//dur := time.Nanosecond * 117728
	//total := time.Second / dur
	//fmt.Printf("Total: %d\n", total)

	b.Run("HealthCheck", func(b *testing.B) {
		for n := 0; n < b.N; n++ {
			if _, err := client.HealthCheck(context.Background(), &guber.HealthCheckReq{}); err != nil {
				b.Errorf("client.HealthCheck() err: %s", err)
			}
		}
	})
}

/*func BenchmarkServer_GRPCGateway(b *testing.B) {
	for n := 0; n < b.N; n++ {
		_, err := http.Get("http://" + cluster.GetHTTPAddress() + "/v1/HealthCheck")
		if err != nil {
			b.Errorf("GRPCGateway() err: %s", err)
		}
	}
}*/

func BenchmarkServer_ThunderingHeard(b *testing.B) {
	client, err := guber.NewV1Client(cluster.GetPeer())
	if err != nil {
		b.Errorf("NewV1Client err: %s", err)
	}

	b.Run("ThunderingHeard", func(b *testing.B) {
		fan := holster.NewFanOut(100)
		for n := 0; n < b.N; n++ {
			fan.Run(func(o interface{}) error {
				_, err := client.GetRateLimits(context.Background(), &guber.GetRateLimitsReq{
					Requests: []*guber.RateLimitReq{
						{
							Name:      "get_rate_limit_benchmark",
							UniqueKey: guber.RandomString(10),
							Limit:     10,
							Duration:  guber.Second * 5,
							Hits:      1,
						},
					},
				})
				if err != nil {
					b.Errorf("client.RateLimit() err: %s", err)
				}
				return nil
			}, nil)
		}
	})
}
