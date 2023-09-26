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
	"fmt"
	"io"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/OneOfOne/xxhash"
	"github.com/mailgun/holster/v4/errors"
	"github.com/mailgun/holster/v4/setter"
	"github.com/mailgun/holster/v4/tracing"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/trace"
)

type WorkerPool interface {
	GetRateLimit(ctx context.Context, rlRequest *RateLimitReq) (*RateLimitResp, error)
	GetCacheItem(ctx context.Context, key string) (item *CacheItem, found bool, err error)
	AddCacheItem(ctx context.Context, key string, item *CacheItem) error
	Store(ctx context.Context) error
	Load(ctx context.Context) error
	Close() error
	Done() chan struct{}
}

type guberWorkerPool struct {
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

var _ io.Closer = &guberWorkerPool{}
var _ workerHasher = &hasher{}

var workerCounter int64

func NewWorkerPool(conf *Config) WorkerPool {
	setter.SetDefault(&conf.CacheSize, 50_000)

	// Compute hashRingStep as interval between workers' 63-bit hash ranges.
	// 64th bit is used here as a max value that is just out of range of 63-bit space to calculate the step.
	// chp := &guberWorkerPool{
	// 	workers:         make([]*Worker, conf.Workers),
	// 	workerCacheSize: conf.CacheSize / conf.Workers,
	// 	hasher:          newHasher(),
	// 	hashRingStep:    uint64(1<<63) / uint64(conf.Workers),
	// 	conf:            conf,
	// 	done:            make(chan struct{}),
	// }

	// // Create workers.
	// for i := 0; i < conf.Workers; i++ {
	// 	chp.workers[i] = chp.newWorker()
	// 	go chp.dispatch(chp.workers[i])
	// }

	// DEBUG: Test with dummy worker pool.
	// Check if dummyWorkerPool runs faster or with less resources (goroutines) than guberWorkerPool.
	const commandChannelSize = 10000
	worker := &Worker{
		name:                "Dummy worker",
		conf:                conf,
		cache:               conf.CacheFactory(conf.CacheSize),
		getRateLimitRequest: make(chan *request, commandChannelSize),
		storeRequest:        make(chan workerStoreRequest, commandChannelSize),
		loadRequest:         make(chan workerLoadRequest, commandChannelSize),
		addCacheItemRequest: make(chan workerAddCacheItemRequest, commandChannelSize),
		getCacheItemRequest: make(chan workerGetCacheItemRequest, commandChannelSize),
	}
	p := &dummyWorkerPool{
		conf:   conf,
		done:   make(chan struct{}),
		worker: worker,
	}
	go worker.dispatch(p)
	// END DEBUG

	return p
}

func newHasher() *hasher {
	return &hasher{}
}

func (ph *hasher) ComputeHash63(input string) uint64 {
	return xxhash.ChecksumString64S(input, 0) >> 1
}

func (p *guberWorkerPool) Close() error {
	close(p.done)
	return nil
}

func (p *guberWorkerPool) Done() chan struct{} {
	return p.done
}

// Create a new pool worker instance.
func (p *guberWorkerPool) newWorker() *Worker {
	const commandChannelSize = 10000

	worker := &Worker{
		cache:               p.conf.CacheFactory(p.workerCacheSize),
		conf:                p.conf,
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
func (p *guberWorkerPool) getWorker(key string) *Worker {
	hash := p.hasher.ComputeHash63(key)
	idx := hash / p.hashRingStep
	return p.workers[idx]
}

// Dispatch pool worker requests from command channels.
func (worker *Worker) dispatch(p WorkerPool) {
	for {
		// Dispatch requests from each channel.
		select {
		case req, ok := <-worker.getRateLimitRequest:
			if !ok {
				// Channel closed.  Unexpected, but should be handled.
				logrus.Error("workerPool worker stopped because channel closed")
				return
			}

			worker.handleGetRateLimit(req, worker.cache)

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

		case <-p.Done():
			// Clean up.
			return
		}
	}
}

// GetRateLimit sends a GetRateLimit request to worker pool.
func (p *guberWorkerPool) GetRateLimit(ctx context.Context, rlRequest *RateLimitReq) (retval *RateLimitResp, reterr error) {
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
func (worker *Worker) handleGetRateLimit(handlerRequest *request, cache Cache) {
	ctx := tracing.StartNamedScopeDebug(handlerRequest.ctx, "WorkerPool.handleGetRateLimit")
	defer tracing.EndScope(ctx, nil)

	var rlResponse *RateLimitResp
	var err error

	switch handlerRequest.request.Algorithm {
	case Algorithm_TOKEN_BUCKET:
		rlResponse, err = tokenBucket(ctx, worker.conf.Store, cache, handlerRequest.request)
		if err != nil {
			msg := "Error in tokenBucket"
			countError(err, msg)
			err = errors.Wrap(err, msg)
			trace.SpanFromContext(ctx).RecordError(err)
		}

	case Algorithm_LEAKY_BUCKET:
		rlResponse, err = leakyBucket(ctx, worker.conf.Store, cache, handlerRequest.request)
		if err != nil {
			msg := "Error in leakyBucket"
			countError(err, msg)
			err = errors.Wrap(err, msg)
			trace.SpanFromContext(ctx).RecordError(err)
		}

	default:
		err = errors.Errorf("Invalid rate limit algorithm '%d'", handlerRequest.request.Algorithm)
		trace.SpanFromContext(ctx).RecordError(err)
		metricCheckErrorCounter.WithLabelValues("Invalid algorithm").Add(1)
	}

	handlerResponse := &response{
		rl:  rlResponse,
		err: err,
	}

	select {
	case handlerRequest.resp <- handlerResponse:
		// Success.

	case <-ctx.Done():
		// Context canceled.
		trace.SpanFromContext(ctx).RecordError(err)
	}
}

// Load atomically loads cache from persistent storage.
// Read from persistent storage.  Load into each appropriate worker's cache.
// Workers are locked during this load operation to prevent race conditions.
func (p *guberWorkerPool) Load(ctx context.Context) (err error) {
	ctx = tracing.StartNamedScope(ctx, "WorkerPool.Load")
	defer func() { tracing.EndScope(ctx, err) }()

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
	ctx := tracing.StartNamedScopeDebug(request.ctx, "WorkerPool.handleLoad")
	defer tracing.EndScope(ctx, nil)

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

		case <-ctx.Done():
			// Context canceled.
			return
		}

		cache.Add(item)
	}

	response := workerLoadResponse{}

	select {
	case request.response <- response:
		// Successfully sent response.

	case <-ctx.Done():
		// Context canceled.
		trace.SpanFromContext(ctx).RecordError(ctx.Err())
	}
}

// Store atomically stores cache to persistent storage.
// Save all workers' caches to persistent storage.
// Workers are locked during this store operation to prevent race conditions.
func (p *guberWorkerPool) Store(ctx context.Context) (err error) {
	ctx = tracing.StartNamedScopeDebug(ctx, "WorkerPool.Store")
	defer func() { tracing.EndScope(ctx, err) }()

	var wg sync.WaitGroup
	out := make(chan *CacheItem, 500)

	// Iterate each worker's cache to `out` channel.
	for _, worker := range p.workers {
		wg.Add(1)

		go func(ctx context.Context, worker *Worker) {
			ctx = tracing.StartNamedScope(ctx, fmt.Sprintf("%p", worker))
			defer tracing.EndScope(ctx, nil)
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
	ctx := tracing.StartNamedScopeDebug(request.ctx, "WorkerPool.handleStore")
	defer tracing.EndScope(ctx, nil)

	for item := range cache.Each() {
		select {
		case request.out <- item:
			// Successfully sent item.

		case <-ctx.Done():
			// Context canceled.
			trace.SpanFromContext(ctx).RecordError(ctx.Err())
			return
		}
	}

	response := workerStoreResponse{}

	select {
	case request.response <- response:
		// Successfully sent response.

	case <-ctx.Done():
		// Context canceled.
		trace.SpanFromContext(ctx).RecordError(ctx.Err())
	}
}

// AddCacheItem adds an item to the worker's cache.
func (p *guberWorkerPool) AddCacheItem(ctx context.Context, key string, item *CacheItem) (err error) {
	ctx = tracing.StartNamedScopeDebug(ctx, "WorkerPool.AddCacheItem")
	tracing.EndScope(ctx, err)

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
	ctx := tracing.StartNamedScopeDebug(request.ctx, "WorkerPool.handleAddCacheItem")
	defer tracing.EndScope(ctx, nil)

	exists := cache.Add(request.item)
	response := workerAddCacheItemResponse{exists}

	select {
	case request.response <- response:
		// Successfully sent response.

	case <-ctx.Done():
		// Context canceled.
		trace.SpanFromContext(ctx).RecordError(ctx.Err())
	}
}

// GetCacheItem gets item from worker's cache.
func (p *guberWorkerPool) GetCacheItem(ctx context.Context, key string) (item *CacheItem, found bool, err error) {
	ctx = tracing.StartNamedScopeDebug(ctx, "WorkerPool.GetCacheItem")
	tracing.EndScope(ctx, err)

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
	ctx := tracing.StartNamedScopeDebug(request.ctx, "WorkerPool.handleGetCacheItem")
	defer tracing.EndScope(ctx, nil)

	item, ok := cache.GetItem(request.key)
	response := workerGetCacheItemResponse{item, ok}

	select {
	case request.response <- response:
		// Successfully sent response.

	case <-ctx.Done():
		// Context canceled.
		trace.SpanFromContext(ctx).RecordError(ctx.Err())
	}
}
