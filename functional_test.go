package gubernator_test

import (
	"context"
	"fmt"
	"github.com/mailgun/gubernator"
	"github.com/mailgun/gubernator/pb"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
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

	descriptor := &pb.RateLimitDescriptor{
		Entries: []*pb.RateLimitDescriptor_Entry{
			{
				Key:   "account",
				Value: "1234",
			},
		},
		RateLimit: &pb.RateLimit{
			RequestsPerSpan: 2,
			SpanInSeconds:   5,
		},
		Hits: 1,
	}

	resp, err := client.RateLimit(context.Background(), "domain", descriptor)

	require.Nil(t, err)
	assert.Equal(t, pb.DescriptorStatus_OK, resp.Code)
	assert.Equal(t, uint32(1), resp.LimitRemaining)
	assert.Equal(t, uint32(2), resp.CurrentLimit)
	assert.Equal(t, uint32(0), resp.OfHitsAccepted)
	assert.True(t, resp.ResetTime != 0)

	resp, err = client.RateLimit(context.Background(), "domain", descriptor)

	require.Nil(t, err)
	assert.Equal(t, pb.DescriptorStatus_OK, resp.Code)
	assert.Equal(t, uint32(0), resp.LimitRemaining)
	assert.Equal(t, uint32(2), resp.CurrentLimit)
	assert.Equal(t, uint32(0), resp.OfHitsAccepted)
	assert.True(t, resp.ResetTime != 0)

	resp, err = client.RateLimit(context.Background(), "domain", descriptor)

	require.Nil(t, err)
	assert.Equal(t, pb.DescriptorStatus_OVER_LIMIT, resp.Code)
	assert.Equal(t, uint32(0), resp.LimitRemaining)
	assert.Equal(t, uint32(2), resp.CurrentLimit)
	assert.Equal(t, uint32(0), resp.OfHitsAccepted)
	assert.True(t, resp.ResetTime != 0)

}
