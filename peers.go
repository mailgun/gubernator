package gubernator

import (
	"github.com/mailgun/gubernator/pb"
	"google.golang.org/grpc"
)

type PeerPicker interface {
	Get([]byte, *PeerInfo) error
	GetPeer(host string) *PeerInfo
	Size() int
}

type PeerInfo struct {
	client   pb.RateLimitServiceClient
	conn     *grpc.ClientConn
	HostName string
}
