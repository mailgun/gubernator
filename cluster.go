package gubernator

import (
	"context"
	"github.com/mailgun/gubernator/proto"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"sync"
)

type Cluster struct {
	picker PeerPicker
	mutex  sync.Mutex
}

// Create a new cluster using the peers provided
func NewCluster(peers []string) (*Cluster, map[string]error) {
	cl := Cluster{}
	errs := cl.Update(peers)
	if errs != nil {
		return &cl, errs
	}
	return &cl, nil
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

func (cl *Cluster) GetRateLimitByKey(ctx context.Context, keyReq *proto.RateLimitKeyRequest) (*proto.RateLimitResponse, error) {
	// TODO: combine requests if called multiple times within a few miliseconds.

	// TODO: Refactor GetRateLimitByKey() to accept multiple requests at a time.

	// TODO: Make the call

	return nil, nil
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
		client:   proto.NewRateLimitServiceClient(conn),
	}, nil
}
