package gubernator

import (
	"context"
	"math/rand"
	"time"

	"github.com/mailgun/gubernator/golang/pb"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
)

type Status int
type Algorithm int

const (
	UnderLimit Status = 0
	OverLimit  Status = 1

	TokenBucket Algorithm = 0
	LeakyBucket Algorithm = 1
)

// A thin wrapper over the GRPC client interface
type Client struct {
	client pb.RateLimitServiceClient
}

type Request struct {
	// The namespace the unique key is in
	Namespace string
	// A unique key that identifies this rate limit
	UniqueKey string
	// Number of requests allowed for this rate limit request
	Limit int64
	// The length of the duration
	Duration time.Duration
	// How many hits to send to the rate limit server
	Hits int64
	// The Algorithm used to calculate the rate limit
	Algorithm Algorithm
}

type Response struct {
	// The current limit imposed on this rate limit
	CurrentLimit int64
	// The number of remaining hits in this rate limit
	LimitRemaining int64
	// The time stamp when the rate limit resets
	ResetTime time.Time
	// Indicates if the requested hit is over the limit
	Status Status
	// If set contains the error for this request
	Error string
}

// Create a new connection to the server
func NewClient(server string) (*Client, error) {
	if len(server) == 0 {
		return nil, errors.New("server is empty; must provide a server")
	}

	conn, err := grpc.Dial(server, grpc.WithInsecure())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to dial peer %s", server)
	}

	return &Client{
		client: pb.NewRateLimitServiceClient(conn),
	}, nil
}

func (c *Client) GetClient() pb.RateLimitServiceClient {
	return c.client
}

func (c *Client) Ping(ctx context.Context) error {
	_, err := c.client.HealthCheck(ctx, &pb.HealthCheckRequest{})
	return err
}

// Get a single rate limit
func (c *Client) GetRateLimit(ctx context.Context, req *Request) (*Response, error) {
	resp, err := c.client.GetRateLimits(ctx, &pb.RateLimitRequestList{
		RateLimits: []*pb.RateLimitRequest{
			{
				Namespace: req.Namespace,
				UniqueKey: req.UniqueKey,
				Hits:      req.Hits,
				RateLimitConfig: &pb.RateLimitConfig{
					Limit:     req.Limit,
					Duration:  ToTimeStamp(req.Duration),
					Algorithm: pb.RateLimitConfig_Algorithm(req.Algorithm),
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}

	if len(resp.RateLimits) == 0 {
		return nil, errors.New("server responded with empty rate limit response")
	}

	result := resp.RateLimits[0]
	return &Response{
		Status:         Status(result.Status),
		ResetTime:      FromUnixMilliseconds(result.ResetTime),
		LimitRemaining: result.LimitRemaining,
		CurrentLimit:   result.CurrentLimit,
		Error:          result.Error,
	}, nil
}

// Convert a time.Duration to a unix millisecond timestamp
func ToTimeStamp(duration time.Duration) int64 {
	return int64(duration / time.Millisecond)
}

// Convert a unix millisecond timestamp to a time.Duration
func FromTimeStamp(ts int64) time.Duration {
	return time.Now().Sub(FromUnixMilliseconds(ts))
}

func FromUnixMilliseconds(ts int64) time.Time {
	return time.Unix(0, ts*int64(time.Millisecond))
}

// Given a list of peers, return a random peer
func RandomPeer(peers []string) string {
	rand.Shuffle(len(peers), func(i, j int) {
		peers[i], peers[j] = peers[j], peers[i]
	})
	return peers[0]
}

// Return a random alpha string of 'n' length
func RandomString(n int) string {
	const alphanum = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	var bytes = make([]byte, n)
	rand.Read(bytes)
	for i, b := range bytes {
		bytes[i] = alphanum[b%byte(len(alphanum))]
	}
	return string(bytes)
}
