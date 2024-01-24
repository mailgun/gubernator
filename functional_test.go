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
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	guber "github.com/mailgun/gubernator/v2"
	"github.com/mailgun/gubernator/v2/cluster"
	"github.com/mailgun/holster/v4/clock"
	"github.com/mailgun/holster/v4/testutil"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/resolver"
	json "google.golang.org/protobuf/encoding/protojson"
)

// Setup and shutdown the mock gubernator cluster for the entire test suite
func TestMain(m *testing.M) {
	if err := cluster.StartWith([]guber.PeerInfo{
		{GRPCAddress: "127.0.0.1:9990", HTTPAddress: "127.0.0.1:9980", DataCenter: cluster.DataCenterNone},
		{GRPCAddress: "127.0.0.1:9991", HTTPAddress: "127.0.0.1:9981", DataCenter: cluster.DataCenterNone},
		{GRPCAddress: "127.0.0.1:9992", HTTPAddress: "127.0.0.1:9982", DataCenter: cluster.DataCenterNone},
		{GRPCAddress: "127.0.0.1:9993", HTTPAddress: "127.0.0.1:9983", DataCenter: cluster.DataCenterNone},
		{GRPCAddress: "127.0.0.1:9994", HTTPAddress: "127.0.0.1:9984", DataCenter: cluster.DataCenterNone},
		{GRPCAddress: "127.0.0.1:9995", HTTPAddress: "127.0.0.1:9985", DataCenter: cluster.DataCenterNone},

		// DataCenterOne
		{GRPCAddress: "127.0.0.1:9890", HTTPAddress: "127.0.0.1:9880", DataCenter: cluster.DataCenterOne},
		{GRPCAddress: "127.0.0.1:9891", HTTPAddress: "127.0.0.1:9881", DataCenter: cluster.DataCenterOne},
		{GRPCAddress: "127.0.0.1:9892", HTTPAddress: "127.0.0.1:9882", DataCenter: cluster.DataCenterOne},
		{GRPCAddress: "127.0.0.1:9893", HTTPAddress: "127.0.0.1:9883", DataCenter: cluster.DataCenterOne},
	}); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer cluster.Stop()
	os.Exit(m.Run())
}

