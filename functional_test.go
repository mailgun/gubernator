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
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	guber "github.com/mailgun/gubernator/v3"
	"github.com/mailgun/gubernator/v3/cluster"
	"github.com/mailgun/holster/v4/clock"
	"github.com/mailgun/holster/v4/testutil"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	json "google.golang.org/protobuf/encoding/protojson"
)

// Setup and shutdown the mock gubernator cluster for the entire test suite
func TestMain(m *testing.M) {
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
	os.Exit(m.Run())
}

func TestOverTheLimit(t *testing.T) {
	client, errs := guber.NewClient(cluster.GetRandomClientOptions(cluster.DataCenterNone))
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
		var resp guber.CheckRateLimitsResponse
		err := client.CheckRateLimits(context.Background(), &guber.CheckRateLimitsRequest{
			Requests: []*guber.RateLimitRequest{
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
		}, &resp)
		require.Nil(t, err)

		rl := resp.Responses[0]

		assert.Equal(t, "", rl.Error)
		assert.Equal(t, test.Status, rl.Status)
		assert.Equal(t, test.Remaining, rl.Remaining)
		assert.Equal(t, int64(2), rl.Limit)
		assert.True(t, rl.ResetTime != 0)
	}
}

// TestMultipleAsync tests a regression that occurred when a client requests multiple
// rate limits that are asynchronously sent to other nodes.
func TestMultipleAsync(t *testing.T) {
	// If the consistent hash changes or the number of peers changes, this might
	// need to be changed. We want the test to forward both rate limits to other
	// nodes in the cluster.

	t.Logf("Asking Peer: %s", cluster.GetPeers()[0].HTTPAddress)
	client, errs := guber.NewClient(guber.WithNoTLS(cluster.GetPeers()[0].HTTPAddress))
	require.Nil(t, errs)

	var resp guber.CheckRateLimitsResponse
	err := client.CheckRateLimits(context.Background(), &guber.CheckRateLimitsRequest{
		Requests: []*guber.RateLimitRequest{
			{
				Name:      "test_multiple_async",
				UniqueKey: "account:9234",
				Algorithm: guber.Algorithm_TOKEN_BUCKET,
				Duration:  guber.Second * 9,
				Limit:     2,
				Hits:      1,
				Behavior:  0,
			},
			{
				Name:      "test_multiple_async",
				UniqueKey: "account:5678",
				Algorithm: guber.Algorithm_TOKEN_BUCKET,
				Duration:  guber.Second * 9,
				Limit:     10,
				Hits:      5,
				Behavior:  0,
			},
		},
	}, &resp)
	require.Nil(t, err)

	require.Len(t, resp.Responses, 2)

	rl := resp.Responses[0]
	assert.Equal(t, guber.Status_UNDER_LIMIT, rl.Status)
	assert.Equal(t, int64(1), rl.Remaining)
	assert.Equal(t, int64(2), rl.Limit)

	rl = resp.Responses[1]
	assert.Equal(t, guber.Status_UNDER_LIMIT, rl.Status)
	assert.Equal(t, int64(5), rl.Remaining)
	assert.Equal(t, int64(10), rl.Limit)
}

