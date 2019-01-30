package gubernator

import (
	"context"
	"errors"
	"math/rand"
	"time"

	"github.com/mailgun/gubernator/pb"
)

type Status int

const (
	Unknown    Status = 0
	UnderLimit Status = 1
	OverLimit  Status = 2
)

// A thin client over the GetRateLimit() GRPC call.
// TODO: Will batch requests for performance, once peer.PeerClient supports it
type Client struct {
	client *PeerClient
	domain string
}

// Creates a new simple client with a domain
func NewClient(domain string, peers []string) (*Client, error) {
	if len(peers) == 0 {
		return nil, errors.New("peer list is empty; must provide atleast one peer")
	}

	// Randomize the order of the peers
	rand.Shuffle(len(peers), func(i, j int) {
		peers[i], peers[j] = peers[j], peers[i]
	})

	return &Client{
		domain: domain,
		client: NewPeerClient(peers[0]),
	}, nil
}

type Request struct {
	// Descriptors that identify this rate limit
	Descriptors map[string]string
	// Number of requests allowed for this rate limit request
	Limit int64
	// The length of the duration
	Duration time.Duration
	// How many hits to send to the rate limit server
	Hits int64
}

type Response struct {
	// The current limit imposed on this rate limit
	CurrentLimit int64
	// The number of remaining hits in this rate limit (provided by GetRateLimit)
	LimitRemaining int64
	// The time when the rate limit duration resets (provided by GetRateLimit)
	ResetTime time.Time
	// Indicates if the requested hit is over the limit
	Status Status
}

func (c *Client) GetRateLimit(ctx context.Context, req *Request) (*Response, error) {
	status, err := c.client.GetRateLimit(ctx, c.domain, &pb.Descriptor{
		RateLimit: &pb.RateLimitDuration{
			Requests: req.Limit,
			Duration: int64(req.Duration / time.Millisecond),
		},
		Values: req.Descriptors,
		Hits:   req.Hits,
	})
	if err != nil {
		return nil, err
	}
	return &Response{
		Status:         Status(status.Status),
		ResetTime:      time.Unix(0, status.ResetTime*int64(time.Millisecond)),
		LimitRemaining: status.LimitRemaining,
		CurrentLimit:   status.CurrentLimit,
	}, nil
}
