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
	"fmt"
	"os"
	"testing"
	"time"

	guber "github.com/mailgun/gubernator"
	"github.com/mailgun/gubernator/cluster"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Setup and shutdown the mock gubernator cluster for the entire test suite
func TestMain(m *testing.M) {
	if err := cluster.StartWith([]string{
		"127.0.0.1:9990",
		"127.0.0.1:9991",
		"127.0.0.1:9992",
		"127.0.0.1:9993",
		"127.0.0.1:9994",
		"127.0.0.1:9995",
	}); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer cluster.Stop()
	os.Exit(m.Run())
}

func TestOverTheLimit(t *testing.T) {
	client, errs := guber.DialV1Server(cluster.GetRandomPeer().Address)
	require.Nil(t, errs)

	tests := []struct {
		Remaining int64
		Status    guber.Status
	}{
		{
			Remaining: 1,
			Status:    guber.Status_UNDER_LIMIT,
		},
		{
			Remaining: 0,
			Status:    guber.Status_UNDER_LIMIT,
		},
		{
			Remaining: 0,
			Status:    guber.Status_OVER_LIMIT,
		},
	}

	for _, test := range tests {
		resp, err := client.GetRateLimits(context.Background(), &guber.GetRateLimitsReq{
			Requests: []*guber.RateLimitReq{
				{
					Name:      "test_over_limit",
					UniqueKey: "account:1234",
					Algorithm: guber.Algorithm_TOKEN_BUCKET,
					Duration:  guber.Second,
					Limit:     2,
					Hits:      1,
					Behavior:  0,
				},
			},
		})
		require.Nil(t, err)

		rl := resp.Responses[0]

		assert.Equal(t, test.Status, rl.Status)
		assert.Equal(t, test.Remaining, rl.Remaining)
		assert.Equal(t, int64(2), rl.Limit)
		assert.True(t, rl.ResetTime != 0)
	}
}

func TestTokenBucket(t *testing.T) {
	client, errs := guber.DialV1Server(cluster.GetRandomPeer().Address)
	require.Nil(t, errs)

	tests := []struct {
		Remaining int64
		Status    guber.Status
		Sleep     time.Duration
	}{
		{
			Remaining: 1,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     time.Duration(0),
		},
		{
			Remaining: 0,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     time.Duration(time.Millisecond * 5),
		},
		{
			Remaining: 1,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     time.Duration(0),
		},
	}

	for _, test := range tests {
		resp, err := client.GetRateLimits(context.Background(), &guber.GetRateLimitsReq{
			Requests: []*guber.RateLimitReq{
				{
					Name:      "test_token_bucket",
					UniqueKey: "account:1234",
					Algorithm: guber.Algorithm_TOKEN_BUCKET,
					Duration:  guber.Millisecond * 5,
					Limit:     2,
					Hits:      1,
				},
			},
		})
		require.Nil(t, err)

		rl := resp.Responses[0]

		assert.Empty(t, rl.Error)
		assert.Equal(t, test.Status, rl.Status)
		assert.Equal(t, test.Remaining, rl.Remaining)
		assert.Equal(t, int64(2), rl.Limit)
		assert.True(t, rl.ResetTime != 0)
		time.Sleep(test.Sleep)
	}
}

func TestLeakyBucket(t *testing.T) {
	client, errs := guber.DialV1Server(cluster.GetRandomPeer().Address)
	require.Nil(t, errs)

	tests := []struct {
		Hits      int64
		Remaining int64
		Status    guber.Status
		Sleep     time.Duration
	}{
		{
			Hits:      5,
			Remaining: 0,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     time.Duration(0),
		},
		{
			Hits:      1,
			Remaining: 0,
			Status:    guber.Status_OVER_LIMIT,
			Sleep:     time.Duration(time.Millisecond * 10),
		},
		{
			Hits:      1,
			Remaining: 0,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     time.Duration(time.Millisecond * 20),
		},
		{
			Hits:      1,
			Remaining: 1,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     time.Duration(0),
		},
	}

	for i, test := range tests {
		resp, err := client.GetRateLimits(context.Background(), &guber.GetRateLimitsReq{
			Requests: []*guber.RateLimitReq{
				{
					Name:      "test_leaky_bucket",
					UniqueKey: "account:1234",
					Algorithm: guber.Algorithm_LEAKY_BUCKET,
					Duration:  guber.Millisecond * 50,
					Hits:      test.Hits,
					Limit:     5,
				},
			},
		})
		require.Nil(t, err)

		rl := resp.Responses[0]

		assert.Equal(t, test.Status, rl.Status, i)
		assert.Equal(t, test.Remaining, rl.Remaining, i)
		assert.Equal(t, int64(5), rl.Limit, i)
		assert.True(t, rl.ResetTime != 0)
		time.Sleep(test.Sleep)
	}
}