func TestOverTheLimit(t *testing.T) {
	client, errs := guber.DialV1Server(cluster.GetRandomPeer(cluster.DataCenterNone).GRPCAddress, nil)
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

// TestMultipleAsync tests a regression that occurred when a client requests multiple
// rate limits that are asynchronously sent to other nodes.
func TestMultipleAsync(t *testing.T) {
	// If the consistent hash changes or the number of peers changes, this might
	// need to be changed. We want the test to forward both rate limits to other
	// nodes in the cluster.

	t.Logf("Asking Peer: %s", cluster.GetPeers()[0].GRPCAddress)
	client, errs := guber.DialV1Server(cluster.GetPeers()[0].GRPCAddress, nil)
	require.Nil(t, errs)

	resp, err := client.GetRateLimits(context.Background(), &guber.GetRateLimitsReq{
		Requests: []*guber.RateLimitReq{
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
	})
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

	addr := cluster.GetRandomPeer(cluster.DataCenterNone).GRPCAddress
	client, errs := guber.DialV1Server(addr, nil)
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

	client, errs := guber.DialV1Server(cluster.GetRandomPeer(cluster.DataCenterNone).GRPCAddress, nil)
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
			resp, err := client.GetRateLimits(context.Background(), &guber.GetRateLimitsReq{
				Requests: []*guber.RateLimitReq{
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
			})
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

	addr := cluster.GetRandomPeer(cluster.DataCenterNone).GRPCAddress
	client, errs := guber.DialV1Server(addr, nil)
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
			resp, err := client.GetRateLimits(context.Background(), &guber.GetRateLimitsReq{
				Requests: []*guber.RateLimitReq{
					{
						Name:      "test_token_bucket_negative",
						UniqueKey: "account:12345",
						Algorithm: guber.Algorithm_TOKEN_BUCKET,
						Duration:  guber.Millisecond * 5,
						Limit:     2,
						Hits:      tt.Hits,
					},
				},
			})
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

	client, errs := guber.DialV1Server(cluster.PeerAt(0).GRPCAddress, nil)
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
			resp, err := client.GetRateLimits(context.Background(), &guber.GetRateLimitsReq{
				Requests: []*guber.RateLimitReq{
					{
						Name:      "test_leaky_bucket",
						UniqueKey: "account:1234",
						Algorithm: guber.Algorithm_LEAKY_BUCKET,
						Duration:  guber.Second * 30,
						Hits:      test.Hits,
						Limit:     10,
					},
				},
			})
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

	client, errs := guber.DialV1Server(cluster.PeerAt(0).GRPCAddress, nil)
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
			resp, err := client.GetRateLimits(context.Background(), &guber.GetRateLimitsReq{
				Requests: []*guber.RateLimitReq{
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
			})
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

	client, errs := guber.DialV1Server(cluster.PeerAt(0).GRPCAddress, nil)
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
			resp, err := client.GetRateLimits(context.Background(), &guber.GetRateLimitsReq{
				Requests: []*guber.RateLimitReq{
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
			})
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

	client, errs := guber.DialV1Server(cluster.PeerAt(0).GRPCAddress, nil)
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
			resp, err := client.GetRateLimits(context.Background(), &guber.GetRateLimitsReq{
				Requests: []*guber.RateLimitReq{
					{
						Name:      "test_leaky_bucket_negative",
						UniqueKey: "account:12345",
						Algorithm: guber.Algorithm_LEAKY_BUCKET,
						Duration:  guber.Second * 30,
						Hits:      test.Hits,
						Limit:     10,
					},
				},
			})
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
	client, errs := guber.DialV1Server(cluster.GetRandomPeer(cluster.DataCenterNone).GRPCAddress, nil)
	require.Nil(t, errs)

	tests := []struct {
		Req    *guber.RateLimitReq
		Status guber.Status
		Error  string
	}{
		{
			Req: &guber.RateLimitReq{
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
			Req: &guber.RateLimitReq{
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
			Req: &guber.RateLimitReq{
				UniqueKey: "account:1234",
				Hits:      1,
				Duration:  10000,
				Limit:     5,
			},
			Error:  "field 'namespace' cannot be empty",
			Status: guber.Status_UNDER_LIMIT,
		},
		{
			Req: &guber.RateLimitReq{
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
			Requests: []*guber.RateLimitReq{test.Req},
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
	})
}

func TestGlobalRateLimitsPeerOverLimit(t *testing.T) {
	const (
		name = "test_global_token_limit"
		key  = "account:12345"
	)

	// Make a connection to a peer in the cluster which does not own this rate limit
	client, err := getClientToNonOwningPeer(name, key)
	require.NoError(t, err)

	sendHit := func(expectedStatus guber.Status, hits int) {
		ctx, cancel := context.WithTimeout(context.Background(), clock.Hour*5)
		defer cancel()
		resp, err := client.GetRateLimits(ctx, &guber.GetRateLimitsReq{
			Requests: []*guber.RateLimitReq{
				{
					Name:      name,
					UniqueKey: key,
					Algorithm: guber.Algorithm_TOKEN_BUCKET,
					Behavior:  guber.Behavior_GLOBAL,
					Duration:  guber.Minute * 5,
					Hits:      1,
					Limit:     2,
				},
			},
		})
		assert.NoError(t, err)
		assert.Equal(t, "", resp.Responses[0].GetError())
		assert.Equal(t, expectedStatus, resp.Responses[0].GetStatus())
	}

	// Send two hits that should be processed by the owner and the broadcast to peer, depleting the remaining
	sendHit(guber.Status_UNDER_LIMIT, 1)
	sendHit(guber.Status_UNDER_LIMIT, 1)
	// Wait for the broadcast from the owner to the peer
	time.Sleep(time.Second * 3)
	// Since the remainder is 0, the peer should set OVER_LIMIT instead of waiting for the owner
	// to respond with OVER_LIMIT.
	sendHit(guber.Status_OVER_LIMIT, 1)
	// Wait for the broadcast from the owner to the peer
	time.Sleep(time.Second * 3)
	// The status should still be OVER_LIMIT
	sendHit(guber.Status_OVER_LIMIT, 0)
}

func TestGlobalRateLimitsPeerOverLimitLeaky(t *testing.T) {
	const (
		name = "test_global_token_limit_leaky"
		key  = "account:12345"
	)

	// Make a connection to a peer in the cluster which does not own this rate limit
	client, err := getClientToNonOwningPeer(name, key)
	require.NoError(t, err)

	sendHit := func(expectedStatus guber.Status, hits int) {
		ctx, cancel := context.WithTimeout(context.Background(), clock.Hour*5)
		defer cancel()
		resp, err := client.GetRateLimits(ctx, &guber.GetRateLimitsReq{
			Requests: []*guber.RateLimitReq{
				{
					Name:      name,
					UniqueKey: key,
					Algorithm: guber.Algorithm_LEAKY_BUCKET,
					Behavior:  guber.Behavior_GLOBAL,
					Duration:  guber.Minute * 5,
					Hits:      1,
					Limit:     2,
				},
			},
		})
		assert.NoError(t, err)
		assert.Equal(t, "", resp.Responses[0].GetError())
		assert.Equal(t, expectedStatus, resp.Responses[0].GetStatus())
	}

	// Send two hits that should be processed by the owner and the broadcast to peer, depleting the remaining
	sendHit(guber.Status_UNDER_LIMIT, 1)
	sendHit(guber.Status_UNDER_LIMIT, 1)
	// Wait for the broadcast from the owner to the peer
	time.Sleep(time.Second * 3)
	// Since the peer must wait for the owner to say it's over the limit, this will return under the limit.
	sendHit(guber.Status_UNDER_LIMIT, 1)
	// Wait for the broadcast from the owner to the peer
	time.Sleep(time.Second * 3)
	// The status should now be OVER_LIMIT
	sendHit(guber.Status_OVER_LIMIT, 0)
}

func getMetricRequest(t testutil.TestingT, url string, name string) *model.Sample {
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	return getMetric(t, resp.Body, name)
}

func TestChangeLimit(t *testing.T) {
	client, errs := guber.DialV1Server(cluster.GetRandomPeer(cluster.DataCenterNone).GRPCAddress, nil)
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
	client, errs := guber.DialV1Server(cluster.GetRandomPeer(cluster.DataCenterNone).GRPCAddress, nil)
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
	client, err := guber.DialV1Server(cluster.DaemonAt(0).GRPCListeners[0].Addr().String(), nil)
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

	// Stop the rest of the cluster to ensure errors occur on our instance
	for i := 1; i < cluster.NumOfDaemons(); i++ {
		d := cluster.DaemonAt(i)
		require.NotNil(t, d)
		d.Close()
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
	require.NoError(t, cluster.Restart(ctx))
}

func TestLeakyBucketDivBug(t *testing.T) {
	defer clock.Freeze(clock.Now()).Unfreeze()

	client, err := guber.DialV1Server(cluster.GetRandomPeer(cluster.DataCenterNone).GRPCAddress, nil)
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

func TestMultiRegion(t *testing.T) {

	// TODO: Queue a rate limit with multi region behavior on the DataCenterNone cluster
	// TODO: Check the immediate response is correct
	// TODO: Wait until the rate limit count shows up on the DataCenterOne and DataCenterTwo cluster

	// TODO: Increment the counts on the DataCenterTwo and DataCenterOne clusters
	// TODO: Wait until both rate limit count show up on all datacenters
}

func TestGRPCGateway(t *testing.T) {
	address := cluster.GetRandomPeer(cluster.DataCenterNone).HTTPAddress
	resp, err := http.DefaultClient.Get("http://" + address + "/v1/HealthCheck")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	b, err := io.ReadAll(resp.Body)

	// This test ensures future upgrades don't accidentally change `under_score` to `camelCase` again.
	assert.Contains(t, string(b), "peer_count")

	var hc guber.HealthCheckResp
	require.NoError(t, json.Unmarshal(b, &hc))
	assert.Equal(t, int32(10), hc.PeerCount)

	require.NoError(t, err)

	payload, err := json.Marshal(&guber.GetRateLimitsReq{
		Requests: []*guber.RateLimitReq{
			{
				Name:      "requests_per_sec",
				UniqueKey: "account:12345",
				Duration:  guber.Millisecond * 1000,
				Hits:      1,
				Limit:     10,
			},
		},
	})
	require.NoError(t, err)

	resp, err = http.DefaultClient.Post("http://"+address+"/v1/GetRateLimits",
		"application/json", bytes.NewReader(payload))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	b, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	var r guber.GetRateLimitsResp

	// NOTE: It is important to use 'protojson' instead of the standard 'json' package
	//  else the enums will not be converted properly and json.Unmarshal() will return an
	//  error.
	require.NoError(t, json.Unmarshal(b, &r))
	require.Equal(t, 1, len(r.Responses))
	assert.Equal(t, guber.Status_UNDER_LIMIT, r.Responses[0].Status)
}

func TestGetPeerRateLimits(t *testing.T) {
	ctx := context.Background()
	peerClient := guber.NewPeerClient(guber.PeerConfig{
		Info: cluster.GetRandomPeer(cluster.DataCenterNone),
	})

	t.Run("Stable rate check request order", func(t *testing.T) {
		// Ensure response order matches rate check request order.
		// Try various batch sizes.
		testCases := []int{1, 2, 5, 10, 100, 1000}

		for _, n := range testCases {
			t.Run(fmt.Sprintf("Batch size %d", n), func(t *testing.T) {
				// Build request.
				req := &guber.GetPeerRateLimitsReq{
					Requests: make([]*guber.RateLimitReq, n),
				}
				for i := 0; i < n; i++ {
					req.Requests[i] = &guber.RateLimitReq{
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
				resp, err := peerClient.GetPeerRateLimits(ctx, req)

				// Verify.
				require.NoError(t, err)
				require.NotNil(t, resp)
				assert.Len(t, resp.RateLimits, n)

				for i, item := range resp.RateLimits {
					// Identify response by its unique limit.
					assert.Equal(t, req.Requests[i].Limit, item.Limit)
				}
			})
		}
	})
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

type staticBuilder struct{}

var _ resolver.Builder = (*staticBuilder)(nil)

func (sb *staticBuilder) Scheme() string {
	return "static"
}

func (sb *staticBuilder) Build(target resolver.Target, cc resolver.ClientConn, _ resolver.BuildOptions) (resolver.Resolver, error) {
	var resolverAddrs []resolver.Address
	for _, address := range strings.Split(target.Endpoint(), ",") {
		resolverAddrs = append(resolverAddrs, resolver.Address{
			Addr:       address,
			ServerName: address,
		})
	}
	if err := cc.UpdateState(resolver.State{Addresses: resolverAddrs}); err != nil {
		return nil, err
	}
	return &staticResolver{cc: cc}, nil
}

// newStaticBuilder returns a builder which returns a staticResolver that tells GRPC
// to connect a specific peer in the cluster.
func newStaticBuilder() resolver.Builder {
	return &staticBuilder{}
}

type staticResolver struct {
	cc resolver.ClientConn
}

func (sr *staticResolver) ResolveNow(_ resolver.ResolveNowOptions) {}

func (sr *staticResolver) Close() {}

var _ resolver.Resolver = (*staticResolver)(nil)

// findNonOwningPeer returns peer info for a peer in the cluster which does not
// own the rate limit for the name and key provided.
func findNonOwningPeer(name, key string) (guber.PeerInfo, error) {
	owner, err := cluster.FindOwningPeer(name, key)
	if err != nil {
		return guber.PeerInfo{}, err
	}

	for _, p := range cluster.GetPeers() {
		if p.HashKey() != owner.HashKey() {
			return p, nil
		}
	}
	return guber.PeerInfo{}, fmt.Errorf("unable to find non-owning peer in '%d' node cluster",
		len(cluster.GetPeers()))
}

// getClientToNonOwningPeer returns a connection to a peer in the cluster which does not own
// the rate limit for the name and key provided.
func getClientToNonOwningPeer(name, key string) (guber.V1Client, error) {
	p, err := findNonOwningPeer(name, key)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.DialContext(context.Background(),
		fmt.Sprintf("static:///%s", p.GRPCAddress),
		grpc.WithResolvers(newStaticBuilder()),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return guber.NewV1Client(conn), nil

}
