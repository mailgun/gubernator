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

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/OneOfOne/xxhash"
	"github.com/mailgun/gubernator/v2/tracing"
	"github.com/opentracing/opentracing-go/ext"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type gubernatorPool struct {
	workers []*poolWorker

	// Workers in the hash ring.  Must be sorted by hash value.
	hashRing []poolHashRingNode

	conf *Config
	done chan struct{}
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

// Reference to a poolWorker in the hash ring.
type poolHashRingNode struct {
	hash   uint64
	worker *poolWorker
}

type poolStoreRequest struct {
	ctx      context.Context
	response chan poolStoreResponse
	out      chan<- CacheItem
}

type poolStoreResponse struct{}

type poolLoadRequest struct {
	ctx      context.Context
	response chan poolLoadResponse
	in       <-chan CacheItem
}

type poolLoadResponse struct{}

type poolAddCacheItemRequest struct {
	ctx      context.Context
	response chan poolAddCacheItemResponse
	item     CacheItem
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
	item CacheItem
	ok   bool
}

var _ io.Closer = &gubernatorPool{}
var poolWorkerCounter uint64

func newGubernatorPool(conf *Config, concurrency int) *gubernatorPool {
	chp := &gubernatorPool{
		conf: conf,
		done: make(chan struct{}),
	}

	for i := 0; i < concurrency; i++ {
		worker := chp.addWorker()
		go chp.worker(worker)
	}

	return chp
}

func (chp *gubernatorPool) Close() error {
	close(chp.done)
	return nil
}

// Add a request channel to the worker pool.
func (chp *gubernatorPool) addWorker() *poolWorker {
	const commandChannelSize = 10000

	worker := &poolWorker{
		cache:               chp.conf.CacheFactory(),
		getRateLimitRequest: make(chan *request, commandChannelSize),
		storeRequest:        make(chan poolStoreRequest, commandChannelSize),
		loadRequest:         make(chan poolLoadRequest, commandChannelSize),
		addCacheItemRequest: make(chan poolAddCacheItemRequest, commandChannelSize),
		getCacheItemRequest: make(chan poolGetCacheItemRequest, commandChannelSize),
	}
	workerNumber := atomic.AddUint64(&poolWorkerCounter, 1)
	worker.name = strconv.FormatUint(workerNumber, 10)
	chp.workers = append(chp.workers, worker)

	// Create redundant poolWorker references in the hash ring to improve even
	// distribution of workload.
	for i := 0; i < chp.conf.PoolWorkerHashRingRedundancy; i++ {
		// Generate an arbitrary hash based off the worker pointer.
		// This hash value is the beginning range of a hash ring node.
		key := fmt.Sprintf("%p-%x", worker, i)
		node := poolHashRingNode{
			hash:   xxhash.ChecksumString64S(key, 0),
			worker: worker,
		}

		chp.hashRing = append(chp.hashRing, node)
	}

	// Sort hashRing array by hash value.
	sort.Slice(chp.hashRing, func(a, b int) bool {
		return chp.hashRing[a].hash < chp.hashRing[b].hash
	})

	return worker
}

// Returns the request channel associated with the key.
// Hash the key, then lookup hash ring to find the channel.
func (chp *gubernatorPool) getWorker(key string) *poolWorker {
	hash := xxhash.ChecksumString64S(key, 0)

	// Binary search for appropriate channel.
	idx := sort.Search(len(chp.hashRing), func(i int) bool {
		return chp.hashRing[i].hash >= hash
	})

	// Means we have cycled back to the first.
	if idx >= len(chp.hashRing) {
		idx = 0
	}

	return chp.hashRing[idx].worker
}

// Pool worker for processing Gubernator requests.
// Each worker maintains its own state.
// A hash ring will distribute requests to an assigned worker by key.
// See: getWorker()
func (chp *gubernatorPool) worker(worker *poolWorker) {
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
func (chp *gubernatorPool) GetRateLimit(ctx context.Context, rlRequest *RateLimitReq) (*RateLimitResp, error) {
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

	poolWorkerQueueLength.WithLabelValues(worker.name).Observe(float64(len(worker.getRateLimitRequest)))

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
func (chp *gubernatorPool) handleGetRateLimit(handlerRequest *request, cache Cache) {
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
func (chp *gubernatorPool) Load(ctx context.Context) error {
	span, ctx := tracing.StartSpan(ctx)
	defer span.Finish()

	ch, err := chp.conf.Loader.Load()
	if err != nil {
		return errors.Wrap(err, "Error in loader.Load")
	}

	type loadChannel struct {
		ch       chan CacheItem
		worker   *poolWorker
		respChan chan poolLoadResponse
	}

	// Map request channel hash to load channel.
	loadChMap := map[*poolWorker]loadChannel{}

	// Send each item to assigned channel's cache.
mainloop:
	for {
		var item CacheItem
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
				ch:       make(chan CacheItem),
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

func (chp *gubernatorPool) handleLoad(request poolLoadRequest, cache Cache) {
	span, ctx := tracing.StartSpan(request.ctx)
	defer span.Finish()

mainloop:
	for {
		var item CacheItem
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
func (chp *gubernatorPool) Store(ctx context.Context) error {
	span, ctx := tracing.StartSpan(ctx)
	defer span.Finish()

	var wg sync.WaitGroup
	out := make(chan CacheItem, 500)

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

func (chp *gubernatorPool) handleStore(request poolStoreRequest, cache Cache) {
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
func (chp *gubernatorPool) AddCacheItem(ctx context.Context, key string, item CacheItem) error {
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

func (chp *gubernatorPool) handleAddCacheItem(request poolAddCacheItemRequest, cache Cache) {
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
func (chp *gubernatorPool) GetCacheItem(ctx context.Context, key string) (CacheItem, bool, error) {
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
		select {
		case resp := <-respChan:
			// Successfully received response.
			return resp.item, resp.ok, nil

		case <-ctx.Done():
			// Context canceled.
			ext.LogError(span, ctx.Err())
			return CacheItem{}, false, ctx.Err()
		}

	case <-ctx.Done():
		// Context canceled.
		ext.LogError(span, ctx.Err())
		return CacheItem{}, false, ctx.Err()
	}
}

func (chp *gubernatorPool) handleGetCacheItem(request poolGetCacheItemRequest, cache Cache) {
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
