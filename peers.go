package gubernator

import (
	"context"
	"github.com/mailgun/gubernator/pb"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
)

type PeerPicker interface {
	GetPeer(host string) *PeerClient
	Peers() []*PeerClient
	Get(string, *PeerClient) error
	New() PeerPicker
	Add(*PeerClient)
	Size() int
}

type PeerClient struct {
	client  pb.PeersServiceClient
	conn    *grpc.ClientConn
	host    string
	isOwner bool // true if this peer refers to this server instance
}

func NewPeerClient(host string) *PeerClient {
	return &PeerClient{
		host: host,
	}
}

// TODO: Eventually this will take multiple requests made concurrently and batch them into a single request
// TODO: This will reduce the propagation of thundering heard effect to peers in our cluster.
// TODO: When implementing batching, allow upstream context cancels to abort processing for an request,  not the entire
// TODO: batch request
func (c *PeerClient) GetPeerRateLimits(ctx context.Context, r *pb.RateLimitRequest) (*pb.RateLimitResponse, error) {
	if c.conn == nil {
		if err := c.dialPeer(); err != nil {
			return nil, err
		}
	}

	resp, err := c.client.GetPeerRateLimits(ctx, &pb.PeerRateLimitRequest{
		RateLimits: []*pb.RateLimitRequest{r},
	})

	if err != nil {
		return nil, err
	}

	if len(resp.RateLimits) == 0 {
		return nil, errors.New("server responded with empty rate limit list")
	}

	return resp.RateLimits[0], nil
}

// Dial to a peer and initialize the GRPC client
func (c *PeerClient) dialPeer() error {
	// TODO: Allow TLS connections

	var err error
	c.conn, err = grpc.Dial(c.host, grpc.WithInsecure())
	if err != nil {
		return errors.Wrapf(err, "failed to dial peer %s", c.host)
	}

	c.client = pb.NewPeersServiceClient(c.conn)
	return nil
}
