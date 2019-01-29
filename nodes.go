package gubernator

import (
	"github.com/mailgun/gubernator/pb"
	"google.golang.org/grpc"
)

type PeerPicker interface {
	GetPeer(host string) *PeerInfo
	Get([]byte, *PeerInfo) error
	New() PeerPicker
	Add(*PeerInfo)
	Size() int
}

type PeerInfo struct {
	rsClient pb.RateLimitServiceClient
	conn     *grpc.ClientConn
	Host     string
}
