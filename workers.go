/*
Copyright 2018-2022 Mailgun Technologies Inc

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package gubernator

// Thread-safe worker pool for handling concurrent Gubernator requests.
// Ensures requests are synchronized to avoid caching conflicts.
// Handle concurrent requests by sharding cache key space across multiple
// workers.
// Uses hash ring design pattern to distribute requests to an assigned worker.
// No mutex locking necessary because each worker has its own data space and
// processes requests sequentially.
//
// Request workflow:
// - A 63-bit hash is generated from an incoming request by its Key/Name
//   values. (Actually 64 bit, but we toss out one bit to properly calculate
//   the next step.)
// - Workers are assigned equal size hash ranges.  The worker is selected by
//   choosing the worker index associated with that linear hash value range.
// - The worker has command channels for each method call.  The request is
//   enqueued to the appropriate channel.
// - The worker pulls the request from the appropriate channel and executes the
//   business logic for that method.  Then, it sends a response back using the
//   requester's provided response channel.

import (
	"context"
	"io"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/OneOfOne/xxhash"
	"github.com/mailgun/holster/v4/errors"
	"github.com/mailgun/holster/v4/setter"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/trace"
)

type WorkerPool struct {
	hasher          workerHasher
	workers         []*Worker
	workerCacheSize int
	hashRingStep    uint64
	conf            *Config
	done            chan struct{}
}

type Worker struct {
	name                string
	conf                *Config
	cache               Cache
	getRateLimitRequest chan *request
	storeRequest        chan workerStoreRequest
	loadRequest         chan workerLoadRequest
	addCacheItemRequest chan workerAddCacheItemRequest
	getCacheItemRequest chan workerGetCacheItemRequest
}

type workerHasher interface {
	// ComputeHash63 returns a 63-bit hash derived from input.
	ComputeHash63(input string) uint64
}

// hasher is the default implementation of workerHasher.
type hasher struct{}

// Method request/response structs.
type workerStoreRequest struct {
	ctx      context.Context
	response chan workerStoreResponse
	out      chan<- *CacheItem
}

type workerStoreResponse struct{}

type workerLoadRequest struct {
	ctx      context.Context
	response chan workerLoadResponse
	in       <-chan *CacheItem
}

type workerLoadResponse struct{}

type workerAddCacheItemRequest struct {
	ctx      context.Context
	response chan workerAddCacheItemResponse
	item     *CacheItem
}

type workerAddCacheItemResponse struct {
	exists bool
}

type workerGetCacheItemRequest struct {
	ctx      context.Context
	response chan workerGetCacheItemResponse
	key      string
}

type workerGetCacheItemResponse struct {
	item *CacheItem
	ok   bool
}

var _ io.Closer = &WorkerPool{}
var _ workerHasher = &hasher{}

var workerCounter int64

func NewWorkerPool(conf *Config) *WorkerPool {
	setter.SetDefault(&conf.CacheSize, 50_000)

	// Compute hashRingStep as interval between workers' 63-bit hash ranges.
	// 64th bit is used here as a max value that is just out of range of 63-bit space to calculate the step.
	chp := &WorkerPool{
		workers:         make([]*Worker, conf.Workers),
		workerCacheSize: conf.CacheSize / conf.Workers,
		hasher:          newHasher(),
		hashRingStep:    uint64(1<<63) / uint64(conf.Workers),
		conf:            conf,
		done:            make(chan struct{}),
	}

	// Create workers.
	logrus.Infof("Starting %d Gubernator workers...", conf.Workers)
	for i := 0; i < conf.Workers; i++ {
		chp.workers[i] = chp.newWorker()
		go chp.dispatch(chp.workers[i])
	}

	return chp
}

func newHasher() *hasher {
	return &hasher{}
}

func (ph *hasher) ComputeHash63(input string) uint64 {
	return xxhash.ChecksumString64S(input, 0) >> 1
}

func (p *WorkerPool) Close() error {
	close(p.done)
	return nil
}

// Create a new pool worker instance.
func (p *WorkerPool) newWorker() *Worker {
	const commandChannelSize = 10000

	worker := &Worker{
		conf:                p.conf,
		cache:               p.conf.CacheFactory(p.workerCacheSize),
		getRateLimitRequest: make(chan *request, commandChannelSize),
		storeRequest:        make(chan workerStoreRequest, commandChannelSize),
		loadRequest:         make(chan workerLoadRequest, commandChannelSize),
		addCacheItemRequest: make(chan workerAddCacheItemRequest, commandChannelSize),
		getCacheItemRequest: make(chan workerGetCacheItemRequest, commandChannelSize),
	}
	workerNumber := atomic.AddInt64(&workerCounter, 1) - 1
	worker.name = strconv.FormatInt(workerNumber, 10)
	return worker
}

// getWorker Returns the request channel associated with the key.
// Hash the key, then lookup hash ring to find the worker.
func (p *WorkerPool) getWorker(key string) *Worker {
	hash := p.hasher.ComputeHash63(key)
	idx := hash / p.hashRingStep
	return p.workers[idx]
}

// Pool worker for processing Gubernator requests.
// Each worker maintains its own state.
// A hash ring will distribute requests to an assigned worker by key.
// See: getWorker()
func (p *WorkerPool) dispatch(worker *Worker) {
	for {
		// Dispatch requests from each channel.
		select {
		case req, ok := <-worker.getRateLimitRequest:
			if !ok {
				// Channel closed.  Unexpected, but should be handled.
				logrus.Error("workerPool worker stopped because channel closed")
				return
			}

			resp := new(response)
			resp.rl, resp.err = worker.handleGetRateLimit(req.ctx, req.request, worker.cache)

			select {
			case req.resp <- resp:
				// Success.

			case <-req.ctx.Done():
				// Context canceled.
				trace.SpanFromContext(req.ctx).RecordError(resp.err)
			}

		case req, ok := <-worker.storeRequest:
			if !ok {
				// Channel closed.  Unexpected, but should be handled.
				logrus.Error("workerPool worker stopped because channel closed")
				return
			}

			worker.handleStore(req, worker.cache)

		case req, ok := <-worker.loadRequest:
			if !ok {
				// Channel closed.  Unexpected, but should be handled.
				logrus.Error("workerPool worker stopped because channel closed")
				return
			}

			worker.handleLoad(req, worker.cache)

		case req, ok := <-worker.addCacheItemRequest:
			if !ok {
				// Channel closed.  Unexpected, but should be handled.
				logrus.Error("workerPool worker stopped because channel closed")
				return
			}

			worker.handleAddCacheItem(req, worker.cache)

		case req, ok := <-worker.getCacheItemRequest:
			if !ok {
				// Channel closed.  Unexpected, but should be handled.
				logrus.Error("workerPool worker stopped because channel closed")
				return
			}

			worker.handleGetCacheItem(req, worker.cache)

		case <-p.done:
			// Clean up.
			return
		}
	}
}

// GetRateLimit sends a GetRateLimit request to worker pool.
func (p *WorkerPool) GetRateLimit(ctx context.Context, rlRequest *RateLimitReq) (retval *RateLimitResp, reterr error) {
	// Delegate request to assigned channel based on request key.
	worker := p.getWorker(rlRequest.UniqueKey)
	handlerRequest := &request{
		ctx:     ctx,
		resp:    make(chan *response, 1),
		request: rlRequest,
	}

	// Send request.
	select {
	case worker.getRateLimitRequest <- handlerRequest:
		// Successfully sent request.
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	metricWorkerQueueLength.WithLabelValues("GetRateLimit", worker.name).Observe(float64(len(worker.getRateLimitRequest)))

	// Wait for response.
	select {
	case handlerResponse := <-handlerRequest.resp:
		// Successfully read response.
		return handlerResponse.rl, handlerResponse.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Handle request received by worker.
func (worker *Worker) handleGetRateLimit(ctx context.Context, req *RateLimitReq, cache Cache) (*RateLimitResp, error) {
	var rlResponse *RateLimitResp
	var err error

	switch req.Algorithm {
	case Algorithm_TOKEN_BUCKET:
		rlResponse, err = tokenBucket(ctx, worker.conf.Store, cache, req)
		if err != nil {
			msg := "Error in tokenBucket"
			countError(err, msg)
			err = errors.Wrap(err, msg)
			trace.SpanFromContext(ctx).RecordError(err)
		}

	case Algorithm_LEAKY_BUCKET:
		rlResponse, err = leakyBucket(ctx, worker.conf.Store, cache, req)
		if err != nil {
			msg := "Error in leakyBucket"
			countError(err, msg)
			err = errors.Wrap(err, msg)
			trace.SpanFromContext(ctx).RecordError(err)
		}

	default:
		err = errors.Errorf("Invalid rate limit algorithm '%d'", req.Algorithm)
		trace.SpanFromContext(ctx).RecordError(err)
		metricCheckErrorCounter.WithLabelValues("Invalid algorithm").Add(1)
	}

	return rlResponse, err
}

// Load atomically loads cache from persistent storage.
// Read from persistent storage.  Load into each appropriate worker's cache.
// Workers are locked during this load operation to prevent race conditions.
func (p *WorkerPool) Load(ctx context.Context) (err error) {
	ch, err := p.conf.Loader.Load()
	if err != nil {
		return errors.Wrap(err, "Error in loader.Load")
	}

	type loadChannel struct {
		ch       chan *CacheItem
		worker   *Worker
		respChan chan workerLoadResponse
	}

	// Map request channel hash to load channel.
	loadChMap := map[*Worker]loadChannel{}

	// Send each item to the assigned channel's cache.
MAIN:
	for {
		var item *CacheItem
		var ok bool

		select {
		case item, ok = <-ch:
			if !ok {
				break MAIN
			}
			// Successfully received item.

		case <-ctx.Done():
			// Context canceled.
			return ctx.Err()
		}

		worker := p.getWorker(item.Key)

		// Initiate a load channel with each worker.
		loadCh, exist := loadChMap[worker]
		if !exist {
			loadCh = loadChannel{
				ch:       make(chan *CacheItem),
				worker:   worker,
				respChan: make(chan workerLoadResponse),
			}
			loadChMap[worker] = loadCh

			// Tie up the worker while loading.
			worker.loadRequest <- workerLoadRequest{
				ctx:      ctx,
				response: loadCh.respChan,
				in:       loadCh.ch,
			}
		}

		// Send item to worker's load channel.
		select {
		case loadCh.ch <- item:
			// Successfully sent item.

		case <-ctx.Done():
			// Context canceled.
			return ctx.Err()
		}
	}

	// Clean up.
	for _, loadCh := range loadChMap {
		close(loadCh.ch)

		// Load response confirms all items have been loaded and the worker
		// resumes normal operation.
		select {
		case <-loadCh.respChan:
			// Successfully received response.

		case <-ctx.Done():
			// Context canceled.
			return ctx.Err()
		}
	}

	return nil
}

func (worker *Worker) handleLoad(request workerLoadRequest, cache Cache) {
MAIN:
	for {
		var item *CacheItem
		var ok bool

		select {
		case item, ok = <-request.in:
			if !ok {
				break MAIN
			}
			// Successfully received item.

		case <-request.ctx.Done():
			// Context canceled.
			return
		}

		cache.Add(item)
	}

	response := workerLoadResponse{}

	select {
	case request.response <- response:
		// Successfully sent response.

	case <-request.ctx.Done():
		// Context canceled.
		trace.SpanFromContext(request.ctx).RecordError(request.ctx.Err())
	}
}

// Store atomically stores cache to persistent storage.
// Save all workers' caches to persistent storage.
// Workers are locked during this store operation to prevent race conditions.
func (p *WorkerPool) Store(ctx context.Context) (err error) {
	var wg sync.WaitGroup
	out := make(chan *CacheItem, 500)

	// Iterate each worker's cache to `out` channel.
	for _, worker := range p.workers {
		wg.Add(1)

		go func(ctx context.Context, worker *Worker) {
			defer wg.Done()

			respChan := make(chan workerStoreResponse)
			req := workerStoreRequest{
				ctx:      ctx,
				response: respChan,
				out:      out,
			}

			select {
			case worker.storeRequest <- req:
				// Successfully sent request.
				select {
				case <-respChan:
					// Successfully received response.
					return

				case <-ctx.Done():
					// Context canceled.
					trace.SpanFromContext(ctx).RecordError(ctx.Err())
					return
				}

			case <-ctx.Done():
				// Context canceled.
				trace.SpanFromContext(ctx).RecordError(ctx.Err())
				return
			}
		}(ctx, worker)
	}

	// When all iterators are done, close `out` channel.
	go func() {
		wg.Wait()
		close(out)
	}()

	if ctx.Err() != nil {
		return ctx.Err()
	}

	if err = p.conf.Loader.Save(out); err != nil {
		return errors.Wrap(err, "while calling p.conf.Loader.Save()")
	}

	return nil
}

func (worker *Worker) handleStore(request workerStoreRequest, cache Cache) {
	for item := range cache.Each() {
		select {
		case request.out <- item:
			// Successfully sent item.

		case <-request.ctx.Done():
			// Context canceled.
			trace.SpanFromContext(request.ctx).RecordError(request.ctx.Err())
			return
		}
	}

	response := workerStoreResponse{}

	select {
	case request.response <- response:
		// Successfully sent response.

	case <-request.ctx.Done():
		// Context canceled.
		trace.SpanFromContext(request.ctx).RecordError(request.ctx.Err())
	}
}

// AddCacheItem adds an item to the worker's cache.
func (p *WorkerPool) AddCacheItem(ctx context.Context, key string, item *CacheItem) (err error) {
	respChan := make(chan workerAddCacheItemResponse)
	worker := p.getWorker(key)
	req := workerAddCacheItemRequest{
		ctx:      ctx,
		response: respChan,
		item:     item,
	}

	select {
	case worker.addCacheItemRequest <- req:
		// Successfully sent request.
		metricWorkerQueueLength.WithLabelValues("AddCacheItem", worker.name).Observe(float64(len(worker.addCacheItemRequest)))

		select {
		case <-respChan:
			// Successfully received response.
			return nil

		case <-ctx.Done():
			// Context canceled.
			return ctx.Err()
		}

	case <-ctx.Done():
		// Context canceled.
		return ctx.Err()
	}
}

func (worker *Worker) handleAddCacheItem(request workerAddCacheItemRequest, cache Cache) {
	exists := cache.Add(request.item)
	response := workerAddCacheItemResponse{exists}

	select {
	case request.response <- response:
		// Successfully sent response.

	case <-request.ctx.Done():
		// Context canceled.
		trace.SpanFromContext(request.ctx).RecordError(request.ctx.Err())
	}
}

// GetCacheItem gets item from worker's cache.
func (p *WorkerPool) GetCacheItem(ctx context.Context, key string) (item *CacheItem, found bool, err error) {
	respChan := make(chan workerGetCacheItemResponse)
	worker := p.getWorker(key)
	req := workerGetCacheItemRequest{
		ctx:      ctx,
		response: respChan,
		key:      key,
	}

	select {
	case worker.getCacheItemRequest <- req:
		// Successfully sent request.
		metricWorkerQueueLength.WithLabelValues("GetCacheItem", worker.name).Observe(float64(len(worker.getCacheItemRequest)))

		select {
		case resp := <-respChan:
			// Successfully received response.
			return resp.item, resp.ok, nil

		case <-ctx.Done():
			// Context canceled.
			return nil, false, ctx.Err()
		}

	case <-ctx.Done():
		// Context canceled.
		return nil, false, ctx.Err()
	}
}

func (worker *Worker) handleGetCacheItem(request workerGetCacheItemRequest, cache Cache) {
	item, ok := cache.GetItem(request.key)
	response := workerGetCacheItemResponse{item, ok}

	select {
	case request.response <- response:
		// Successfully sent response.

	case <-request.ctx.Done():
		// Context canceled.
		trace.SpanFromContext(request.ctx).RecordError(request.ctx.Err())
	}
}
