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

func (c *Client) RateLimit(ctx context.Context, domain string, hits uint32, entries []*proto.RateLimitDescriptor_Entry) (*proto.RateLimitResponse, error) {
	var key bytebufferpool.ByteBuffer

	// TODO: Keep key buffers in a buffer pool to avoid un-necessary garbage collection
	// Or Keep proto.KeyRequests in a pool

	// Generate key from the request
	if err := c.generateKey(&key, domain, entries); err != nil {
		return nil, err
	}

	/*keyReq := proto.RateLimitKeyRequest{
		Key:        key.Bytes(),
		HitsAddend: hits,
	}*/

	return c.cluster.GetRateLimitByKey(ctx, &key, hits)
}

func (c *Client) generateKey(b *bytebufferpool.ByteBuffer, domain string, entries []*proto.RateLimitDescriptor_Entry) error {

	// TODO: Check provided Domain
	// TODO: Check provided at least one entry

	b.Reset()
	b.WriteString(domain)
	b.WriteByte('_')

	for _, entry := range entries {
		b.WriteString(entry.Key)
		b.WriteByte('_')
		b.WriteString(entry.Value)
		b.WriteByte('_')
	}
	return nil
}