func TestTokenBucket(t *testing.T) {
	defer clock.Freeze(clock.Now()).Unfreeze()

	addr := cluster.GetRandomPeerInfo(cluster.DataCenterNone).HTTPAddress
	client, errs := guber.NewClient(guber.WithNoTLS(addr))
	require.Nil(t, errs)

	tests := []struct {
		name      string
		Remaining int64
		Status    guber.Status
		Sleep     clock.Duration
	}{
		{
			name:      "remaining should be one",
			Remaining: 1,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Duration(0),
		},
		{
			name:      "remaining should be zero and under limit",
			Remaining: 0,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Millisecond * 100,
		},
		{
			name:      "after waiting 100ms remaining should be 1 and under limit",
			Remaining: 1,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Duration(0),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resp guber.CheckRateLimitsResponse
			err := client.CheckRateLimits(context.Background(), &guber.CheckRateLimitsRequest{
				Requests: []*guber.RateLimitRequest{
					{
						Name:      "test_token_bucket",
						UniqueKey: "account:1234",
						Algorithm: guber.Algorithm_TOKEN_BUCKET,
						Duration:  guber.Millisecond * 5,
						Limit:     2,
						Hits:      1,
					},
				},
			}, &resp)
			require.Nil(t, err)

			rl := resp.Responses[0]

			assert.Empty(t, rl.Error)
			assert.Equal(t, tt.Status, rl.Status)
			assert.Equal(t, tt.Remaining, rl.Remaining)
			assert.Equal(t, int64(2), rl.Limit)
			assert.True(t, rl.ResetTime != 0)
			clock.Advance(tt.Sleep)
		})
	}
}

func TestTokenBucketGregorian(t *testing.T) {
	defer clock.Freeze(clock.Now()).Unfreeze()

	client, errs := guber.NewClient(guber.WithNoTLS(cluster.GetRandomPeerInfo(cluster.DataCenterNone).HTTPAddress))
	require.Nil(t, errs)

	tests := []struct {
		Name      string
		Remaining int64
		Status    guber.Status
		Sleep     clock.Duration
		Hits      int64
	}{
		{
			Name:      "first hit",
			Hits:      1,
			Remaining: 59,
			Status:    guber.Status_UNDER_LIMIT,
		},
		{
			Name:      "second hit",
			Hits:      1,
			Remaining: 58,
			Status:    guber.Status_UNDER_LIMIT,
		},
		{
			Name:      "consume remaining hits",
			Hits:      58,
			Remaining: 0,
			Status:    guber.Status_UNDER_LIMIT,
		},
		{
			Name:      "should be over the limit",
			Hits:      1,
			Remaining: 0,
			Status:    guber.Status_OVER_LIMIT,
			Sleep:     clock.Second * 61,
		},
		{
			Name:      "should be under the limit",
			Hits:      0,
			Remaining: 60,
			Status:    guber.Status_UNDER_LIMIT,
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			var resp guber.CheckRateLimitsResponse
			err := client.CheckRateLimits(context.Background(), &guber.CheckRateLimitsRequest{
				Requests: []*guber.RateLimitRequest{
					{
						Name:      "test_token_bucket_greg",
						UniqueKey: "account:12345",
						Behavior:  guber.Behavior_DURATION_IS_GREGORIAN,
						Algorithm: guber.Algorithm_TOKEN_BUCKET,
						Duration:  guber.GregorianMinutes,
						Hits:      test.Hits,
						Limit:     60,
					},
				},
			}, &resp)
			require.Nil(t, err)

			rl := resp.Responses[0]

			assert.Empty(t, rl.Error)
			assert.Equal(t, test.Status, rl.Status)
			assert.Equal(t, test.Remaining, rl.Remaining)
			assert.Equal(t, int64(60), rl.Limit)
			assert.True(t, rl.ResetTime != 0)
			clock.Advance(test.Sleep)
		})
	}
}

