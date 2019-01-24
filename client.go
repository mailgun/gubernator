package gubernator

import (
	"context"
	"github.com/mailgun/gubernator/pb"
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

func (c *Client) RateLimit(ctx context.Context, domain string, descriptor *pb.RateLimitDescriptor) (*pb.RateLimitResponse, error) {
	var key bytebufferpool.ByteBuffer

	// TODO: Keep key buffers in a buffer pool to avoid un-necessary garbage collection
	// Or Keep pb.KeyRequests in a pool

	// Generate key from the request
	if err := c.generateKey(&key, domain, descriptor); err != nil {
		return nil, err
	}

	keyReq := pb.KeyRequestEntry{
		Key:       key.Bytes(),
		Hits:      descriptor.Hits,
		RateLimit: descriptor.RateLimit,
	}

	return c.cluster.GetRateLimitByKey(ctx, &keyReq)

	// TODO: put buffer back into pool
}

func (c *Client) generateKey(b *bytebufferpool.ByteBuffer, domain string, descriptor *pb.RateLimitDescriptor) error {

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
