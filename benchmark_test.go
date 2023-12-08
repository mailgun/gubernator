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
	"fmt"
	"os"
	"testing"

	guber "github.com/mailgun/gubernator/v3"
	"github.com/mailgun/gubernator/v3/cluster"
	"github.com/stretchr/testify/require"
)

// go test benchmark_test.go -bench=BenchmarkTrace -benchtime=20s -trace=trace.out
// go tool trace trace.out
func BenchmarkTrace(b *testing.B) {
	if err := cluster.StartWith([]guber.PeerInfo{
		{HTTPAddress: "127.0.0.1:9980", DataCenter: cluster.DataCenterNone},
		{HTTPAddress: "127.0.0.1:9981", DataCenter: cluster.DataCenterNone},
		{HTTPAddress: "127.0.0.1:9982", DataCenter: cluster.DataCenterNone},
		{HTTPAddress: "127.0.0.1:9983", DataCenter: cluster.DataCenterNone},
		{HTTPAddress: "127.0.0.1:9984", DataCenter: cluster.DataCenterNone},
		{HTTPAddress: "127.0.0.1:9985", DataCenter: cluster.DataCenterNone},

		// DataCenterOne
		{HTTPAddress: "127.0.0.1:9880", DataCenter: cluster.DataCenterOne},
		{HTTPAddress: "127.0.0.1:9881", DataCenter: cluster.DataCenterOne},
		{HTTPAddress: "127.0.0.1:9882", DataCenter: cluster.DataCenterOne},
		{HTTPAddress: "127.0.0.1:9883", DataCenter: cluster.DataCenterOne},
	}); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer cluster.Stop()
	ctx := context.Background()
	conf := guber.Config{}
	err := conf.SetDefaults()
	require.NoError(b, err, "Error in conf.SetDefaults")

	b.Run("CheckRateLimits() BATCHING", func(b *testing.B) {
		client, err := guber.NewClient(guber.WithNoTLS(cluster.GetRandomPeerInfo(cluster.DataCenterNone).HTTPAddress))
		require.NoError(b, err, "Error in guber.NewClient")

		b.ResetTimer()

		for n := 0; n < b.N; n++ {
			var resp guber.CheckRateLimitsResponse
			err := client.CheckRateLimits(ctx, &guber.CheckRateLimitsRequest{
				Requests: []*guber.RateLimitRequest{
					{
						Name:      "get_rate_limit_benchmark",
						UniqueKey: guber.RandomString(10),
						Limit:     10,
						Duration:  guber.Second * 5,
						Hits:      1,
					},
				},
			}, &resp)
			if err != nil {
				b.Errorf("Error in client.CheckRateLimits(): %s", err)
			}
		}
	})
}

//
// go test -bench=BenchmarkServer -benchmem=1  -benchtime=20s
//

