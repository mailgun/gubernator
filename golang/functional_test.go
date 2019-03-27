package gubernator_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	guber "github.com/mailgun/gubernator/golang"
	"github.com/mailgun/gubernator/golang/cluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Setup and shutdown the mailgun mock server for the entire test suite
func TestMain(m *testing.M) {
	if err := cluster.Start(5); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer cluster.Stop()
	os.Exit(m.Run())
}

func TestOverTheLimit(t *testing.T) {
	client, errs := guber.NewV1Client(cluster.GetPeer())
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
	client, errs := guber.NewV1Client(cluster.GetPeer())
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

		assert.Equal(t, test.Status, rl.Status)
		assert.Equal(t, test.Remaining, rl.Remaining)
		assert.Equal(t, int64(2), rl.Limit)
		assert.True(t, rl.ResetTime != 0)
		time.Sleep(test.Sleep)
	}
}

func TestLeakyBucket(t *testing.T) {
	client, errs := guber.NewV1Client(cluster.GetPeer())
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

	for _, test := range tests {
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

		assert.Equal(t, test.Status, rl.Status)
		assert.Equal(t, test.Remaining, rl.Remaining)
		assert.Equal(t, int64(5), rl.Limit)
		time.Sleep(test.Sleep)
	}
}

func TestMissingFields(t *testing.T) {
	client, errs := guber.NewV1Client(cluster.GetPeer())
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

// TODO: Add a test for sending no rate limits RateLimitReqList.RateLimits = nil
