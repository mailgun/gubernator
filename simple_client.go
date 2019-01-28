package gubernator

import (
	"context"
	"errors"
	"math/rand"
	"time"

	"github.com/mailgun/gubernator/pb"
)

// A simple client that picks a random node in the cluster and makes all
// requests through that node. This client preforms no key optimization and
// does not participate in node list syncing but is the simplest client to use.
type SimpleClient struct {
	connectedNode *PeerInfo
	nodes         []string
	domain        string
}

// Creates a new simple client with a domain
func NewSimpleClient(domain string, nodes []string) *SimpleClient {
	// Randomize the order of the nodes
	rand.Shuffle(len(nodes), func(i, j int) {
		nodes[i], nodes[j] = nodes[j], nodes[i]
	})

	return &SimpleClient{
		domain: domain,
		nodes:  nodes,
	}
}

type Status int

const (
	Unknown   Status = 0
	OK        Status = 1
	OverLimit Status = 2
)

type Request struct {
	// Descriptors that identify this rate limit
	Descriptors map[string]string
	// Number of requests allowed for this rate limit request
	Requests int64
	// The length of the duration
	Duration time.Duration
	// How many hits to send to the rate limit server
	Hit int64
}

type Response struct {
	// The number of remaining hits in this rate limit (provided by GetRateLimit)
	LimitRemaining int64
	// The time when the rate limit duration resets (provided by GetRateLimit)
	Reset time.Time
	// Indicates if the requested hit is over the limit
	Status Status
}

func (sc *SimpleClient) GetRateLimit(ctx context.Context, req *Request) (*Response, error) {
	if sc.connectedNode == nil {
		if err := sc.connect(); err != nil {
			return nil, err
		}
	}

	resp, err := sc.connectedNode.rsClient.GetRateLimit(ctx, &pb.RateLimitRequest{
		Descriptors: []*pb.Descriptor{
			{
				RateLimit: &pb.RateLimitDuration{
					Requests: req.Requests,
					Duration: int64(req.Duration / time.Millisecond),
				},
				Values: req.Descriptors,
				Hits:   req.Hit,
			},
		},
	})
	if err != nil {
		return nil, err
	}

	if len(resp.Statuses) == 0 {
		return nil, errors.New("server responded with empty descriptor status")
	}

	status := resp.Statuses[0]
	return &Response{
		Status:         Status(status.Status),
		Reset:          time.Unix(0, status.ResetTime*int64(time.Millisecond)),
		LimitRemaining: status.LimitRemaining,
	}, nil
}

// Attempt to connect to any node in our node list
func (sc *SimpleClient) connect() error {
	var err error

	errs := ClientError{}
	for _, node := range sc.nodes {
		sc.connectedNode, err = newPeerConnection(node)
		if err != nil {
			errs.Add(err)
		}
		break
	}
	return nil
}
