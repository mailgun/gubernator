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

// Threadsafe worker pool for handling concurrent Gubernator requests.
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
	"github.com/mailgun/gubernator/v2/tracing"
	"github.com/mailgun/holster/v4/setter"
	"github.com/opentracing/opentracing-go/ext"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type GubernatorPool struct {
	workers         []*poolWorker
	workerCacheSize int
	hasher          ipoolHasher
	hashRingStep    uint64
	conf            *Config
	done            chan struct{}
}

type poolWorker struct {
	name                string
	conf                *Config
	cache               Cache
	getRateLimitRequest chan *request
	storeRequest        chan poolStoreRequest
	loadRequest         chan poolLoadRequest
	addCacheItemRequest chan poolAddCacheItemRequest
	getCacheItemRequest chan poolGetCacheItemRequest
}

type ipoolHasher interface {
	// Return a 63-bit hash derived from input.
	ComputeHash63(input string) uint64
}

// Standard implementation of ipoolHasher.
type poolHasher struct {
}

// Method request/response structs.
type poolStoreRequest struct {
	ctx      context.Context
	response chan poolStoreResponse
	out      chan<- *CacheItem
}

type poolStoreResponse struct{}

type poolLoadRequest struct {
	ctx      context.Context
	response chan poolLoadResponse
	in       <-chan *CacheItem
}

type poolLoadResponse struct{}

type poolAddCacheItemRequest struct {
	ctx      context.Context
	response chan poolAddCacheItemResponse
	item     *CacheItem
}

type poolAddCacheItemResponse struct {
	exists bool
}

type poolGetCacheItemRequest struct {
	ctx      context.Context
	response chan poolGetCacheItemResponse
	key      string
}

type poolGetCacheItemResponse struct {
	item *CacheItem
	ok   bool
}

var _ io.Closer = &GubernatorPool{}
var _ ipoolHasher = &poolHasher{}

var poolWorkerCounter int64

// Map ratelimit key->name->counter.
var overlimitCounter = map[string]map[string]int{}
var overLimitCounterMutex sync.Mutex

func NewGubernatorPool(conf *Config, concurrency int, cacheSize int) *GubernatorPool {
	setter.SetDefault(&cacheSize, 50_000)

	// Compute hashRingStep as interval between workers' 63-bit hash ranges.
	// 64th bit is used here as a max value that is just out of range of 63-bit space to calculate the step.
	chp := &GubernatorPool{
		workers:         make([]*poolWorker, concurrency),
		workerCacheSize: cacheSize / concurrency,
		hasher:          newPoolHasher(),
		hashRingStep:    uint64(1 << 63) / uint64(concurrency),
		conf:            conf,
		done:            make(chan struct{}),
	}

	// Create workers.
	for i := 0; i < concurrency; i++ {
		chp.workers[i] = chp.newWorker()
		go chp.worker(chp.workers[i])
	}

	return chp
}

func newPoolHasher() *poolHasher {
	return &poolHasher{}
}

func (ph *poolHasher) ComputeHash63(input string) uint64 {
	return xxhash.ChecksumString64S(input, 0) >> 1
}

func (chp *GubernatorPool) Close() error {
	close(chp.done)
	return nil
}

// Create a new pool worker instance.
func (chp *GubernatorPool) newWorker() *poolWorker {
	const commandChannelSize = 10000

	worker := &poolWorker{
		cache:               chp.conf.CacheFactory(chp.workerCacheSize),
		getRateLimitRequest: make(chan *request, commandChannelSize),
		storeRequest:        make(chan poolStoreRequest, commandChannelSize),
		loadRequest:         make(chan poolLoadRequest, commandChannelSize),
		addCacheItemRequest: make(chan poolAddCacheItemRequest, commandChannelSize),
		getCacheItemRequest: make(chan poolGetCacheItemRequest, commandChannelSize),
	}
	workerNumber := atomic.AddInt64(&poolWorkerCounter, 1) - 1
	worker.name = strconv.FormatInt(workerNumber, 10)
	return worker
}

// Returns the request channel associated with the key.
// Hash the key, then lookup hash ring to find the worker.
func (chp *GubernatorPool) getWorker(key string) *poolWorker {
	hash := chp.hasher.ComputeHash63(key)
	idx := hash / chp.hashRingStep
	return chp.workers[idx]
}

