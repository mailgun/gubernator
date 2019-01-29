package gubernator

import (
	"context"
	"github.com/mailgun/gubernator/pb"
	"github.com/pkg/errors"
	"github.com/valyala/bytebufferpool"
	"google.golang.org/grpc"
	"sync"
)

type UserClient struct {
	cluster ClusterConfiger
	picker  PeerPicker
	mutex   sync.Mutex
}

func NewClient() *UserClient {
	c := UserClient{}

	return &c
	/*errs := ClientError{}

	// Connect to all the peers we know about
	var peerInfo []*PeerInfo
	for _, peer := range peers {
		info, err := newPeerConnection(peer)
		if err != nil {
			errs.Add(err)
			continue
		}
		peerInfo = append(peerInfo, info)
	}

	if errs.Size() == len(peers) {
		return nil, errs.Err(errors.New("unable to connect to any peers"))
	}*/
}

// Return the size of the cluster as reported by the cluster config
func (c *UserClient) ClusterSize() int {
	return c.picker.Size()
}

func (c *UserClient) ForwardRequest(ctx context.Context, peer *PeerInfo, r *pb.RateLimitKeyRequest_Entry) (*pb.DescriptorStatus, error) {
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

	resp, err := peer.rsClient.GetRateLimitByKey(ctx, &pb.RateLimitKeyRequest{
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

func (c *UserClient) generateKey(b *bytebufferpool.ByteBuffer, domain string, descriptor *pb.Descriptor) error {

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

// Updates the list of peers in the cluster with the following semantics
//  * Any peer that is not in the list provided will be disconnected and removed from the list of peers.
//  * Any peer that fails to connect it will not be added to the list of peers.
//  * If return map is not nil, the map contains the error for each peer that failed to connect.
func (c *UserClient) Update(hosts []string) map[string]error {
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
		Host:     hostName,
		conn:     conn,
		rsClient: pb.NewRateLimitServiceClient(conn),
	}, nil
}
