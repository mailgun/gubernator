package gubernator

import (
	"context"
	"github.com/mailgun/gubernator/pb"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"sync"
)

type Cluster struct {
	picker PeerPicker
	mutex  sync.Mutex
}

// Create a new cluster using the peers provided
func NewCluster(hosts []string) (*Cluster, map[string]error) {
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

	return &Cluster{
		picker: newConsitantHashPicker(peers, nil),
	}, errs
}

// Return the size of the cluster
func (cl *Cluster) Size() int {
	return cl.picker.Size()
}

// Updates the list of peers in the cluster.
// Any peer that is not in the list provided will be disconnected and removed from the cluster.
// Any peer that fails to connect will not be added to the cluster.
// If return map is not nil, the map contains the error for each peer that failed to connect.
func (cl *Cluster) Update(hosts []string) map[string]error {
	errs := make(map[string]error)
	var peers []*PeerInfo
	for _, host := range hosts {
		peer := cl.picker.GetPeer(host)
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

	cl.mutex.Lock()
	// Create a new picker based on consistent hash algorithm
	cl.picker = newConsitantHashPicker(peers, nil)
	cl.mutex.Unlock()

	if len(errs) != 0 {
		return errs
	}
	return nil
}

func (cl *Cluster) GetRateLimitByKey(ctx context.Context, req *pb.KeyRequestEntry) (*pb.RateLimitResponse, error) {
	// TODO: combine requests if called multiple times within a few miliseconds.

	var peer PeerInfo
	if err := cl.picker.Get(req.Key, &peer); err != nil {
		return nil, err
	}
	//fmt.Printf("Key: '%s' Pick: %s\n", key.Bytes(), peer.HostName)

	return peer.client.ShouldRateLimitByKey(ctx, &pb.RateLimitKeyRequest{
		Entries: []*pb.KeyRequestEntry{req},
	})
}

// Given a host, return a PeerInfo with an initialized GRPC client
func newPeerConnection(hostName string) (*PeerInfo, error) {
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