// Pool worker for processing Gubernator requests.
// Each worker maintains its own state.
// A hash ring will distribute requests to an assigned worker by key.
// See: getWorker()
func (chp *GubernatorPool) worker(worker *poolWorker) {
	for {
		// Dispatch requests from each channel.
		select {
		case req, ok := <-worker.getRateLimitRequest:
			if !ok {
				// Channel closed.  Unexpected, but should be handled.
				logrus.Error("checkHandlerPool worker stopped because channel closed")
				return
			}

			chp.handleGetRateLimit(req, worker.cache)

		case req, ok := <-worker.storeRequest:
			if !ok {
				// Channel closed.  Unexpected, but should be handled.
				logrus.Error("checkHandlerPool worker stopped because channel closed")
				return
			}

			chp.handleStore(req, worker.cache)

		case req, ok := <-worker.loadRequest:
			if !ok {
				// Channel closed.  Unexpected, but should be handled.
				logrus.Error("checkHandlerPool worker stopped because channel closed")
				return
			}

			chp.handleLoad(req, worker.cache)

		case req, ok := <-worker.addCacheItemRequest:
			if !ok {
				// Channel closed.  Unexpected, but should be handled.
				logrus.Error("checkHandlerPool worker stopped because channel closed")
				return
			}

			chp.handleAddCacheItem(req, worker.cache)

		case req, ok := <-worker.getCacheItemRequest:
			if !ok {
				// Channel closed.  Unexpected, but should be handled.
				logrus.Error("checkHandlerPool worker stopped because channel closed")
				return
			}

			chp.handleGetCacheItem(req, worker.cache)

		case <-chp.done:
			// Clean up.
			return
		}
	}
}

// Send a GetRateLimit request to worker pool.
func (chp *GubernatorPool) GetRateLimit(ctx context.Context, rlRequest *RateLimitReq) (*RateLimitResp, error) {
	span, ctx := tracing.StartSpan(ctx)
	defer span.Finish()

	// Delegate request to assigned channel based on request key.
	worker := chp.getWorker(rlRequest.UniqueKey)
	handlerRequest := &request{
		ctx:     ctx,
		resp:    make(chan *response, 1),
		request: rlRequest,
	}

	// Send request.
	tracing.LogInfo(span, "Sending request...", "channelLength", len(worker.getRateLimitRequest))
	select {
	case worker.getRateLimitRequest <- handlerRequest:
		// Successfully sent request.
	case <-ctx.Done():
		ext.LogError(span, ctx.Err())
		return nil, ctx.Err()
	}

	poolWorkerQueueLength.WithLabelValues("GetRateLimit", worker.name).Observe(float64(len(worker.getRateLimitRequest)))

	// Wait for response.
	tracing.LogInfo(span, "Waiting for response...")
	select {
	case handlerResponse := <-handlerRequest.resp:
		// Successfully read response.
		return handlerResponse.rl, handlerResponse.err
	case <-ctx.Done():
		ext.LogError(span, ctx.Err())
		return nil, ctx.Err()
	}
}

// Handle request received by worker.
func (chp *GubernatorPool) handleGetRateLimit(handlerRequest *request, cache Cache) {
	span, ctx := tracing.StartSpan(handlerRequest.ctx)
	defer span.Finish()

	var rlResponse *RateLimitResp
	var err error

	switch handlerRequest.request.Algorithm {
	case Algorithm_TOKEN_BUCKET:
		rlResponse, err = tokenBucket(ctx, chp.conf.Store, cache, handlerRequest.request)
		if err != nil {
			msg := "Error in tokenBucket"
			countError(err, msg)
			err = errors.Wrap(err, msg)
			ext.LogError(span, err)
		}

	case Algorithm_LEAKY_BUCKET:
		rlResponse, err = leakyBucket(ctx, chp.conf.Store, cache, handlerRequest.request)
		if err != nil {
			msg := "Error in leakyBucket"
			countError(err, msg)
			err = errors.Wrap(err, msg)
			ext.LogError(span, err)
		}

	default:
		err = errors.Errorf("Invalid rate limit algorithm '%d'", handlerRequest.request.Algorithm)
		ext.LogError(span, err)
		checkErrorCounter.WithLabelValues("Invalid algorithm").Add(1)
	}

	if err == nil {
		if rlResponse.Status == Status_OVER_LIMIT {
			name := handlerRequest.request.Name
			key := handlerRequest.request.UniqueKey
			occurrences := incrementOverLimitCounter(name, key)
			requestId, _ := ctx.Value(requestIdKey{}).(string)

			logrus.WithFields(logrus.Fields{
				"name": name,
				"key": key,
				"duration": handlerRequest.request.Duration,
				"limit": handlerRequest.request.Limit,
				"occurrences": occurrences,
				"requestId": requestId,
			}).Warn("Rate over limit")
		}
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
		ext.LogError(span, ctx.Err())
	}
}