func TestMissingFields(t *testing.T) {
	client, errs := guber.DialV1Server(cluster.GetRandomPeer().Address)
	require.Nil(t, errs)

	tests := []struct {
		Req    guber.RateLimitReq
		Status guber.Status
		Error  string
	}{
		{
			Req: guber.RateLimitReq{
				Name:      "test_missing_fields",
				UniqueKey: "account:1234",
				Hits:      1,
				Limit:     10,
				Duration:  0,
			},
			Error:  "", // No Error
			Status: guber.Status_UNDER_LIMIT,
		},
		{
			Req: guber.RateLimitReq{
				Name:      "test_missing_fields",
				UniqueKey: "account:12345",
				Hits:      1,
				Duration:  10000,
				Limit:     0,
			},
			Error:  "", // No Error
			Status: guber.Status_OVER_LIMIT,
		},
		{
			Req: guber.RateLimitReq{
				UniqueKey: "account:1234",
				Hits:      1,
				Duration:  10000,
				Limit:     5,
			},
			Error:  "field 'namespace' cannot be empty",
			Status: guber.Status_UNDER_LIMIT,
		},
		{
			Req: guber.RateLimitReq{
				Name:     "test_missing_fields",
				Hits:     1,
				Duration: 10000,
				Limit:    5,
			},
			Error:  "field 'unique_key' cannot be empty",
			Status: guber.Status_UNDER_LIMIT,
		},
	}

	for i, test := range tests {
		resp, err := client.GetRateLimits(context.Background(), &guber.GetRateLimitsReq{
			Requests: []*guber.RateLimitReq{&test.Req},
		})
		require.Nil(t, err)
		assert.Equal(t, test.Error, resp.Responses[0].Error, i)
		assert.Equal(t, test.Status, resp.Responses[0].Status, i)
	}
}

func TestGlobalRateLimits(t *testing.T) {
	const clientInstance = 1
	peer := cluster.PeerAt(clientInstance)
	client, errs := guber.DialV1Server(peer.Address)
	require.Nil(t, errs)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()

	sendHit := func(status guber.Status, remain int64, i int) string {
		resp, err := client.GetRateLimits(ctx, &guber.GetRateLimitsReq{
			Requests: []*guber.RateLimitReq{
				{
					Name:      "test_global",
					UniqueKey: "account:12345",
					Algorithm: guber.Algorithm_TOKEN_BUCKET,
					Behavior:  guber.Behavior_GLOBAL,
					Duration:  guber.Second * 3,
					Hits:      1,
					Limit:     5,
				},
			},
		})
		require.Nil(t, err, i)
		assert.Equal(t, "", resp.Responses[0].Error, i)
		assert.Equal(t, status, resp.Responses[0].Status, i)
		assert.Equal(t, remain, resp.Responses[0].Remaining, i)
		assert.Equal(t, int64(5), resp.Responses[0].Limit, i)

		// ensure that we have a canonical host
		assert.NotEmpty(t, resp.Responses[0].Metadata["owner"])

		// name/key should ensure our connected peer is NOT the owner,
		// the peer we are connected to should forward requests asynchronously to the owner.
		assert.NotEqual(t, peer, resp.Responses[0].Metadata["owner"])

		return resp.Responses[0].Metadata["owner"]
	}

	// Our first hit should create the request on the peer and queue for async forward
	sendHit(guber.Status_UNDER_LIMIT, 4, 1)

	// Our second should be processed as if we own it since the async forward hasn't occurred yet
	sendHit(guber.Status_UNDER_LIMIT, 3, 2)

	time.Sleep(time.Second)

	// After sleeping this response should be from the updated async call from our owner. Notice the
	// remaining is still 3 as the hit is queued for update to the owner
	canonicalHost := sendHit(guber.Status_UNDER_LIMIT, 3, 3)

	canonicalInstance := cluster.InstanceForHost(canonicalHost)

	// Inspect our metrics, ensure they collected the counts we expected during this test
	instance := cluster.InstanceForHost(peer.Address)

	metricCh := make(chan prometheus.Metric, 5)
	instance.Guber.Collect(metricCh)

	buf := dto.Metric{}
	m := <-metricCh // Async metric
	assert.Nil(t, m.Write(&buf))
	assert.Equal(t, uint64(1), *buf.Histogram.SampleCount)

	metricCh = make(chan prometheus.Metric, 5)
	canonicalInstance.Guber.Collect(metricCh)

	m = <-metricCh // Async metric
	m = <-metricCh // Broadcast metric
	assert.Nil(t, m.Write(&buf))
	assert.Equal(t, uint64(1), *buf.Histogram.SampleCount)
}