func TestTokenBucketNegativeHits(t *testing.T) {
	defer clock.Freeze(clock.Now()).Unfreeze()

	addr := cluster.GetRandomPeerInfo(cluster.DataCenterNone).HTTPAddress
	client, errs := guber.NewClient(guber.WithNoTLS(addr))
	require.Nil(t, errs)

	tests := []struct {
		name      string
		Remaining int64
		Status    guber.Status
		Sleep     clock.Duration
		Hits      int64
	}{
		{
			name:      "remaining should be three",
			Remaining: 3,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Duration(0),
			Hits:      -1,
		},
		{
			name:      "remaining should be four and under limit",
			Remaining: 4,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Duration(0),
			Hits:      -1,
		},
		{
			name:      "remaining should be 0 and under limit",
			Remaining: 0,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Duration(0),
			Hits:      4,
		},
		{
			name:      "remaining should be 1 and under limit",
			Remaining: 1,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Duration(0),
			Hits:      -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resp guber.CheckRateLimitsResponse
			err := client.CheckRateLimits(context.Background(), &guber.CheckRateLimitsRequest{
				Requests: []*guber.RateLimitRequest{
					{
						Name:      "test_token_bucket_negative",
						UniqueKey: "account:12345",
						Algorithm: guber.Algorithm_TOKEN_BUCKET,
						Duration:  guber.Millisecond * 5,
						Limit:     2,
						Hits:      tt.Hits,
					},
				},
			}, &resp)
			require.Nil(t, err)

			rl := resp.Responses[0]

			assert.Empty(t, rl.Error)
			assert.Equal(t, tt.Status, rl.Status)
			assert.Equal(t, tt.Remaining, rl.Remaining)
			assert.Equal(t, int64(2), rl.Limit)
			assert.True(t, rl.ResetTime != 0)
			clock.Advance(tt.Sleep)
		})
	}
}

func TestLeakyBucket(t *testing.T) {
	defer clock.Freeze(clock.Now()).Unfreeze()

	client, errs := guber.NewClient(guber.WithNoTLS(cluster.PeerAt(0).HTTPAddress))
	require.Nil(t, errs)

	tests := []struct {
		Name      string
		Hits      int64
		Remaining int64
		Status    guber.Status
		Sleep     clock.Duration
	}{
		{
			Name:      "first hit",
			Hits:      1,
			Remaining: 9,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Second,
		},
		{
			Name:      "second hit; no leak",
			Hits:      1,
			Remaining: 8,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Second,
		},
		{
			Name:      "third hit; no leak",
			Hits:      1,
			Remaining: 7,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Millisecond * 1500,
		},
		{
			Name:      "should leak one hit 3 seconds after first hit",
			Hits:      0,
			Remaining: 8,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Second * 3,
		},
		{
			Name:      "3 Seconds later we should have leaked another hit",
			Hits:      0,
			Remaining: 9,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Duration(0),
		},
		{
			Name:      "max out our bucket and sleep for 3 seconds",
			Hits:      9,
			Remaining: 0,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Duration(0),
		},
		{
			Name:      "should be over the limit",
			Hits:      1,
			Remaining: 0,
			Status:    guber.Status_OVER_LIMIT,
			Sleep:     clock.Second * 3,
		},
		{
			Name:      "should have leaked 1 hit",
			Hits:      0,
			Remaining: 1,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Second * 60,
		},
		{
			Name:      "should max out the limit",
			Hits:      0,
			Remaining: 10,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Second * 60,
		},
		{
			Name:      "should use up the limit and wait until 1 second before duration period",
			Hits:      10,
			Remaining: 0,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Second * 29,
		},
		{
			Name:      "should use up all hits one second before duration period",
			Hits:      9,
			Remaining: 0,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Second * 3,
		},
		{
			Name:      "only have 1 hit remaining",
			Hits:      1,
			Remaining: 0,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Second,
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			var resp guber.CheckRateLimitsResponse
			err := client.CheckRateLimits(context.Background(), &guber.CheckRateLimitsRequest{
				Requests: []*guber.RateLimitRequest{
					{
						Name:      "test_leaky_bucket",
						UniqueKey: "account:1234",
						Algorithm: guber.Algorithm_LEAKY_BUCKET,
						Duration:  guber.Second * 30,
						Hits:      test.Hits,
						Limit:     10,
					},
				},
			}, &resp)
			require.NoError(t, err)
			require.Len(t, resp.Responses, 1)

			rl := resp.Responses[0]

			assert.Equal(t, test.Status, rl.Status)
			assert.Equal(t, test.Remaining, rl.Remaining)
			assert.Equal(t, int64(10), rl.Limit)
			assert.Equal(t, clock.Now().Unix()+(rl.Limit-rl.Remaining)*3, rl.ResetTime/1000)
			clock.Advance(test.Sleep)
		})
	}
}

