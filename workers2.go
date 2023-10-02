package gubernator

import (
	"context"
	"io"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/mailgun/holster/v4/setter"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/trace"
)

type WorkerPool2 struct {
	conf                *Config
	cache               Cache
	cacheMu             sync.Mutex
	done                chan struct{}
	getRateLimitRequest chan request
	storeRequest        chan workerStoreRequest
	loadRequest         chan workerLoadRequest
	addCacheItemRequest chan workerAddCacheItemRequest
	getCacheItemRequest chan workerGetCacheItemRequest
	workerCounter       atomic.Int64
}

type Worker2 struct {
	name  string
	conf  *Config
}

var _ io.Closer = &WorkerPool2{}

func NewWorkerPool2(conf *Config) *WorkerPool2 {
	setter.SetDefault(&conf.CacheSize, 50_000)

	// Compute hashRingStep as interval between workers' 63-bit hash ranges.
	// 64th bit is used here as a max value that is just out of range of 63-bit space to calculate the step.
	chp := &WorkerPool2{
		conf:                conf,
		cache:               conf.CacheFactory(conf.CacheSize),
		done:                make(chan struct{}),
		getRateLimitRequest: make(chan request),
		storeRequest:        make(chan workerStoreRequest),
		loadRequest:         make(chan workerLoadRequest),
		addCacheItemRequest: make(chan workerAddCacheItemRequest),
		getCacheItemRequest: make(chan workerGetCacheItemRequest),
	}

	// Create workers.
	conf.Logger.Infof("Starting %d Gubernator workers...", conf.Workers)
	for i := 0; i < conf.Workers; i++ {
		worker := chp.newWorker()
		go worker.dispatch(chp)
	}

	return chp
}

func (p *WorkerPool2) Close() error {
	close(p.done)
	return nil
}

// Create a new pool worker instance.
func (p *WorkerPool2) newWorker() *Worker2 {
	workerNumber := p.workerCounter.Add(1) - 1
	worker := &Worker2{
		name: strconv.FormatInt(workerNumber, 10),
		conf: p.conf,
	}
	return worker
}

// Pool worker for processing Gubernator requests.
// Each worker maintains its own state.
// A hash ring will distribute requests to an assigned worker by key.
// See: getWorker()
func (worker *Worker2) dispatch(p *WorkerPool2) {
	for {
		// Dispatch requests from each channel.
		select {
		case req, ok := <-p.getRateLimitRequest:
			if !ok {
				// Channel closed.  Unexpected, but should be handled.
				logrus.Error("workerPool worker stopped because channel closed")
				return
			}

			resp := new(response)
			p.cacheMu.Lock()
			resp.rl, resp.err = worker.handleGetRateLimit(req.ctx, req.request, p.cache)
			p.cacheMu.Unlock()
			select {
			case req.resp <- resp:
				// Success.

			case <-req.ctx.Done():
				// Context canceled.
				trace.SpanFromContext(req.ctx).RecordError(resp.err)
			}
			metricCommandCounter.WithLabelValues(worker.name, "GetRateLimit").Inc()

		case req, ok := <-p.storeRequest:
			if !ok {
				// Channel closed.  Unexpected, but should be handled.
				logrus.Error("workerPool worker stopped because channel closed")
				return
			}

			p.cacheMu.Lock()
			worker.handleStore(req, p.cache)
			p.cacheMu.Unlock()
			metricCommandCounter.WithLabelValues(worker.name, "Store").Inc()

		case req, ok := <-p.loadRequest:
			if !ok {
				// Channel closed.  Unexpected, but should be handled.
				logrus.Error("workerPool worker stopped because channel closed")
				return
			}

			p.cacheMu.Lock()
			worker.handleLoad(req, p.cache)
			p.cacheMu.Unlock()
			metricCommandCounter.WithLabelValues(worker.name, "Load").Inc()

		case req, ok := <-p.addCacheItemRequest:
			if !ok {
				// Channel closed.  Unexpected, but should be handled.
				logrus.Error("workerPool worker stopped because channel closed")
				return
			}

			p.cacheMu.Lock()
			worker.handleAddCacheItem(req, p.cache)
			p.cacheMu.Unlock()
			metricCommandCounter.WithLabelValues(worker.name, "AddCacheItem").Inc()

		case req, ok := <-p.getCacheItemRequest:
			if !ok {
				// Channel closed.  Unexpected, but should be handled.
				logrus.Error("workerPool worker stopped because channel closed")
				return
			}

			p.cacheMu.Lock()
			worker.handleGetCacheItem(req, p.cache)
			p.cacheMu.Unlock()
			metricCommandCounter.WithLabelValues(worker.name, "GetCacheItem").Inc()

		case <-p.done:
			// Clean up.
			return
		}
	}
}