func TestChangeLimit(t *testing.T) {
	client, errs := guber.DialV1Server(cluster.GetRandomPeer().Address)
	require.Nil(t, errs)

	tests := []struct {
		Remaining int64
		Algorithm guber.Algorithm
		Status    guber.Status
		Name      string
		Limit     int64
	}{
		{
			Name:      "Should subtract 1 from remaining",
			Algorithm: guber.Algorithm_TOKEN_BUCKET,
			Status:    guber.Status_UNDER_LIMIT,
			Remaining: 99,
			Limit:     100,
		},
		{
			Name:      "Should subtract 1 from remaining",
			Algorithm: guber.Algorithm_TOKEN_BUCKET,
			Status:    guber.Status_UNDER_LIMIT,
			Remaining: 98,
			Limit:     100,
		},
		{
			Name:      "Should subtract 1 from remaining and change limit to 10",
			Algorithm: guber.Algorithm_TOKEN_BUCKET,
			Status:    guber.Status_UNDER_LIMIT,
			Remaining: 7,
			Limit:     10,
		},
		{
			Name:      "Should subtract 1 from remaining with new limit of 10",
			Algorithm: guber.Algorithm_TOKEN_BUCKET,
			Status:    guber.Status_UNDER_LIMIT,
			Remaining: 6,
			Limit:     10,
		},
		{
			Name:      "Should subtract 1 from remaining with new limit of 200",
			Algorithm: guber.Algorithm_TOKEN_BUCKET,
			Status:    guber.Status_UNDER_LIMIT,
			Remaining: 195,
			Limit:     200,
		},
		{
			Name:      "Should subtract 1 from remaining for leaky bucket",
			Algorithm: guber.Algorithm_LEAKY_BUCKET,
			Status:    guber.Status_UNDER_LIMIT,
			Remaining: 99,
			Limit:     100,
		},
		{
			Name:      "Should subtract 1 from remaining for leaky bucket after limit change",
			Algorithm: guber.Algorithm_LEAKY_BUCKET,
			Status:    guber.Status_UNDER_LIMIT,
			Remaining: 9,
			Limit:     10,
		},
		{
			Name:      "Should subtract 1 from remaining for leaky bucket with new limit",
			Algorithm: guber.Algorithm_LEAKY_BUCKET,
			Status:    guber.Status_UNDER_LIMIT,
			Remaining: 8,
			Limit:     10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.Name, func(t *testing.T) {
			resp, err := client.GetRateLimits(context.Background(), &guber.GetRateLimitsReq{
				Requests: []*guber.RateLimitReq{
					{
						Name:      "test_change_limit",
						UniqueKey: "account:1234",
						Algorithm: tt.Algorithm,
						Duration:  guber.Millisecond * 100,
						Limit:     tt.Limit,
						Hits:      1,
					},
				},
			})
			require.Nil(t, err)

			rl := resp.Responses[0]

			assert.Equal(t, tt.Status, rl.Status)
			assert.Equal(t, tt.Remaining, rl.Remaining)
			assert.Equal(t, tt.Limit, rl.Limit)
			assert.True(t, rl.ResetTime != 0)
		})
	}
}

func TestResetRemaining(t *testing.T) {
	client, errs := guber.DialV1Server(cluster.GetRandomPeer().Address)
	require.Nil(t, errs)

	tests := []struct {
		Remaining int64
		Algorithm guber.Algorithm
		Behavior  guber.Behavior
		Status    guber.Status
		Name      string
		Limit     int64
	}{
		{
			Name:      "Should subtract 1 from remaining",
			Algorithm: guber.Algorithm_TOKEN_BUCKET,
			Status:    guber.Status_UNDER_LIMIT,
			Behavior:  guber.Behavior_BATCHING,
			Remaining: 99,
			Limit:     100,
		},
		{
			Name:      "Should subtract 2 from remaining",
			Algorithm: guber.Algorithm_TOKEN_BUCKET,
			Status:    guber.Status_UNDER_LIMIT,
			Behavior:  guber.Behavior_BATCHING,
			Remaining: 98,
			Limit:     100,
		},
		{
			Name:      "Should reset the remaining",
			Algorithm: guber.Algorithm_TOKEN_BUCKET,
			Status:    guber.Status_UNDER_LIMIT,
			Behavior:  guber.Behavior_RESET_REMAINING,
			Remaining: 100,
			Limit:     100,
		},
		{
			Name:      "Should subtract 1 from remaining after reset",
			Algorithm: guber.Algorithm_TOKEN_BUCKET,
			Status:    guber.Status_UNDER_LIMIT,
			Behavior:  guber.Behavior_BATCHING,
			Remaining: 99,
			Limit:     100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.Name, func(t *testing.T) {
			resp, err := client.GetRateLimits(context.Background(), &guber.GetRateLimitsReq{
				Requests: []*guber.RateLimitReq{
					{
						Name:      "test_reset_remaining",
						UniqueKey: "account:1234",
						Algorithm: tt.Algorithm,
						Duration:  guber.Millisecond * 100,
						Behavior:  tt.Behavior,
						Limit:     tt.Limit,
						Hits:      1,
					},
				},
			})
			require.Nil(t, err)

			rl := resp.Responses[0]

			assert.Equal(t, tt.Status, rl.Status)
			assert.Equal(t, tt.Remaining, rl.Remaining)
			assert.Equal(t, tt.Limit, rl.Limit)
		})
	}
}

