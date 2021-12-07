package gubernator

// Threadsafe worker pool for handling concurrent GetRateLimit requests.
// Ensures requests are synchronized to avoid caching conflicts.
// Handle concurrent requests by sharding cache key space across multiple
// workers.
// Uses hash ring design pattern to distribute requests to an assigned worker.
// No mutex locking necessary because each worker has its own data space and
// processes requests sequentially.

import (
	"context"
	"fmt"
	"io"
	"sort"

	"github.com/OneOfOne/xxhash"
	"github.com/mailgun/gubernator/v2/tracing"
	"github.com/opentracing/opentracing-go/ext"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type checkHandlerPool struct {
	// List of channels.  Must be sorted by hash value.
	reqChans []checkRequestChannel

	done chan struct{}
}

type checkRequestChannel struct {
	hash uint64
	ch   *chan *request
}

var _ io.Closer = &checkHandlerPool{}

func newCheckHandlerPool(conf Config, concurrency int) *checkHandlerPool {
	const HandlerChannelSize = 1000

	chp := &checkHandlerPool{
		done: make(chan struct{}),
	}

	for i := 0; i < concurrency; i++ {
		ch := make(chan *request, HandlerChannelSize)
		go chp.worker(conf, ch)
		chp.addChannel(&ch)
	}

	return chp
}

func (chp *checkHandlerPool) Close() error {
	close(chp.done)
	return nil
}

// Pool worker for processing GetRateLimit requests.
// Each worker maintains its own cache.
// A hash ring will distribute requests to an assigned worker by key.
func (chp *checkHandlerPool) worker(conf Config, ch chan *request) {
	localCache := conf.Cache.New()

	for {
		select {
		case handlerReq, ok := <-ch:
			if !ok {
				// Channel closed.
				// Unexpected, but should be handled.
				logrus.Error("checkHandlerPool worker stopped because channel closed")
				return
			}

			chp.handleRequest(&conf, localCache, handlerReq)

		case <-chp.done:
			// Clean up.
			return
		}
	}
}

// Handle request received by worker.
func (chp *checkHandlerPool) handleRequest(conf *Config, cache Cache, handlerRequest *request) {
	span, ctx := tracing.StartSpan(handlerRequest.ctx)
	defer span.Finish()

	var rlResponse *RateLimitResp
	var err error

	switch handlerRequest.request.Algorithm {
	case Algorithm_TOKEN_BUCKET:
		rlResponse, err = tokenBucket(ctx, conf.Store, cache, handlerRequest.request)
		if err != nil {
			msg := "Error in tokenBucket"
			countCheckError(err, msg)
			err = errors.Wrap(err, msg)
			ext.LogError(span, err)
		}

	case Algorithm_LEAKY_BUCKET:
		rlResponse, err = leakyBucket(ctx, conf.Store, cache, handlerRequest.request)
		if err != nil {
			msg := "Error in leakyBucket"
			countCheckError(err, msg)
			err = errors.Wrap(err, msg)
			ext.LogError(span, err)
		}

	default:
		err = errors.Errorf("Invalid rate limit algorithm '%d'", handlerRequest.request.Algorithm)
		ext.LogError(span, err)
		checkErrorCounter.WithLabelValues("Invalid algorithm").Add(1)
	}

	handlerResponse := &response{
		rl:  rlResponse,
		err: err,
	}

	select {
	case handlerRequest.resp <- handlerResponse:
		// Success.
	case <-ctx.Done():
		// Silently exit on context cancel.  Caller must handle error.
	}
}

// Send a GetRateLimit request to worker pool.
func (chp *checkHandlerPool) Handle(ctx context.Context, rlRequest *RateLimitReq) (*RateLimitResp, error) {
	span, ctx := tracing.StartSpan(ctx)
	defer span.Finish()

	// Delegate request to assigned channel based on request key.
	ch := chp.getChannel(rlRequest.UniqueKey)
	handlerRequest := &request{
		resp:    make(chan *response, 1),
		request: rlRequest,
		ctx:     ctx,
	}

	// Send request.
	select {
	case *ch <- handlerRequest:
		// Successfully sent request.
	case <-ctx.Done():
		checkErrorCounter.WithLabelValues("Timeout").Add(1)
		return nil, errors.Wrap(ctx.Err(), "Error sending request to checkHandlerPool")
	}

	// Wait for response.
	select {
	case handlerResponse := <-handlerRequest.resp:
		// Successfully read response.
		return handlerResponse.rl, handlerResponse.err
	case <-ctx.Done():
		checkErrorCounter.WithLabelValues("Timeout").Add(1)
		return nil, errors.Wrap(ctx.Err(), "Error reading response from checkHandlerPool")
	}
}

// Add a request channel to the worker pool.
func (chp *checkHandlerPool) addChannel(ch *chan *request) {
	// Generate a hash based off the channel pointer.
	// This hash value is the beginning range of a hash ring node.
	key := fmt.Sprintf("%x", ch)
	hash := xxhash.ChecksumString64S(key, 0)
	chp.reqChans = append(chp.reqChans, checkRequestChannel{
		hash: hash,
		ch:   ch,
	})

	// Ensure keys array is sorted by hash value.
	sort.Slice(chp.reqChans, func(a, b int) bool {
		return chp.reqChans[a].hash < chp.reqChans[b].hash
	})
}

// Returns the request channel associated with the key.
// Hash the key, then lookup hash ring to find the channel.
func (chp *checkHandlerPool) getChannel(key string) *chan *request {
	hash := xxhash.ChecksumString64S(key, 0)

	// Binary search for appropriate channel.
	idx := sort.Search(len(chp.reqChans), func(i int) bool {
		return chp.reqChans[i].hash >= hash
	})

	// Means we have cycled back to the first.
	if idx == len(chp.reqChans) {
		idx = 0
	}

	return chp.reqChans[idx].ch
}
