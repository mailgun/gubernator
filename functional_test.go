package gubernator_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/mailgun/gubernator"
	"github.com/mailgun/gubernator/pb"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var peers []string
var servers []*gubernator.Server

func startCluster() error {
	for i := 0; i < 6; i++ {
		srv, err := gubernator.NewServer("")
		if err != nil {
			return errors.Wrap(err, "NewServer()")
		}
		peers = append(peers, srv.Address())
		go srv.Run()
		servers = append(servers, srv)
	}
	return nil
}

func stopCluster() {
	for _, srv := range servers {
		srv.Stop()
	}
}

// Setup and shutdown the mailgun mock server for the entire test suite
func TestMain(m *testing.M) {
	if err := startCluster(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer stopCluster()
	os.Exit(m.Run())
}

func TestOverTheLimit(t *testing.T) {
	client, errs := gubernator.NewClient(peers)
	require.Nil(t, errs)

	tests := []struct {
		Remaining int64
		Status    pb.DescriptorStatus_Code
	}{
		{
			Remaining: 1,
			Status:    pb.DescriptorStatus_OK,
		},
		{
			Remaining: 0,
			Status:    pb.DescriptorStatus_OK,
		},
		{
			Remaining: 0,
			Status:    pb.DescriptorStatus_OVER_LIMIT,
		},
	}

	for _, test := range tests {
		resp, err := client.RateLimit(context.Background(), "domain", &pb.Descriptor{
			Values: map[string]string{
				"account": "1234",
			},
			RateLimit: &pb.RateLimitDuration{
				Requests: 2,
				Duration: gubernator.Second * 1,
			},
			Hits: 1,
		})
		require.Nil(t, err)

		assert.Equal(t, test.Status, resp.Status)
		assert.Equal(t, test.Remaining, resp.LimitRemaining)
		assert.Equal(t, int64(2), resp.CurrentLimit)
		assert.True(t, resp.ResetTime != 0)
	}
}

func TestUnderLimitDuration(t *testing.T) {
	client, errs := gubernator.NewClient(peers)
	require.Nil(t, errs)

	tests := []struct {
		Remaining int64
		Status    pb.DescriptorStatus_Status
		Sleep     time.Duration
	}{
		{
			Remaining: 1,
			Status:    pb.DescriptorStatus_OK,
			Sleep:     time.Duration(0),
		},
		{
			Remaining: 0,
			Status:    pb.DescriptorStatus_OK,
			Sleep:     time.Duration(time.Millisecond * 5),
		},
		{
			Remaining: 1,
			Status:    pb.DescriptorStatus_OK,
			Sleep:     time.Duration(0),
		},
	}

	for _, test := range tests {
		resp, err := client.RateLimit(context.Background(), "domain", &pb.Descriptor{
			Values: map[string]string{
				"account": "1234",
			},
			RateLimit: &pb.RateLimitDuration{
				Requests: 2,
				Duration: gubernator.Millisecond * 5,
			},
			Hits: 1,
		})
		require.Nil(t, err)

		assert.Equal(t, test.Status, resp.Status)
		assert.Equal(t, test.Remaining, resp.LimitRemaining)
		assert.Equal(t, int64(2), resp.CurrentLimit)
		assert.True(t, resp.ResetTime != 0)
		time.Sleep(test.Sleep)
	}
}
