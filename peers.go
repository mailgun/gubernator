package gubernator

import (
	"context"
	"errors"
	"github.com/mailgun/gubernator/pb"
	"github.com/valyala/bytebufferpool"
	"google.golang.org/grpc"
	"sync"
)

type PeerPicker interface {
	GetPeer(host string) *PeerInfo
	Get([]byte, *PeerInfo) error
	New() PeerPicker
	Add(*PeerInfo)
	Size() int
}

type PeerInfo struct {
	Client pb.RateLimitServiceClient
	conn   *grpc.ClientConn
	Host   string
}

// TODO: Eventually this client will take multiple requests made concurrently and batch them into a single request
// TODO: This will reduce the propagation of thundering heard effect to peers in our cluster.
type PeerClient struct {
	mutex sync.Mutex
}

func NewPeerClient() *PeerClient {
	return &PeerClient{}
}

// TODO: Given a single rate limit descriptor, batch similar requests into a single request and return the result to callers when complete
func (c *PeerClient) GetRateLimit(ctx context.Context, peer *PeerInfo, d *pb.Descriptor) (*pb.DescriptorStatus, error) {
	resp, err := peer.Client.GetRateLimit(ctx, &pb.RateLimitRequest{
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
func (c *PeerClient) GetRateLimitByKey(ctx context.Context, peer *PeerInfo, r *pb.RateLimitKeyRequest_Entry) (*pb.DescriptorStatus, error) {
	/*var key bytebufferpool.ByteBuffer

	// TODO: Keep key buffers in a buffer pool to avoid un-necessary garbage collection
	// Or Keep pb.KeyRequests in a pool

	// Generate key from the request
	if err := c.generateKey(&key, domain, descriptor); err != nil {
		return nil, err
	}

	keyReq := pb.RateLimitKeyRequest_Entry{
		Key:       key.Bytes(),
		Hits:      descriptor.Hits,
		RateLimit: descriptor.RateLimit,
	}*/

	// TODO: combine requests if called multiple times within a few milliseconds.

	/*// TODO: Lock before accessing the picker
	var peer PeerInfo
	if err := c.picker.Get(r.Key, &peer); err != nil {
		return nil, err
	}
	//fmt.Printf("Key: '%s' Pick: %s\n", key.Bytes(), peer.Host)*/

	// TODO: If PeerInfo is not connected, Then dial the server

	resp, err := peer.Client.GetRateLimitByKey(ctx, &pb.RateLimitKeyRequest{
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

func (c *PeerClient) generateKey(b *bytebufferpool.ByteBuffer, domain string, descriptor *pb.Descriptor) error {

	// TODO: Check provided Domain
	// TODO: Check provided at least one entry

	b.Reset()
	b.WriteString(domain)
	b.WriteByte('_')

	for key, value := range descriptor.Values {
		b.WriteString(key)
		b.WriteByte('_')
		b.WriteString(value)
		b.WriteByte('_')
	}
	return nil
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
