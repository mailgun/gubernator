package gubernator

// PERSISTENT STORE DETAILS

// The storage interfaces defined here allows the implementor flexibility in storage options. Depending on the
// use case an implementor can only implement the `Loader` interface and only support persistence of
// ratelimits at startup and shutdown or implement `Store` and  gubernator will continuously call `OnChange()`
// and `Get()` to keep the in memory cache and persistent store up to date with the latest ratelimit data.
// Both interfaces can be implemented simultaneously to ensure data is always saved to persistent storage.

type LeakyBucketItem struct {
	Limit     int64
	Duration  int64
	Remaining int64
	TimeStamp int64
}

// Store interface allows implementors to off load storage of all or a subset of ratelimits to
// some persistent store. Methods OnChange() and Get() should avoid blocking as much as possible as these
// methods are called on every rate limit request and will effect the performance of gubernator.
type Store interface {
	// Called by gubernator when a rate limit item is updated. It's up to the store to
	// decide if this rate limit item should be persisted in the store. It's up to the
	// store to expire old rate limit items.
	OnChange(r *RateLimitReq, item *CacheItem)

	// Called by gubernator when a rate limit is missing from the cache. It's up to the store
	// to decide if this request is fulfilled. Should return true if the request is fulfilled
	// and false if the request is not fulfilled or doesn't exist in the store.
	Get(r *RateLimitReq) (*CacheItem, bool)

	// Called by gubernator when an existing rate limit should be removed from the store.
	// NOTE: This is NOT called when an rate limit expires from the cache, store implementors
	// must expire rate limits in the store.
	Remove(key string)
}

// Loader interface allows implementors to store all or a subset of ratelimits into a persistent
// store during startup and shutdown of the gubernator instance.
type Loader interface {
	// Load is called by gubernator just before the instance is ready to accept requests. The implementation
	// should return a channel gubernator can read to load all rate limits that should be loaded into the
	// instance cache. The implementation should close the channel to indicate no more rate limits left to load.
	Load() (chan *CacheItem, error)

	// Save is called by gubernator just before the instance is shutdown. The passed channel should be
	// read until the channel is closed.
	Save(chan *CacheItem) error
}

func NewMockStore() *MockStore {
	ml := &MockStore{
		Called:     make(map[string]int),
		CacheItems: make(map[string]*CacheItem),
	}
	ml.Called["OnChange()"] = 0
	ml.Called["Remove()"] = 0
	ml.Called["Get()"] = 0
	return ml
}

type MockStore struct {
	Called     map[string]int
	CacheItems map[string]*CacheItem
}

var _ Store = &MockStore{}

func (ms *MockStore) OnChange(r *RateLimitReq, item *CacheItem) {
	ms.Called["OnChange()"] += 1
	ms.CacheItems[item.Key] = item
}

func (ms *MockStore) Get(r *RateLimitReq) (*CacheItem, bool) {
	ms.Called["Get()"] += 1
	item, ok := ms.CacheItems[r.HashKey()]
	return item, ok
}

func (ms *MockStore) Remove(key string) {
	ms.Called["Remove()"] += 1
	delete(ms.CacheItems, key)
}

func NewMockLoader() *MockLoader {
	ml := &MockLoader{
		Called: make(map[string]int),
	}
	ml.Called["Load()"] = 0
	ml.Called["Save()"] = 0
	return ml
}

type MockLoader struct {
	Called     map[string]int
	CacheItems []*CacheItem
}

var _ Loader = &MockLoader{}

func (ml *MockLoader) Load() (chan *CacheItem, error) {
	ml.Called["Load()"] += 1

	ch := make(chan *CacheItem, 10)
	go func() {
		for _, i := range ml.CacheItems {
			ch <- i
		}
		close(ch)
	}()
	return ch, nil
}

func (ml *MockLoader) Save(in chan *CacheItem) error {
	ml.Called["Save()"] += 1

	for i := range in {
		ml.CacheItems = append(ml.CacheItems, i)
	}
	return nil
}
