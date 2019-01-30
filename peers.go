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
	Get([]byte, *PeerClient) error
	New() PeerPicker
	Add(*PeerClient)
	Size() int
}

// TODO: Eventually this client will take multiple requests made concurrently and batch them into a single request
// TODO: This will reduce the propagation of thundering heard effect to peers in our cluster.
// TODO: When implementing batching, allow upstream context cancels to abort processing for an request,  not the entire
// TODO: batch request
type PeerClient struct {
	client  pb.RateLimitServiceClient
	conn    *grpc.ClientConn
	host    string
	isOwner bool // true if this peer refers to this server instance
}

func NewPeerClient(host string) *PeerClient {
	return &PeerClient{
		host: host,
	}
}

func (c *PeerClient) GetRateLimit(ctx context.Context, domain string, d *pb.Descriptor) (*pb.DescriptorStatus, error) {
	if c.conn == nil {
		if err := c.dialPeer(); err != nil {
			return nil, err
		}
	}

	resp, err := c.client.GetRateLimit(ctx, &pb.RateLimitRequest{
		Domain:      domain,
		Descriptors: []*pb.Descriptor{d},
	})
	if err != nil {
		return nil, err
	}

	if len(resp.Statuses) == 0 {
		return nil, errors.New("server responded with empty descriptor status")
	}

	return resp.Statuses[0], nil
}

func (c *PeerClient) GetRateLimitByKey(ctx context.Context, r *pb.RateLimitKeyRequest_Entry) (*pb.DescriptorStatus, error) {
	if c.conn == nil {
		if err := c.dialPeer(); err != nil {
			return nil, err
		}
	}

	resp, err := c.client.GetRateLimitByKey(ctx, &pb.RateLimitKeyRequest{
		Entries: []*pb.RateLimitKeyRequest_Entry{r},
	})

	if err != nil {
		return nil, err
	}

	if len(resp.Statuses) == 0 {
		return nil, errors.New("server responded with empty descriptor status")
	}

	return resp.Statuses[0], nil
}

// Dial to a peer and initialize the GRPC client
func (c *PeerClient) dialPeer() error {
	// TODO: Allow TLS connections

	var err error
	c.conn, err = grpc.Dial(c.host, grpc.WithInsecure())
	if err != nil {
		return errors.Wrapf(err, "failed to dial peer %s", c.host)
	}

	c.client = pb.NewRateLimitServiceClient(c.conn)
	return nil
}