func TestLeakyBucketWithBurst(t *testing.T) {
	defer clock.Freeze(clock.Now()).Unfreeze()

	client, errs := guber.NewClient(guber.WithNoTLS(cluster.PeerAt(0).HTTPAddress))
	require.Nil(t, errs)

	tests := []struct {
		Name      string
		Hits      int64
		Remaining int64
		Status    guber.Status
		Sleep     clock.Duration
	}{
		{
			Name:      "first hit",
			Hits:      1,
			Remaining: 19,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Second,
		},
		{
			Name:      "second hit; no leak",
			Hits:      1,
			Remaining: 18,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Second,
		},
		{
			Name:      "third hit; no leak",
			Hits:      1,
			Remaining: 17,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Millisecond * 1500,
		},
		{
			Name:      "should leak one hit 3 seconds after first hit",
			Hits:      0,
			Remaining: 18,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Second * 3,
		},
		{
			Name:      "3 Seconds later we should have leaked another hit",
			Hits:      0,
			Remaining: 19,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Duration(0),
		},
		{
			Name:      "max out our bucket and sleep for 3 seconds",
			Hits:      19,
			Remaining: 0,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Duration(0),
		},
		{
			Name:      "should be over the limit",
			Hits:      1,
			Remaining: 0,
			Status:    guber.Status_OVER_LIMIT,
			Sleep:     clock.Second * 3,
		},
		{
			Name:      "should have leaked 1 hit",
			Hits:      0,
			Remaining: 1,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Second * 60,
		},
		{
			Name:      "should max out remaining",
			Hits:      0,
			Remaining: 20,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Second,
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			var resp guber.CheckRateLimitsResponse
			err := client.CheckRateLimits(context.Background(), &guber.CheckRateLimitsRequest{
				Requests: []*guber.RateLimitRequest{
					{
						Name:      "test_leaky_bucket_with_burst",
						UniqueKey: "account:1234",
						Algorithm: guber.Algorithm_LEAKY_BUCKET,
						Duration:  guber.Second * 30,
						Hits:      test.Hits,
						Limit:     10,
						Burst:     20,
					},
				},
			}, &resp)
			require.NoError(t, err)
			require.Len(t, resp.Responses, 1)

			rl := resp.Responses[0]

			assert.Equal(t, test.Status, rl.Status)
			assert.Equal(t, test.Remaining, rl.Remaining)
			assert.Equal(t, int64(10), rl.Limit)
			assert.Equal(t, clock.Now().Unix()+(rl.Limit-rl.Remaining)*3, rl.ResetTime/1000)
			clock.Advance(test.Sleep)
		})
	}
}

func TestLeakyBucketGregorian(t *testing.T) {
	defer clock.Freeze(clock.Now()).Unfreeze()

	client, errs := guber.NewClient(guber.WithNoTLS(cluster.PeerAt(0).HTTPAddress))
	require.Nil(t, errs)

	tests := []struct {
		Name      string
		Hits      int64
		Remaining int64
		Status    guber.Status
		Sleep     clock.Duration
	}{
		{
			Name:      "first hit",
			Hits:      1,
			Remaining: 59,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Millisecond * 500,
		},
		{
			Name:      "second hit; no leak",
			Hits:      1,
			Remaining: 58,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Second,
		},
		{
			Name:      "third hit; leak one hit",
			Hits:      1,
			Remaining: 58,
			Status:    guber.Status_UNDER_LIMIT,
		},
	}

	now := clock.Now()
	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			var resp guber.CheckRateLimitsResponse
			err := client.CheckRateLimits(context.Background(), &guber.CheckRateLimitsRequest{
				Requests: []*guber.RateLimitRequest{
					{
						Name:      "test_leaky_bucket_greg",
						UniqueKey: "account:12345",
						Behavior:  guber.Behavior_DURATION_IS_GREGORIAN,
						Algorithm: guber.Algorithm_LEAKY_BUCKET,
						Duration:  guber.GregorianMinutes,
						Hits:      test.Hits,
						Limit:     60,
					},
				},
			}, &resp)
			clock.Freeze(clock.Now())
			require.NoError(t, err)

			rl := resp.Responses[0]

			assert.Equal(t, test.Status, rl.Status)
			assert.Equal(t, test.Remaining, rl.Remaining)
			assert.Equal(t, int64(60), rl.Limit)
			assert.True(t, rl.ResetTime > now.Unix())
			clock.Advance(test.Sleep)
		})
	}
}

