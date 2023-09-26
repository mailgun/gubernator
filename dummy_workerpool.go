package gubernator

import (
	"context"
	"fmt"
	"sync"

	"github.com/mailgun/holster/v4/tracing"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/trace"
)

// dummyWorkerPool functions as a WorkerPool with only one worker.
type dummyWorkerPool struct {
	conf   *Config
	done   chan struct{}
	worker *Worker
}

func (p *dummyWorkerPool) Close() error {
	close(p.done)
	return nil
}

func (p *dummyWorkerPool) Done() chan struct{} {
	return p.done
}

// GetRateLimit sends a GetRateLimit request to worker pool.
func (p *dummyWorkerPool) GetRateLimit(ctx context.Context, rlRequest *RateLimitReq) (retval *RateLimitResp, reterr error) {
	// Delegate request to assigned channel based on request key.
	worker := p.worker
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

// Load atomically loads cache from persistent storage.
// Read from persistent storage.  Load into each appropriate worker's cache.
// Workers are locked during this load operation to prevent race conditions.
func (p *dummyWorkerPool) Load(ctx context.Context) (err error) {
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

		worker := p.worker

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

// Store atomically stores cache to persistent storage.
// Save all workers' caches to persistent storage.
// Workers are locked during this store operation to prevent race conditions.
func (p *dummyWorkerPool) Store(ctx context.Context) (err error) {
	ctx = tracing.StartNamedScopeDebug(ctx, "WorkerPool.Store")
	defer func() { tracing.EndScope(ctx, err) }()

	var wg sync.WaitGroup
	out := make(chan *CacheItem, 500)

	// Iterate each worker's cache to `out` channel.
	worker := p.worker
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

// AddCacheItem adds an item to the worker's cache.
func (p *dummyWorkerPool) AddCacheItem(ctx context.Context, key string, item *CacheItem) (err error) {
	ctx = tracing.StartNamedScopeDebug(ctx, "WorkerPool.AddCacheItem")
	tracing.EndScope(ctx, err)

	respChan := make(chan workerAddCacheItemResponse)
	worker := p.worker
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

// GetCacheItem gets item from worker's cache.
func (p *dummyWorkerPool) GetCacheItem(ctx context.Context, key string) (item *CacheItem, found bool, err error) {
	ctx = tracing.StartNamedScopeDebug(ctx, "WorkerPool.GetCacheItem")
	tracing.EndScope(ctx, err)

	respChan := make(chan workerGetCacheItemResponse)
	worker := p.worker
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