// Atomically load cache from persistent storage.
// Read from persistent storage.  Load into each appropriate worker's cache.
// Workers are locked during this load operation to prevent race conditions.
func (chp *GubernatorPool) Load(ctx context.Context) error {
	span, ctx := tracing.StartSpan(ctx)
	defer span.Finish()

	ch, err := chp.conf.Loader.Load()
	if err != nil {
		return errors.Wrap(err, "Error in loader.Load")
	}

	type loadChannel struct {
		ch       chan *CacheItem
		worker   *poolWorker
		respChan chan poolLoadResponse
	}

	// Map request channel hash to load channel.
	loadChMap := map[*poolWorker]loadChannel{}

	// Send each item to assigned channel's cache.
mainloop:
	for {
		var item *CacheItem
		var ok bool

		select {
		case item, ok = <-ch:
			if !ok {
				break mainloop
			}
			// Successfully received item.

		case <-ctx.Done():
			// Context canceled.
			return ctx.Err()
		}

		worker := chp.getWorker(item.Key)

		// Initiate a load channel with each worker.
		loadCh, exist := loadChMap[worker]
		if !exist {
			loadCh = loadChannel{
				ch:       make(chan *CacheItem),
				worker:   worker,
				respChan: make(chan poolLoadResponse),
			}
			loadChMap[worker] = loadCh

			// Tie up the worker while loading.
			worker.loadRequest <- poolLoadRequest{
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

func (chp *GubernatorPool) handleLoad(request poolLoadRequest, cache Cache) {
	span, ctx := tracing.StartSpan(request.ctx)
	defer span.Finish()

mainloop:
	for {
		var item *CacheItem
		var ok bool

		select {
		case item, ok = <-request.in:
			if !ok {
				break mainloop
			}
			// Successfully received item.

		case <-ctx.Done():
			// Context canceled.
			return
		}

		cache.Add(item)
	}

	response := poolLoadResponse{}

	select {
	case request.response <- response:
		// Successfully sent response.

	case <-ctx.Done():
		// Context canceled.
		ext.LogError(span, ctx.Err())
	}
}

// Atomically store cache to persistent storage.
// Save all workers' caches to persistent storage.
// Workers are locked during this store operation to prevent race conditions.
func (chp *GubernatorPool) Store(ctx context.Context) error {
	span, ctx := tracing.StartSpan(ctx)
	defer span.Finish()

	var wg sync.WaitGroup
	out := make(chan *CacheItem, 500)

	// Iterate each worker's cache to `out` channel.
	for _, worker := range chp.workers {
		wg.Add(1)

		go func(worker *poolWorker) {
			span2, ctx2 := tracing.StartNamedSpan(ctx, fmt.Sprintf("%p", worker))
			defer span2.Finish()
			defer wg.Done()

			respChan := make(chan poolStoreResponse)
			req := poolStoreRequest{
				ctx:      ctx2,
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

				case <-ctx2.Done():
					// Context canceled.
					ext.LogError(span2, ctx2.Err())
					return
				}

			case <-ctx2.Done():
				// Context canceled.
				ext.LogError(span2, ctx2.Err())
				return
			}
		}(worker)
	}

	// When all iterators are done, close `out` channel.
	go func() {
		wg.Wait()
		close(out)
	}()

	if ctx.Err() != nil {
		ext.LogError(span, ctx.Err())
		return ctx.Err()
	}

	return chp.conf.Loader.Save(out)
}

func (chp *GubernatorPool) handleStore(request poolStoreRequest, cache Cache) {
	span, ctx := tracing.StartSpan(request.ctx)
	defer span.Finish()

	for item := range cache.Each() {
		select {
		case request.out <- item:
			// Successfully sent item.

		case <-ctx.Done():
			// Context canceled.
			ext.LogError(span, ctx.Err())
			return
		}
	}

	response := poolStoreResponse{}

	select {
	case request.response <- response:
		// Successfully sent response.

	case <-ctx.Done():
		// Context canceled.
		ext.LogError(span, ctx.Err())
	}
}

// Add to worker's cache.
func (chp *GubernatorPool) AddCacheItem(ctx context.Context, key string, item *CacheItem) error {
	span, ctx := tracing.StartSpan(ctx)
	defer span.Finish()

	respChan := make(chan poolAddCacheItemResponse)
	worker := chp.getWorker(key)
	req := poolAddCacheItemRequest{
		ctx:      ctx,
		response: respChan,
		item:     item,
	}

	select {
	case worker.addCacheItemRequest <- req:
		// Successfully sent request.
		poolWorkerQueueLength.WithLabelValues("AddCacheItem", worker.name).Observe(float64(len(worker.addCacheItemRequest)))

		select {
		case <-respChan:
			// Successfully received response.
			return nil

		case <-ctx.Done():
			// Context canceled.
			ext.LogError(span, ctx.Err())
			return ctx.Err()
		}

	case <-ctx.Done():
		// Context canceled.
		ext.LogError(span, ctx.Err())
		return ctx.Err()
	}
}

func (chp *GubernatorPool) handleAddCacheItem(request poolAddCacheItemRequest, cache Cache) {
	span, ctx := tracing.StartSpan(request.ctx)
	defer span.Finish()

	exists := cache.Add(request.item)
	response := poolAddCacheItemResponse{exists}

	select {
	case request.response <- response:
		// Successfully sent response.

	case <-ctx.Done():
		// Context canceled.
		ext.LogError(span, ctx.Err())
	}
}

// Get item from worker's cache.
func (chp *GubernatorPool) GetCacheItem(ctx context.Context, key string) (*CacheItem, bool, error) {
	span, ctx := tracing.StartSpan(ctx)
	defer span.Finish()

	respChan := make(chan poolGetCacheItemResponse)
	worker := chp.getWorker(key)
	req := poolGetCacheItemRequest{
		ctx:      ctx,
		response: respChan,
		key:      key,
	}

	select {
	case worker.getCacheItemRequest <- req:
		// Successfully sent requst.
		poolWorkerQueueLength.WithLabelValues("GetCacheItem", worker.name).Observe(float64(len(worker.getCacheItemRequest)))

		select {
		case resp := <-respChan:
			// Successfully received response.
			return resp.item, resp.ok, nil

		case <-ctx.Done():
			// Context canceled.
			ext.LogError(span, ctx.Err())
			return nil, false, ctx.Err()
		}

	case <-ctx.Done():
		// Context canceled.
		ext.LogError(span, ctx.Err())
		return nil, false, ctx.Err()
	}
}

func (chp *GubernatorPool) handleGetCacheItem(request poolGetCacheItemRequest, cache Cache) {
	span, ctx := tracing.StartSpan(request.ctx)
	defer span.Finish()

	item, ok := cache.GetItem(request.key)
	response := poolGetCacheItemResponse{item, ok}

	select {
	case request.response <- response:
		// Successfully sent response.

	case <-ctx.Done():
		// Context canceled.
		ext.LogError(span, ctx.Err())
	}
}

func incrementOverLimitCounter(name, key string) int {
	overLimitCounterMutex.Lock()
	defer overLimitCounterMutex.Unlock()
	occurrences := int(1)

	if keyMap, ok := overlimitCounter[key]; ok {
		if value, ok := keyMap[name]; ok {
			occurrences += value
			keyMap[name] = occurrences
		} else {
			keyMap[name] = occurrences
		}
	} else {
		overlimitCounter[key] = map[string]int{
			name: occurrences,
		}
	}

	return occurrences
}