func TestLeakyBucketNegativeHits(t *testing.T) {
	defer clock.Freeze(clock.Now()).Unfreeze()

	client, errs := guber.NewClient(guber.WithNoTLS(cluster.PeerAt(0).HTTPAddress))
	require.Nil(t, errs)

	tests := []struct {
		Name      string
		Hits      int64
		Remaining int64
		Status    guber.Status
		Sleep     clock.Duration
	}{
		{
			Name:      "first hit",
			Hits:      1,
			Remaining: 9,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Duration(0),
		},
		{
			Name:      "can increase remaining",
			Hits:      -1,
			Remaining: 10,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Duration(0),
		},
		{
			Name:      "remaining should be zero",
			Hits:      10,
			Remaining: 0,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Duration(0),
		},
		{
			Name:      "can append one to remaining when remaining is zero",
			Hits:      -1,
			Remaining: 1,
			Status:    guber.Status_UNDER_LIMIT,
			Sleep:     clock.Duration(0),
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			var resp guber.CheckRateLimitsResponse
			err := client.CheckRateLimits(context.Background(), &guber.CheckRateLimitsRequest{
				Requests: []*guber.RateLimitRequest{
					{
						Name:      "test_leaky_bucket_negative",
						UniqueKey: "account:12345",
						Algorithm: guber.Algorithm_LEAKY_BUCKET,
						Duration:  guber.Second * 30,
						Hits:      test.Hits,
						Limit:     10,
					},
				},
			}, &resp)
			require.NoError(t, err)
			require.Len(t, resp.Responses, 1)

			rl := resp.Responses[0]

			assert.Equal(t, test.Status, rl.Status)
			assert.Equal(t, test.Remaining, rl.Remaining)
			assert.Equal(t, int64(10), rl.Limit)
			assert.Equal(t, clock.Now().Unix()+(rl.Limit-rl.Remaining)*3, rl.ResetTime/1000)
			clock.Advance(test.Sleep)
		})
	}
}