func BenchmarkServer(b *testing.B) {
	if err := cluster.StartWith([]guber.PeerInfo{
		{HTTPAddress: "127.0.0.1:9980", DataCenter: cluster.DataCenterNone},
		{HTTPAddress: "127.0.0.1:9981", DataCenter: cluster.DataCenterNone},
		{HTTPAddress: "127.0.0.1:9982", DataCenter: cluster.DataCenterNone},
		{HTTPAddress: "127.0.0.1:9983", DataCenter: cluster.DataCenterNone},
		{HTTPAddress: "127.0.0.1:9984", DataCenter: cluster.DataCenterNone},
		{HTTPAddress: "127.0.0.1:9985", DataCenter: cluster.DataCenterNone},

		// DataCenterOne
		{HTTPAddress: "127.0.0.1:9880", DataCenter: cluster.DataCenterOne},
		{HTTPAddress: "127.0.0.1:9881", DataCenter: cluster.DataCenterOne},
		{HTTPAddress: "127.0.0.1:9882", DataCenter: cluster.DataCenterOne},
		{HTTPAddress: "127.0.0.1:9883", DataCenter: cluster.DataCenterOne},
	}); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer cluster.Stop()
	ctx := context.Background()
	conf := guber.Config{}
	err := conf.SetDefaults()
	require.NoError(b, err, "Error in conf.SetDefaults")

	//b.Run("Forward() NO_BATCHING", func(b *testing.B) {
	//	client, err := guber.NewPeer(guber.PeerConfig{
	//		Info:     cluster.GetRandomPeerInfo(cluster.DataCenterNone),
	//		Behavior: conf.Behaviors,
	//	})
	//	if err != nil {
	//		b.Errorf("during guber.NewPeer(): %s", err)
	//	}
	//
	//	b.ResetTimer()
	//
	//	for n := 0; n < b.N; n++ {
	//		_, err := client.Forward(context.Background(), &guber.RateLimitRequest{
	//			Name:      "get_peer_rate_limits_benchmark",
	//			UniqueKey: guber.RandomString(10),
	//			Behavior:  guber.Behavior_NO_BATCHING,
	//			Limit:     10,
	//			Duration:  5,
	//			Hits:      1,
	//		})
	//		if err != nil {
	//			b.Errorf("Error in client.Forward: %s", err)
	//		}
	//	}
	//})

	//b.Run("CheckRateLimits() BATCHING", func(b *testing.B) {
	//	client, err := guber.NewClient(guber.WithNoTLS(cluster.GetRandomPeerInfo(cluster.DataCenterNone).HTTPAddress))
	//	require.NoError(b, err, "Error in guber.NewClient")
	//
	//	b.ResetTimer()
	//
	//	for n := 0; n < b.N; n++ {
	//		var resp guber.CheckRateLimitsResponse
	//		err := client.CheckRateLimits(ctx, &guber.CheckRateLimitsRequest{
	//			Requests: []*guber.RateLimitRequest{
	//				{
	//					Name:      "get_rate_limit_benchmark",
	//					UniqueKey: guber.RandomString(10),
	//					Limit:     10,
	//					Duration:  guber.Second * 5,
	//					Hits:      1,
	//				},
	//			},
	//		}, &resp)
	//		if err != nil {
	//			b.Errorf("Error in client.CheckRateLimits(): %s", err)
	//		}
	//	}
	//})

	b.Run("CheckRateLimits() NO_BATCHING", func(b *testing.B) {
		client, err := guber.NewClient(guber.WithNoTLS(cluster.GetRandomPeerInfo(cluster.DataCenterNone).HTTPAddress))
		require.NoError(b, err, "Error in guber.NewClient")

		b.ResetTimer()

		for n := 0; n < b.N; n++ {
			var resp guber.CheckRateLimitsResponse
			err := client.CheckRateLimits(ctx, &guber.CheckRateLimitsRequest{
				Requests: []*guber.RateLimitRequest{
					{
						Name:      "get_rate_limit_benchmark",
						UniqueKey: guber.RandomString(10),
						Behavior:  guber.Behavior_NO_BATCHING,
						Limit:     10,
						Duration:  guber.Second * 5,
						Hits:      1,
					},
				},
			}, &resp)
			if err != nil {
				b.Errorf("Error in client.CheckRateLimits(): %s", err)
			}
		}
	})

	//b.Run("CheckRateLimits() GLOBAL", func(b *testing.B) {
	//	client, err := guber.NewClient(guber.WithNoTLS(cluster.GetRandomPeerInfo(cluster.DataCenterNone).HTTPAddress))
	//	require.NoError(b, err, "Error in guber.NewClient")
	//
	//	b.ResetTimer()
	//
	//	for n := 0; n < b.N; n++ {
	//		var resp guber.CheckRateLimitsResponse
	//		err := client.CheckRateLimits(context.Background(), &guber.CheckRateLimitsRequest{
	//			Requests: []*guber.RateLimitRequest{
	//				{
	//					Name:      "get_rate_limit_benchmark",
	//					UniqueKey: guber.RandomString(10),
	//					Behavior:  guber.Behavior_GLOBAL,
	//					Limit:     10,
	//					Duration:  guber.Second * 5,
	//					Hits:      1,
	//				},
	//			},
	//		}, &resp)
	//		if err != nil {
	//			b.Errorf("Error in client.CheckRateLimits: %s", err)
	//		}
	//	}
	//})

	//b.Run("HealthCheck", func(b *testing.B) {
	//	client, err := guber.NewClient(guber.WithNoTLS(cluster.GetRandomPeerInfo(cluster.DataCenterNone).HTTPAddress))
	//	require.NoError(b, err, "Error in guber.NewClient")
	//
	//	b.ResetTimer()
	//
	//	for n := 0; n < b.N; n++ {
	//		var resp guber.HealthCheckResponse
	//		if err := client.HealthCheck(context.Background(), &resp); err != nil {
	//			b.Errorf("Error in client.HealthCheck: %s", err)
	//		}
	//	}
	//})
	//
	//b.Run("Thundering herd", func(b *testing.B) {
	//	client, err := guber.NewClient(guber.WithNoTLS(cluster.GetRandomPeerInfo(cluster.DataCenterNone).HTTPAddress))
	//	require.NoError(b, err, "Error in guber.NewClient")
	//
	//	b.ResetTimer()
	//
	//	fan := syncutil.NewFanOut(100)
	//
	//	for n := 0; n < b.N; n++ {
	//		fan.Run(func(o interface{}) error {
	//			var resp guber.CheckRateLimitsResponse
	//			err := client.CheckRateLimits(context.Background(), &guber.CheckRateLimitsRequest{
	//				Requests: []*guber.RateLimitRequest{
	//					{
	//						Name:      "get_rate_limit_benchmark",
	//						UniqueKey: guber.RandomString(10),
	//						Limit:     10,
	//						Duration:  guber.Second * 5,
	//						Hits:      1,
	//					},
	//				},
	//			}, &resp)
	//			if err != nil {
	//				b.Errorf("Error in client.CheckRateLimits: %s", err)
	//			}
	//			return nil
	//		}, nil)
	//	}
	//})
}
