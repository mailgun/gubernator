package gubernator

import (
	"context"
	"errors"
	"github.com/mailgun/gubernator/pb"
	"google.golang.org/grpc"
)

type PeerPicker interface {
	GetPeer(host string) *PeerClient
	Get([]byte, *PeerClient) error
	New() PeerPicker
	Add(*PeerClient)
	Size() int
}

// TODO: Eventually this client will take multiple requests made concurrently and batch them into a single request
// TODO: This will reduce the propagation of thundering heard effect to peers in our cluster.
type PeerClient struct {
	client pb.RateLimitServiceClient
	conn   *grpc.ClientConn
	Host   string
}

func NewPeerClient(host string) *PeerClient {
	return &PeerClient{
		Host: host,
	}
}

// TODO: Given a single rate limit descriptor, batch similar requests into a single request and return the result to callers when complete
func (c *PeerClient) GetRateLimit(ctx context.Context, d *pb.Descriptor) (*pb.DescriptorStatus, error) {

	// TODO: If PeerInfo is not connected, Then dial the server   <------ DO THIS NEXT

	resp, err := c.client.GetRateLimit(ctx, &pb.RateLimitRequest{
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

// TODO: Given a single rate limit key request entry, batch similar requests into a single request and return the result to callers when complete
func (c *PeerClient) GetRateLimitByKey(ctx context.Context, r *pb.RateLimitKeyRequest_Entry) (*pb.DescriptorStatus, error) {
	// TODO: If PeerInfo is not connected, Then dial the server

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

// Given a host, return a PeerInfo with an initialized GRPC client
/*func newPeerConnection(hostName string) (*gubernator.PeerInfo, error) {
	// TODO: Allow TLS connections
	conn, err := grpc.Dial(hostName, grpc.WithInsecure())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to dial peer %s", hostName)
	}

	return &gubernator.PeerInfo{
		Host:   hostName,
		conn:   conn,
		PeerClient: pb.NewRateLimitServiceClient(conn),
	}, nil
}*/
