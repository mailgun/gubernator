package gubernator

import (
	"context"
	"github.com/mailgun/holster"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"sync"
	"time"

	"github.com/mailgun/gubernator/golang/pb"
)

type PeerPicker interface {
	GetPeer(host string) *PeerClient
	Peers() []*PeerClient
	Get(string) (*PeerClient, error)
	New() PeerPicker
	Add(*PeerClient)
	Size() int
}

type PeerClient struct {
	client     pb.PeersServiceClient
	conn       *grpc.ClientConn
	host       string
	isOwner    bool // true if this peer refers to this server instance
	mutex      sync.Mutex
	queue      chan *request
	timeout    time.Duration
	batchWait  time.Duration
	batchLimit int
}

type response struct {
	rateLimitResp *pb.RateLimitResponse
	err           error
}

type request struct {
	rateLimitReq *pb.RateLimitRequest
	resp         chan *response
}

func NewPeerClient(host string) (*PeerClient, error) {
	c := &PeerClient{
		queue: make(chan *request, 1000),
		host:  host,
	}

	// TODO: Allow the user to configure this (Move this to the config module)
	// How long to wait for the peer server to respond
	holster.SetDefault(&c.timeout, time.Millisecond*500)
	// The limit of batched requests in a single peer request
	// TODO: Error if this is higher than the max BatchSize
	holster.SetDefault(&c.batchLimit, maxBatchSize)
	// How long we should wait before sending a batch
	holster.SetDefault(&c.batchWait, time.Microsecond*500)

	if err := c.dialPeer(); err != nil {
		return nil, err
	}

	go c.run()

	return c, nil
}

// GetPeerRateLimits will batch requests that occur within
func (c *PeerClient) GetPeerRateLimits(ctx context.Context, r *pb.RateLimitRequest) (*pb.RateLimitResponse, error) {
	req := request{rateLimitReq: r, resp: make(chan *response, 1)}

	// Enqueue the request to be sent
	c.queue <- &req

	// Wait for a response or context cancel
	select {
	case resp := <-req.resp:
		if resp.err != nil {
			return nil, resp.err
		}
		return resp.rateLimitResp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// dialPeer dials a peer and initializes the GRPC client
func (c *PeerClient) dialPeer() error {
	var err error
	c.conn, err = grpc.Dial(c.host, grpc.WithInsecure())
	if err != nil {
		return errors.Wrapf(err, "failed to dial peer %s", c.host)
	}

	c.client = pb.NewPeersServiceClient(c.conn)
	return nil
}

// run waits for requests to be queued, when either c.batchWait time
// has elapsed or the queue reaches c.batchLimit. Send what is in the queue.
func (c *PeerClient) run() {
	var interval = NewInterval(c.batchWait)
	var queue []*request

	for {
		select {
		case r := <-c.queue:
			queue = append(queue, r)

			// Send the queue if we reached our batch limit
			if len(queue) > c.batchLimit {
				c.sendQueue(queue)
				queue = nil
				continue
			}

			// If this is our first queued item since last send
			// queue the next interval
			if len(queue) == 1 {
				interval.Next()
			}

		case <-interval.C:
			if len(queue) != 0 {
				c.sendQueue(queue)
				queue = nil
			}
		}
	}
}

// sendQueue sends the queue provided and returns the responses to
// waiting go routines
func (c *PeerClient) sendQueue(queue []*request) {
	var peerReq pb.PeerRateLimitRequest
	for _, r := range queue {
		peerReq.RateLimits = append(peerReq.RateLimits, r.rateLimitReq)
	}

	//fmt.Printf("Send(%d)\n", len(queue))
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	resp, err := c.client.GetPeerRateLimits(ctx, &peerReq)
	cancel()

	// An error here indicates the entire request failed
	if err != nil {
		for _, r := range queue {
			r.resp <- &response{err: err}
		}
		return
	}

	// Unlikely, but this avoids a panic if something wonky happens
	if len(resp.RateLimits) != len(queue) {
		err = errors.New("server responded with incorrect rate limit list size")
		for _, r := range queue {
			r.resp <- &response{err: err}
		}
		return
	}

	// Provide responses to channels waiting in the queue
	for i, r := range queue {
		r.resp <- &response{rateLimitResp: resp.RateLimits[i]}
	}
}
