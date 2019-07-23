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

// Setup and shutdown the mailgun mock server for the entire test suite
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
	client, errs := guber.DialV1Server(cluster.GetPeer())
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
	client, errs := guber.DialV1Server(cluster.GetPeer())
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
	client, errs := guber.DialV1Server(cluster.GetPeer())
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
		time.Sleep(test.Sleep)
	}
}

func TestMissingFields(t *testing.T) {
	client, errs := guber.DialV1Server(cluster.GetPeer())
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
	client, errs := guber.DialV1Server(cluster.PeerAt(0))
	require.Nil(t, errs)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()

	sendHit := func(status guber.Status, remain int64, i int) {
		resp, err := client.GetRateLimits(ctx, &guber.GetRateLimitsReq{
			Requests: []*guber.RateLimitReq{
				{
					// For this test this name/key should ensure our connected peer is NOT the owner,
					// the peer we are connected to should forward requests asynchronously to the owner.
					Name:      "test_global",
					UniqueKey: "account:1234",
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
	}

	// Our first hit should create the request on the peer and queue for async forward
	sendHit(guber.Status_UNDER_LIMIT, 4, 1)

	// Our second hit should return the same response as the first since the async forward hasn't occurred yet
	sendHit(guber.Status_UNDER_LIMIT, 4, 2)

	time.Sleep(time.Second)

	// Our second hit should return the accurate response as the async forward should have completed and the owner
	// will have updated us with the latest counts
	sendHit(guber.Status_UNDER_LIMIT, 3, 3)

	// Inspect our metrics, ensure they collected the counts we expected during this test
	instance := cluster.InstanceAt(0)
	metricCh := make(chan prometheus.Metric, 5)
	instance.Guber.Collect(metricCh)

	buf := dto.Metric{}
	m := <-metricCh // Async metric
	assert.Nil(t, m.Write(&buf))
	assert.Equal(t, uint64(1), *buf.Histogram.SampleCount)

	instance = cluster.InstanceAt(3)
	metricCh = make(chan prometheus.Metric, 5)
	instance.Guber.Collect(metricCh)

	m = <-metricCh // Async metric
	m = <-metricCh // Broadcast metric
	assert.Nil(t, m.Write(&buf))
	assert.Equal(t, uint64(1), *buf.Histogram.SampleCount)
}

// TODO: Add a test for sending no rate limits RateLimitReqList.RateLimits = nil
