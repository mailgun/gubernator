package gubernator

import (
	"context"
	"github.com/mailgun/gubernator/proto"
	"github.com/valyala/bytebufferpool"
)

type Client struct {
	cluster *Cluster
}

func NewClient(peers []string) (*Client, map[string]error) {
	cluster, errs := NewCluster(peers)
	return &Client{cluster: cluster}, errs
}

func (c *Client) IsConnected() bool {
	if c.cluster.Size() == 0 {
		return false
	}
	return true
}

func (c *Client) RateLimit(ctx context.Context, req *proto.RateLimitRequest) (*proto.RateLimitResponse, error) {
	var key bytebufferpool.ByteBuffer

	// TODO: Keep key buffers in a buffer pool to avoid un-necessary garbage collection
	// Or Keep proto.KeyRequests in a pool

	// Generate key from the request
	if err := c.generateKey(&key, req); err != nil {
		return nil, err
	}

	keyReq := proto.RateLimitKeyRequest{
		Key:        key.Bytes(),
		HitsAddend: req.HitsAddend,
	}

	return c.cluster.GetRateLimitByKey(ctx, &keyReq)
}

func (c *Client) generateKey(b *bytebufferpool.ByteBuffer, req *proto.RateLimitRequest) error {

	// TODO: Check provided Domain
	// TODO: Check provided at least one descriptor

	b.Reset()
	b.WriteString(req.Domain)
	b.WriteByte('_')

	for _, des := range req.Descriptors {
		for _, entry := range des.Entries {
			b.WriteString(entry.Key)
			b.WriteByte('_')
			b.WriteString(entry.Value)
			b.WriteByte('_')
		}
	}
	return nil
}