func TestHealthCheck(t *testing.T) {
	client, errs := guber.DialV1Server(cluster.InstanceAt(0).Address)
	require.Nil(t, errs)

	// Check that the cluster is healthy to start with
	healthResp, err := client.HealthCheck(context.Background(), &guber.HealthCheckReq{})
	require.Nil(t, err)

	assert.Equal(t, "healthy", healthResp.GetStatus())

	// Create a global rate limit that will need to be sent to all peers in the cluster
	_, err = client.GetRateLimits(context.Background(), &guber.GetRateLimitsReq{
		Requests: []*guber.RateLimitReq{
			{
				Name:      "test_health_check",
				UniqueKey: "account:12345",
				Algorithm: guber.Algorithm_TOKEN_BUCKET,
				Behavior:  guber.Behavior_GLOBAL,
				Duration:  guber.Second * 3,
				Hits:      1,
				Limit:     5,
			},
		},
	})
	require.Nil(t, err)

	// Stop the rest of the cluster to ensure errors occur on our instance and
	// collect addresses to restart the stopped instances after the test completes
	var addresses []string
	for i := 1; i < cluster.NumOfInstances(); i++ {
		addresses = append(addresses, cluster.InstanceAt(i).Address)
		cluster.StopInstanceAt(i)
	}
	time.Sleep(time.Second)

	// Hit the global rate limit again this time causing a connection error
	_, err = client.GetRateLimits(context.Background(), &guber.GetRateLimitsReq{
		Requests: []*guber.RateLimitReq{
			{
				Name:      "test_health_check",
				UniqueKey: "account:12345",
				Algorithm: guber.Algorithm_TOKEN_BUCKET,
				Behavior:  guber.Behavior_GLOBAL,
				Duration:  guber.Second * 3,
				Hits:      1,
				Limit:     5,
			},
		},
	})
	require.Nil(t, err)

	// Check the health again to get back the connection error
	healthResp, err = client.HealthCheck(context.Background(), &guber.HealthCheckReq{})
	require.Nil(t, err)

	assert.Equal(t, "unhealthy", healthResp.GetStatus())
	assert.Contains(t, healthResp.GetMessage(), "connect: connection refused")

	// Restart stopped instances
	for i := 1; i < cluster.NumOfInstances(); i++ {
		cluster.StartInstance(addresses[i-1], cluster.GetDefaultConfig())
	}
}

func TestLeakyBucketDivBug(t *testing.T) {
	client, errs := guber.DialV1Server(cluster.GetRandomPeer().Address)
	require.Nil(t, errs)

	resp, err := client.GetRateLimits(context.Background(), &guber.GetRateLimitsReq{
		Requests: []*guber.RateLimitReq{
			{
				Name:      "test_leaky_bucket",
				UniqueKey: "account:1234",
				Algorithm: guber.Algorithm_LEAKY_BUCKET,
				Duration:  guber.Millisecond * 1000,
				Hits:      1,
				Limit:     2000,
			},
		},
	})
	assert.Equal(t, guber.Status_UNDER_LIMIT, resp.Responses[0].Status)
	assert.Equal(t, int64(1999), resp.Responses[0].Remaining)
	assert.Equal(t, int64(2000), resp.Responses[0].Limit)
	require.Nil(t, err)

	// Should result in a rate of 0.5
	resp, err = client.GetRateLimits(context.Background(), &guber.GetRateLimitsReq{
		Requests: []*guber.RateLimitReq{
			{
				Name:      "test_leaky_bucket",
				UniqueKey: "account:1234",
				Algorithm: guber.Algorithm_LEAKY_BUCKET,
				Duration:  guber.Millisecond * 1000,
				Hits:      100,
				Limit:     2000,
			},
		},
	})
	require.Nil(t, err)
	assert.Equal(t, int64(1900), resp.Responses[0].Remaining)
	assert.Equal(t, int64(2000), resp.Responses[0].Limit)
}

// TODO: Add a test for sending no rate limits RateLimitReqList.RateLimits = nil