func TestMissingFields(t *testing.T) {
	client, errs := guber.NewClient(guber.WithNoTLS(cluster.GetRandomPeerInfo(cluster.DataCenterNone).HTTPAddress))
	require.Nil(t, errs)

	tests := []struct {
		Req    *guber.RateLimitRequest
		Status guber.Status
		Error  string
	}{
		{
			Req: &guber.RateLimitRequest{
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
			Req: &guber.RateLimitRequest{
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
			Req: &guber.RateLimitRequest{
				UniqueKey: "account:1234",
				Hits:      1,
				Duration:  10000,
				Limit:     5,
			},
			Error:  "field 'namespace' cannot be empty",
			Status: guber.Status_UNDER_LIMIT,
		},
		{
			Req: &guber.RateLimitRequest{
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
		var resp guber.CheckRateLimitsResponse
		err := client.CheckRateLimits(context.Background(), &guber.CheckRateLimitsRequest{
			Requests: []*guber.RateLimitRequest{test.Req},
		}, &resp)
		require.Nil(t, err)
		assert.Equal(t, test.Error, resp.Responses[0].Error, i)
		assert.Equal(t, test.Status, resp.Responses[0].Status, i)
	}
}

func TestGlobalRateLimits(t *testing.T) {
	address := cluster.PeerAt(0).HTTPAddress
	client, errs := guber.NewClient(guber.WithNoTLS(address))
	require.NoError(t, errs)

	sendHit := func(status guber.Status, remain int64, i int) string {
		ctx, cancel := context.WithTimeout(context.Background(), clock.Second*5)
		defer cancel()
		var resp guber.CheckRateLimitsResponse
		err := client.CheckRateLimits(ctx, &guber.CheckRateLimitsRequest{
			Requests: []*guber.RateLimitRequest{
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
		}, &resp)
		require.NoError(t, err, i)
		assert.Equal(t, "", resp.Responses[0].Error, i)
		assert.Equal(t, status, resp.Responses[0].Status, i)
		assert.Equal(t, remain, resp.Responses[0].Remaining, i)
		assert.Equal(t, int64(5), resp.Responses[0].Limit, i)

		// ensure that we have a canonical host
		assert.NotEmpty(t, resp.Responses[0].Metadata["owner"])

		// name/key should ensure our connected peer is NOT the owner,
		// the peer we are connected to should forward requests asynchronously to the owner.
		assert.NotEqual(t, address, resp.Responses[0].Metadata["owner"])

		return resp.Responses[0].Metadata["owner"]
	}

	// Our first hit should create the request on the peer and queue for async forward
	sendHit(guber.Status_UNDER_LIMIT, 4, 1)

	// Our second should be processed as if we own it since the async forward hasn't occurred yet
	sendHit(guber.Status_UNDER_LIMIT, 3, 2)

	log := t.Logf
	testutil.UntilPass(t, 20, clock.Millisecond*200, func(t testutil.TestingT) {
		// Inspect our metrics, ensure they collected the counts we expected during this test
		d := cluster.DaemonAt(0)
		metricsURL := fmt.Sprintf("http://%s/metrics", d.Config().HTTPListenAddress)
		m := getMetricRequest(t, metricsURL, "gubernator_global_send_duration_count")
		assert.Equal(t, 1, int(m.Value))

		// Expect one peer (the owning peer) to indicate a broadcast.
		var broadcastCount int
		for i := 0; i < cluster.NumOfDaemons(); i++ {
			d := cluster.DaemonAt(i)
			metricsURL := fmt.Sprintf("http://%s/metrics", d.Config().HTTPListenAddress)
			m := getMetricRequest(t, metricsURL, "gubernator_broadcast_duration_count")
			broadcastCount += int(m.Value)
		}

		assert.Equal(t, 1, broadcastCount)
		log("Global Send Duration Count: %d\n", broadcastCount)

		// Service 5 should be the owner of our global rate limit
		d = cluster.DaemonAt(5)
		config := d.Config()
		resp, err := http.Get(fmt.Sprintf("http://%s/metrics", config.HTTPListenAddress))
		if !assert.NoError(t, err) {
			return
		}
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		m = getMetric(t, resp.Body, "gubernator_broadcast_duration_count")
		assert.Equal(t, 1, int(m.Value))
		log("Broadcast Duration Count: %d\n", int(m.Value))
	})
}

func getMetricRequest(t testutil.TestingT, url string, name string) *model.Sample {
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	return getMetric(t, resp.Body, name)
}

func TestChangeLimit(t *testing.T) {
	client, errs := guber.NewClient(guber.WithNoTLS(cluster.GetRandomPeerInfo(cluster.DataCenterNone).HTTPAddress))
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
			var resp guber.CheckRateLimitsResponse
			err := client.CheckRateLimits(context.Background(), &guber.CheckRateLimitsRequest{
				Requests: []*guber.RateLimitRequest{
					{
						Name:      "test_change_limit",
						UniqueKey: "account:1234",
						Algorithm: tt.Algorithm,
						Duration:  guber.Millisecond * 9000,
						Limit:     tt.Limit,
						Hits:      1,
					},
				},
			}, &resp)
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
	client, errs := guber.NewClient(guber.WithNoTLS(cluster.GetRandomPeerInfo(cluster.DataCenterNone).HTTPAddress))
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
			var resp guber.CheckRateLimitsResponse
			err := client.CheckRateLimits(context.Background(), &guber.CheckRateLimitsRequest{
				Requests: []*guber.RateLimitRequest{
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
			}, &resp)
			require.Nil(t, err)

			rl := resp.Responses[0]

			assert.Equal(t, tt.Status, rl.Status)
			assert.Equal(t, tt.Remaining, rl.Remaining)
			assert.Equal(t, tt.Limit, rl.Limit)
		})
	}
}

func TestHealthCheck(t *testing.T) {
	client, err := guber.NewClient(guber.WithNoTLS(cluster.DaemonAt(0).Listener.Addr().String()))
	require.NoError(t, err)

	// Check that the cluster is healthy to start with
	var resp guber.HealthCheckResponse
	err = client.HealthCheck(context.Background(), &resp)
	require.NoError(t, err)

	require.Equal(t, "healthy", resp.GetStatus())

	// Create a global rate limit that will need to be sent to all peers in the cluster
	{
		var resp guber.CheckRateLimitsResponse
		err = client.CheckRateLimits(context.Background(), &guber.CheckRateLimitsRequest{
			Requests: []*guber.RateLimitRequest{
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
		}, &resp)
		require.NoError(t, err)
	}

	// Stop the rest of the cluster to ensure errors occur on our instance
	for i := 1; i < cluster.NumOfDaemons(); i++ {
		d := cluster.DaemonAt(i)
		require.NotNil(t, d)
		d.Close(context.Background())
	}

	// Hit the global rate limit again this time causing a connection error
	{
		var resp guber.CheckRateLimitsResponse
		err = client.CheckRateLimits(context.Background(), &guber.CheckRateLimitsRequest{
			Requests: []*guber.RateLimitRequest{
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
		}, &resp)
		require.NoError(t, err)
	}

	testutil.UntilPass(t, 20, clock.Millisecond*300, func(t testutil.TestingT) {
		// Check the health again to get back the connection error
		var resp guber.HealthCheckResponse
		err = client.HealthCheck(context.Background(), &resp)
		if assert.Nil(t, err) {
			return
		}

		assert.Equal(t, "unhealthy", resp.GetStatus())
		assert.Contains(t, resp.GetMessage(), "connect: connection refused")
	})

	// Restart stopped instances
	ctx, cancel := context.WithTimeout(context.Background(), clock.Second*15)
	defer cancel()
	require.NoError(t, cluster.Restart(ctx))
}

func TestLeakyBucketDivBug(t *testing.T) {
	defer clock.Freeze(clock.Now()).Unfreeze()

	client, err := guber.NewClient(guber.WithNoTLS(cluster.GetRandomPeerInfo(cluster.DataCenterNone).HTTPAddress))
	require.NoError(t, err)

	var resp guber.CheckRateLimitsResponse
	err = client.CheckRateLimits(context.Background(), &guber.CheckRateLimitsRequest{
		Requests: []*guber.RateLimitRequest{
			{
				Name:      "test_leaky_bucket_div",
				UniqueKey: "account:12345",
				Algorithm: guber.Algorithm_LEAKY_BUCKET,
				Duration:  guber.Millisecond * 1000,
				Hits:      1,
				Limit:     2000,
			},
		},
	}, &resp)
	require.NoError(t, err)
	assert.Equal(t, "", resp.Responses[0].Error)
	assert.Equal(t, guber.Status_UNDER_LIMIT, resp.Responses[0].Status)
	assert.Equal(t, int64(1999), resp.Responses[0].Remaining)
	assert.Equal(t, int64(2000), resp.Responses[0].Limit)

	// Should result in a rate of 0.5
	err = client.CheckRateLimits(context.Background(), &guber.CheckRateLimitsRequest{
		Requests: []*guber.RateLimitRequest{
			{
				Name:      "test_leaky_bucket_div",
				UniqueKey: "account:12345",
				Algorithm: guber.Algorithm_LEAKY_BUCKET,
				Duration:  guber.Millisecond * 1000,
				Hits:      100,
				Limit:     2000,
			},
		},
	}, &resp)
	require.NoError(t, err)
	assert.Equal(t, int64(1899), resp.Responses[0].Remaining)
	assert.Equal(t, int64(2000), resp.Responses[0].Limit)
}

func TestMultiRegion(t *testing.T) {

	// TODO: Queue a rate limit with multi region behavior on the DataCenterNone cluster
	// TODO: Check the immediate response is correct
	// TODO: Wait until the rate limit count shows up on the DataCenterOne and DataCenterTwo cluster

	// TODO: Increment the counts on the DataCenterTwo and DataCenterOne clusters
	// TODO: Wait until both rate limit count show up on all datacenters
}

func TestDefaultHealthZ(t *testing.T) {
	address := cluster.GetRandomPeerInfo(cluster.DataCenterNone).HTTPAddress
	resp, err := http.DefaultClient.Get("http://" + address + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	b, err := io.ReadAll(resp.Body)

	assert.Contains(t, string(b), "peerCount")

	var hc guber.HealthCheckResponse
	require.NoError(t, json.Unmarshal(b, &hc))
	assert.Equal(t, int32(10), hc.PeerCount)

	require.NoError(t, err)
}

func TestGetPeerRateLimits(t *testing.T) {
	ctx := context.Background()
	info := cluster.GetRandomPeerInfo(cluster.DataCenterNone)

	peerClient, err := guber.NewPeer(guber.PeerConfig{
		PeerClient: guber.NewPeerClient(guber.WithNoTLS(info.HTTPAddress)),
		Info:       info,
	})
	require.NoError(t, err)

	t.Run("Stable rate check request order", func(t *testing.T) {
		// Ensure response order matches rate check request order.
		// Try various batch sizes.
		testCases := []int{1, 2, 5, 10, 100, 1000}

		for _, n := range testCases {
			t.Run(fmt.Sprintf("Batch size %d", n), func(t *testing.T) {
				// Build request.
				req := &guber.ForwardRequest{
					Requests: make([]*guber.RateLimitRequest, n),
				}
				for i := 0; i < n; i++ {
					req.Requests[i] = &guber.RateLimitRequest{
						Name:      "Foobar",
						UniqueKey: fmt.Sprintf("%08x", i),
						Hits:      0,
						Limit:     1000 + int64(i),
						Duration:  1000,
						Algorithm: guber.Algorithm_TOKEN_BUCKET,
						Behavior:  guber.Behavior_BATCHING,
					}
				}

				// Send request.
				var resp guber.ForwardResponse
				err := peerClient.ForwardBatch(ctx, req, &resp)

				// Verify.
				require.NoError(t, err)
				assert.Len(t, resp.RateLimits, n)

				for i, item := range resp.RateLimits {
					// Identify response by its unique limit.
					assert.Equal(t, req.Requests[i].Limit, item.Limit)
				}
			})
		}
	})
}

func TestNoRateLimits(t *testing.T) {
	client, errs := guber.NewClient(cluster.GetRandomClientOptions(cluster.DataCenterNone))
	require.Nil(t, errs)

	var resp guber.CheckRateLimitsResponse
	err := client.CheckRateLimits(context.Background(), &guber.CheckRateLimitsRequest{}, &resp)
	require.Error(t, err)
}

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
