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
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	guber "github.com/mailgun/gubernator"
	"github.com/mailgun/gubernator/cluster"
	"github.com/mailgun/holster/v3/clock"
	"github.com/mailgun/holster/v3/testutil"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Setup and shutdown the mock gubernator cluster for the entire test suite
func TestMain(m *testing.M) {
	if err := cluster.StartWith([]guber.PeerInfo{
		{GRPCAddress: "127.0.0.1:9990", HTTPAddress: "127.0.0.1:9980"},
		{GRPCAddress: "127.0.0.1:9991", HTTPAddress: "127.0.0.1:9981"},
		{GRPCAddress: "127.0.0.1:9992", HTTPAddress: "127.0.0.1:9982"},
		{GRPCAddress: "127.0.0.1:9993", HTTPAddress: "127.0.0.1:9983"},
		{GRPCAddress: "127.0.0.1:9994", HTTPAddress: "127.0.0.1:9984"},
		{GRPCAddress: "127.0.0.1:9995", HTTPAddress: "127.0.0.1:9985"},
	}); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer cluster.Stop()
	os.Exit(m.Run())
}

func TestOverTheLimit(t *testing.T) {
	client, errs := guber.DialV1Server(cluster.GetRandomPeer().GRPCAddress, nil)
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
					Duration:  guber.Second * 9,
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
	defer clock.Freeze(clock.Now()).Unfreeze()

	client, errs := guber.DialV1Server(cluster.GetRandomPeer().GRPCAddress, nil)
	require.Nil(t, errs)

	tests := []struct {
		Remaining int64
		Status    guber.Status
		Sleep     clock.Duration
	}{
		{
			Remaining: 1,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Duration(0),
		},
		{
			Remaining: 0,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Duration(clock.Millisecond * 100),
		},
		{
			Remaining: 1,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Duration(0),
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
		clock.Advance(test.Sleep)
	}
}

func TestLeakyBucket(t *testing.T) {
	defer clock.Freeze(clock.Now()).Unfreeze()

	client, errs := guber.DialV1Server(cluster.PeerAt(0).GRPCAddress, nil)
	require.Nil(t, errs)

	tests := []struct {
		Hits      int64
		Remaining int64
		Status    guber.Status
		Sleep     clock.Duration
	}{
		{
			Hits:      5,
			Remaining: 0,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Duration(0),
		},
		{
			Hits:      1,
			Remaining: 0,
			Status:    guber.Status_OVER_LIMIT,
			Sleep:     clock.Millisecond * 100,
		},
		{
			Hits:      1,
			Remaining: 0,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Millisecond * 400,
		},
		{
			Hits:      1,
			Remaining: 4,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Duration(0),
		},
	}

	for i, test := range tests {
		resp, err := client.GetRateLimits(context.Background(), &guber.GetRateLimitsReq{
			Requests: []*guber.RateLimitReq{
				{
					Name:      "test_leaky_bucket",
					UniqueKey: "account:1234",
					Algorithm: guber.Algorithm_LEAKY_BUCKET,
					Duration:  guber.Millisecond * 300,
					Hits:      test.Hits,
					Limit:     5,
				},
			},
		})
		clock.Freeze(clock.Now())
		require.NoError(t, err)

		rl := resp.Responses[0]

		assert.Equal(t, test.Status, rl.Status, i)
		assert.Equal(t, test.Remaining, rl.Remaining, i)
		assert.Equal(t, int64(5), rl.Limit, i)
		assert.True(t, rl.ResetTime != 0)
		clock.Advance(test.Sleep)
	}
}

func TestMissingFields(t *testing.T) {
	client, errs := guber.DialV1Server(cluster.GetRandomPeer().GRPCAddress, nil)
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
	peer := cluster.PeerAt(0).GRPCAddress
	client, errs := guber.DialV1Server(peer, nil)
	require.NoError(t, errs)

	sendHit := func(status guber.Status, remain int64, i int) string {
		ctx, cancel := context.WithTimeout(context.Background(), clock.Second*5)
		defer cancel()
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
		require.NoError(t, err, i)
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

	testutil.UntilPass(t, 20, clock.Millisecond*200, func(t testutil.TestingT) {
		// Inspect our metrics, ensure they collected the counts we expected during this test
		d := cluster.DaemonAt(0)
		config := d.Config()
		resp, err := http.Get(fmt.Sprintf("http://%s/metrics", config.HTTPListenAddress))
		if !assert.NoError(t, err) {
			return
		}
		defer resp.Body.Close()

		m := getMetric(t, resp.Body, "gubernator_async_durations_count")
		assert.NotEqual(t, 0, int(m.Value))

		// V1Instance 2 should be the owner of our global rate limit
		d = cluster.DaemonAt(2)
		config = d.Config()
		resp, err = http.Get(fmt.Sprintf("http://%s/metrics", config.HTTPListenAddress))
		if !assert.NoError(t, err) {
			return
		}
		defer resp.Body.Close()

		m = getMetric(t, resp.Body, "gubernator_broadcast_durations_count")
		assert.NotEqual(t, 0, int(m.Value))
	})
}

func TestChangeLimit(t *testing.T) {
	client, errs := guber.DialV1Server(cluster.GetRandomPeer().GRPCAddress, nil)
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
						Duration:  guber.Millisecond * 9000,
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
	client, errs := guber.DialV1Server(cluster.GetRandomPeer().GRPCAddress, nil)
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
						Duration:  guber.Millisecond * 9000,
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
	client, err := guber.DialV1Server(cluster.DaemonAt(0).GRPCListener.Addr().String(), nil)
	require.NoError(t, err)

	// Check that the cluster is healthy to start with
	healthResp, err := client.HealthCheck(context.Background(), &guber.HealthCheckReq{})
	require.NoError(t, err)

	require.Equal(t, "healthy", healthResp.GetStatus())

	// Create a global rate limit that will need to be sent to all peers in the cluster
	_, err = client.GetRateLimits(context.Background(), &guber.GetRateLimitsReq{
		Requests: []*guber.RateLimitReq{
			{
				Name:      "test_health_check",
				UniqueKey: "account:12345",
				Algorithm: guber.Algorithm_TOKEN_BUCKET,
				Behavior:  guber.Behavior_BATCHING,
				Duration:  guber.Second * 3,
				Hits:      1,
				Limit:     5,
			},
		},
	})
	require.Nil(t, err)

	// Stop the rest of the cluster to ensure errors occur on our instance and
	// collect daemons to restart the stopped peers after the test completes
	var daemons []*guber.Daemon
	for i := 1; i < cluster.NumOfDaemons(); i++ {
		d := cluster.DaemonAt(i)
		require.NotNil(t, d)
		d.Close()
		daemons = append(daemons, d)
	}

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

	testutil.UntilPass(t, 20, clock.Millisecond*300, func(t testutil.TestingT) {
		// Check the health again to get back the connection error
		healthResp, err = client.HealthCheck(context.Background(), &guber.HealthCheckReq{})
		if assert.Nil(t, err) {
			return
		}

		assert.Equal(t, "unhealthy", healthResp.GetStatus())
		assert.Contains(t, healthResp.GetMessage(), "connect: connection refused")
	})

	// Restart stopped instances
	ctx, cancel := context.WithTimeout(context.Background(), clock.Second*15)
	defer cancel()
	cluster.Restart(ctx)
}

func TestLeakyBucketDivBug(t *testing.T) {
	defer clock.Freeze(clock.Now()).Unfreeze()

	client, err := guber.DialV1Server(cluster.GetRandomPeer().GRPCAddress, nil)
	require.NoError(t, err)

	resp, err := client.GetRateLimits(context.Background(), &guber.GetRateLimitsReq{
		Requests: []*guber.RateLimitReq{
			{
				Name:      "test_leaky_bucket_div",
				UniqueKey: "account:12345",
				Algorithm: guber.Algorithm_LEAKY_BUCKET,
				Duration:  guber.Millisecond * 1000,
				Hits:      1,
				Limit:     2000,
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "", resp.Responses[0].Error)
	assert.Equal(t, guber.Status_UNDER_LIMIT, resp.Responses[0].Status)
	assert.Equal(t, int64(1999), resp.Responses[0].Remaining)
	assert.Equal(t, int64(2000), resp.Responses[0].Limit)

	// Should result in a rate of 0.5
	resp, err = client.GetRateLimits(context.Background(), &guber.GetRateLimitsReq{
		Requests: []*guber.RateLimitReq{
			{
				Name:      "test_leaky_bucket_div",
				UniqueKey: "account:12345",
				Algorithm: guber.Algorithm_LEAKY_BUCKET,
				Duration:  guber.Millisecond * 1000,
				Hits:      100,
				Limit:     2000,
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1899), resp.Responses[0].Remaining)
	assert.Equal(t, int64(2000), resp.Responses[0].Limit)
}

func TestGRPCGateway(t *testing.T) {
	resp, err := http.DefaultClient.Get("http://" + cluster.GetRandomPeer().HTTPAddress + "/v1/HealthCheck")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TODO: Add a test for sending no rate limits RateLimitReqList.RateLimits = nil

func getMetric(t testutil.TestingT, in io.Reader, name string) *model.Sample {
	dec := expfmt.SampleDecoder{
		Dec: expfmt.NewDecoder(in, expfmt.FmtText),
		Opts: &expfmt.DecodeOptions{
			Timestamp: model.Now(),
		},
	}

	var all model.Vector
	for {
		var smpls model.Vector
		err := dec.Decode(&smpls)
		if err == io.EOF {
			break
		}
		assert.NoError(t, err)
		all = append(all, smpls...)
	}

	for _, s := range all {
		if strings.Contains(s.Metric.String(), name) {
			return s
		}
	}
	return nil
}