// GetRateLimit sends a GetRateLimit request to worker pool.
func (p *WorkerPool2) GetRateLimit(ctx context.Context, rlRequest *RateLimitReq) (retval *RateLimitResp, reterr error) {
	// Delegate request to assigned channel based on request key.
	timer2 := prometheus.NewTimer(metricFuncTimeDuration.WithLabelValues("WorkerPool2.GetRateLimit_2"))
	handlerRequest := request{
		ctx:     ctx,
		resp:    make(chan *response, 1),
		request: rlRequest,
	}

	// Send request.
	select {
	case p.getRateLimitRequest <- handlerRequest:
		// Successfully sent request.
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	timer2.ObserveDuration()

	// Wait for response.
	defer prometheus.NewTimer(metricFuncTimeDuration.WithLabelValues("WorkerPool2.GetRateLimit_3")).ObserveDuration()
	select {
	case handlerResponse := <-handlerRequest.resp:
		// Successfully read response.
		return handlerResponse.rl, handlerResponse.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Handle request received by worker.
func (worker *Worker2) handleGetRateLimit(ctx context.Context, req *RateLimitReq, cache Cache) (*RateLimitResp, error) {
	defer prometheus.NewTimer(metricFuncTimeDuration.WithLabelValues("Worker2.handleGetRateLimit")).ObserveDuration()
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
func (p *WorkerPool2) Load(ctx context.Context) (err error) {
	ch, err := p.conf.Loader.Load()
	if err != nil {
		return errors.Wrap(err, "Error in loader.Load")
	}

	type loadChannel struct {
		ch       chan *CacheItem
		respChan chan workerLoadResponse
	}

	loadCh := loadChannel{
		ch:       make(chan *CacheItem),
		respChan: make(chan workerLoadResponse),
	}
	p.loadRequest <- workerLoadRequest{
		ctx:      ctx,
		response: loadCh.respChan,
		in:       loadCh.ch,
	}

	// Send each item.
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
	close(loadCh.ch)

	select {
	case <-loadCh.respChan:
		// Successfully received response.

	case <-ctx.Done():
		// Context canceled.
		return ctx.Err()
	}

	return nil
}

func (worker *Worker2) handleLoad(request workerLoadRequest, cache Cache) {
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
func (p *WorkerPool2) Store(ctx context.Context) (err error) {
	var wg sync.WaitGroup
	out := make(chan *CacheItem, 500)

	// Iterate each worker's cache to `out` channel.
	wg.Add(1)

	go func() {
		defer wg.Done()

		respChan := make(chan workerStoreResponse)
		req := workerStoreRequest{
			ctx:      ctx,
			response: respChan,
			out:      out,
		}

		select {
		case p.storeRequest <- req:
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
	}()

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

func (worker *Worker2) handleStore(request workerStoreRequest, cache Cache) {
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
func (p *WorkerPool2) AddCacheItem(ctx context.Context, key string, item *CacheItem) (err error) {
	respChan := make(chan workerAddCacheItemResponse)
	req := workerAddCacheItemRequest{
		ctx:      ctx,
		response: respChan,
		item:     item,
	}

	select {
	case p.addCacheItemRequest <- req:
		// Successfully sent request.
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

func (worker *Worker2) handleAddCacheItem(request workerAddCacheItemRequest, cache Cache) {
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
func (p *WorkerPool2) GetCacheItem(ctx context.Context, key string) (item *CacheItem, found bool, err error) {
	respChan := make(chan workerGetCacheItemResponse)
	req := workerGetCacheItemRequest{
		ctx:      ctx,
		response: respChan,
		key:      key,
	}

	select {
	case p.getCacheItemRequest <- req:
		// Successfully sent request.
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

func (worker *Worker2) handleGetCacheItem(request workerGetCacheItemRequest, cache Cache) {
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
