/*
Copyright 2018-2019 Mailgun Technologies Inc

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package gubernator_test

import (
	"context"
	"testing"

	guber "github.com/mailgun/gubernator"
	"github.com/mailgun/gubernator/cluster"
	"github.com/mailgun/holster/v3/syncutil"
)

func BenchmarkServer_GetPeerRateLimitNoBatching(b *testing.B) {
	conf := guber.Config{}
	if err := conf.SetDefaults(); err != nil {
		b.Errorf("SetDefaults err: %s", err)
	}

	client := guber.NewPeerClient(guber.PeerConfig{
		Info:     cluster.GetRandomPeer(),
		Behavior: conf.Behaviors,
	})

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
	client, err := guber.DialV1Server(cluster.GetRandomPeer().GRPCAddress, nil)
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
	client, err := guber.DialV1Server(cluster.GetRandomPeer().GRPCAddress, nil)
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
	client, err := guber.DialV1Server(cluster.GetRandomPeer().GRPCAddress, nil)
	if err != nil {
		b.Errorf("NewV1Client err: %s", err)
	}

	b.Run("ThunderingHeard", func(b *testing.B) {
		fan := syncutil.NewFanOut(100)
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
