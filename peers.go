package gubernator

import (
	"github.com/mailgun/gubernator/proto"
	"github.com/valyala/bytebufferpool"
	"google.golang.org/grpc"
)

type PeerPicker interface {
	Get(*bytebufferpool.ByteBuffer, *PeerInfo) error
	GetPeer(host string) *PeerInfo
	Size() int
}

type PeerInfo struct {
	client   proto.RateLimitServiceClient
	conn     *grpc.ClientConn
	HostName string
}
