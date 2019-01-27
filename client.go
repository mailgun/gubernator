package gubernator

import (
	"context"
	"github.com/mailgun/gubernator/pb"
	"github.com/pkg/errors"
	"github.com/valyala/bytebufferpool"
	"google.golang.org/grpc"
	"sync"
)

const (
	Second = 1000
	Minute = 60 * Second
	Hour   = 60 * Minute
)

type Client struct {
	picker PeerPicker
	mutex  sync.Mutex
}

func NewClient(hosts []string) (*Client, map[string]error) {
	// TODO: Have NewClient() pick a random peer and ask that peer to provide a list of peers, then connect to all peers
	// TODO: Provide an option to skip this step ^^  gubernator.SkipFetchPeers() for servers that want to use the client
	// TODO: Or users that want to manage the peers themselves

	// TODO: ^^ DONT DO THIS... I THINK. Instead call a call back func to get the peers

	// Connect to all the peers
	var peers []*PeerInfo
	errs := make(map[string]error)
	for _, host := range hosts {
		peer, err := newPeerConnection(host)
		if err != nil {
			errs[host] = err
			continue
		}
		peers = append(peers, peer)
	}

	if len(errs) == 0 {
		errs = nil
	}

	return &Client{
		picker: newConsitantHashPicker(peers, nil),
	}, errs
}

// Return the size of the cluster
func (c *Client) IsConnected() bool {
	return c.picker.Size() != 0
}

func (c *Client) RateLimit(ctx context.Context, domain string, descriptor *pb.Descriptor) (*pb.DescriptorStatus, error) {
	var key bytebufferpool.ByteBuffer

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
	}

	// TODO: combine requests if called multiple times within a few miliseconds.

	var peer PeerInfo
	if err := c.picker.Get(keyReq.Key, &peer); err != nil {
		return nil, err
	}
	//fmt.Printf("Key: '%s' Pick: %s\n", key.Bytes(), peer.HostName)

	resp, err := peer.client.GetRateLimitByKey(ctx, &pb.RateLimitKeyRequest{
		Entries: []*pb.RateLimitKeyRequest_Entry{&keyReq},
	})

	// TODO: put buffer back into pool

	if err != nil {
		return nil, err
	}

	if len(resp.Statuses) == 0 {
		return nil, errors.New("server responded with empty descriptor status")
	}

	return resp.Statuses[0], nil
}

func (c *Client) generateKey(b *bytebufferpool.ByteBuffer, domain string, descriptor *pb.Descriptor) error {

	// TODO: Check provided Domain
	// TODO: Check provided at least one entry

	b.Reset()
	b.WriteString(domain)
	b.WriteByte('_')

	for _, entry := range descriptor.Entries {
		b.WriteString(entry.Key)
		b.WriteByte('_')
		b.WriteString(entry.Value)
		b.WriteByte('_')
	}
	return nil
}

// Updates the list of peers in the cluster with the following semantics
//  * Any peer that is not in the list provided will be disconnected and removed from the list of peers.
//  * Any peer that fails to connect it will not be added to the list of peers.
//  * If return map is not nil, the map contains the error for each peer that failed to connect.
func (c *Client) Update(hosts []string) map[string]error {
	errs := make(map[string]error)
	var peers []*PeerInfo
	for _, host := range hosts {
		peer := c.picker.GetPeer(host)
		var err error

		if peer == nil {
			peer, err = newPeerConnection(host)
			if err != nil {
				errs[host] = err
				continue
			}
		}
		peers = append(peers, peer)
	}

	// TODO: schedule a disconnect for old peers once they are no longer in flight

	c.mutex.Lock()
	// Create a new picker based on consistent hash algorithm
	c.picker = newConsitantHashPicker(peers, nil)
	c.mutex.Unlock()

	if len(errs) != 0 {
		return errs
	}
	return nil
}

// Given a host, return a PeerInfo with an initialized GRPC client
func newPeerConnection(hostName string) (*PeerInfo, error) {
	// TODO: Allow TLS connections
	conn, err := grpc.Dial(hostName, grpc.WithInsecure())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to dial peer %s", hostName)
	}

	return &PeerInfo{
		HostName: hostName,
		conn:     conn,
		client:   pb.NewRateLimitServiceClient(conn),
	}, nil
}
