/*
Copyright 2018-2022 Mailgun Technologies Inc

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

	guber "github.com/mailgun/gubernator/v2"
	"github.com/mailgun/gubernator/v2/cluster"
	"github.com/mailgun/holster/v4/syncutil"
	"github.com/stretchr/testify/require"
)

func BenchmarkServer(b *testing.B) {
	ctx := context.Background()
	conf := guber.Config{}
	err := conf.SetDefaults()
	require.NoError(b, err, "Error in conf.SetDefaults")

	b.Run("GetPeerRateLimit() with no batching", func(b *testing.B) {
		client, err := guber.NewPeerClient(guber.PeerConfig{
			Info:     cluster.GetRandomPeer(cluster.DataCenterNone),
			Behavior: conf.Behaviors,
		})
		if err != nil {
			b.Errorf("Error building client: %s", err)
		}

		b.ResetTimer()

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
				b.Errorf("Error in client.GetPeerRateLimit: %s", err)
			}
		}
	})

	b.Run("GetRateLimit()", func(b *testing.B) {
		client, err := guber.DialV1Server(cluster.GetRandomPeer(cluster.DataCenterNone).GRPCAddress, nil)
		require.NoError(b, err, "Error in guber.DialV1Server")

		b.ResetTimer()

		for n := 0; n < b.N; n++ {
			_, err := client.GetRateLimits(ctx, &guber.GetRateLimitsReq{
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
				b.Errorf("Error in client.GetRateLimits(): %s", err)
			}
		}
	})

	b.Run("GetRateLimitGlobal()", func(b *testing.B) {
		client, err := guber.DialV1Server(cluster.GetRandomPeer(cluster.DataCenterNone).GRPCAddress, nil)
		require.NoError(b, err, "Error in guber.DialV1Server")

		b.ResetTimer()

		for n := 0; n < b.N; n++ {
			_, err := client.GetRateLimits(context.Background(), &guber.GetRateLimitsReq{
				Requests: []*guber.RateLimitReq{
					{
						Name:      "get_rate_limit_benchmark",
						UniqueKey: guber.RandomString(10),
						Behavior:  guber.Behavior_GLOBAL,
						Limit:     10,
						Duration:  guber.Second * 5,
						Hits:      1,
					},
				},
			})
			if err != nil {
				b.Errorf("Error in client.GetRateLimits: %s", err)
			}
		}
	})

	b.Run("HealthCheck", func(b *testing.B) {
		client, err := guber.DialV1Server(cluster.GetRandomPeer(cluster.DataCenterNone).GRPCAddress, nil)
		require.NoError(b, err, "Error in guber.DialV1Server")

		b.ResetTimer()

		for n := 0; n < b.N; n++ {
			if _, err := client.HealthCheck(context.Background(), &guber.HealthCheckReq{}); err != nil {
				b.Errorf("Error in client.HealthCheck: %s", err)
			}
		}
	})

	b.Run("Thundering herd", func(b *testing.B) {
		client, err := guber.DialV1Server(cluster.GetRandomPeer(cluster.DataCenterNone).GRPCAddress, nil)
		require.NoError(b, err, "Error in guber.DialV1Server")

		b.ResetTimer()

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
					b.Errorf("Error in client.GetRateLimits: %s", err)
				}
				return nil
			}, nil)
		}
	})
}
