package gubernator

// PERSISTENT STORE DETAILS

// The storage interfaces defined here allows the implementor flexibility in storage options. Depending on the
// use case an implementor can only implement the `Loader` interface and only support persistence of
// ratelimits at startup and shutdown or implement `Store` and  gubernator will continuously call `OnChange()`
// and `Get()` to keep the in memory cache and persistent store up to date with the latest ratelimit data.
// Both interfaces can be implemented simultaneously to ensure data is always saved to persistent storage.

type RateLimitItem struct {
	Algorithm Algorithm
	ExpireAt  int64
	HashKey   string
	Value     interface{}
}

type LeakyBucketItem struct {
	Limit          int64
	Duration       int64
	LimitRemaining int64
	TimeStamp      int64
}

type TokenBucketItem RateLimitResp

// Store interface allows implementors to off load storage of all or a subset of ratelimits to
// some persistent store. Methods OnChange() and Get() should avoid blocking as much as possible as these
// methods are called on every rate limit request and will effect the performance of gubernator.
type Store interface {
	// Called by gubernator when a rate limit item is updated. It's up to the store to
	// decide if this rate limit item should be persisted in the store. It's up to the
	// store to expire old rate limit items.
	OnChange(r *RateLimitReq, item RateLimitItem, expireAt int64)

	// Called by gubernator when a rate limit is missing from the cache. It's up to the store
	// to decide if this request is fulfilled. Should return true if the request is fulfilled
	// and false if the request is not fulfilled or doesn't exist in the store.
	Get(r *RateLimitReq) (RateLimitItem, bool)
}

// Loader interface allows implementors to store all or a subset of ratelimits into a persistent
// store during startup and shutdown of the gubernator instance.
type Loader interface {
	// Load is called by gubernator just before the instance is ready to accept requests. The implementation
	// should return a channel gubernator can read to load all rate limits that should be loaded into the
	// instance cache. The implementation should close the channel to indicate no more rate limits left to load.
	Load() (chan RateLimitItem, error)

	// Save is called by gubernator just before the instance is shutdown. The passed channel should be
	// read until the channel is closed.
	Save(chan RateLimitItem) error
}

var _ Store = &NullStore{}

type NullStore struct {
}

func (ns *NullStore) OnChange(r *RateLimitReq, item RateLimitItem, expireAt int64) {
	return
}

func (ns *NullStore) Get(r *RateLimitReq) (RateLimitItem, bool) {
	return nil, false
}

var _ Store = &MockStore{}

type MockStore struct {
	Data []interface{}
}

func (ns *MockStore) OnChange(r *RateLimitReq, item RateLimitItem, expireAt int64) {
	return
}

func (ns *MockStore) Get(r *RateLimitReq) (RateLimitItem, bool) {
	return nil, false
}

var _ Loader = &NullLoader{}

type NullLoader struct {
}

func (ns *NullLoader) Load() (chan RateLimitItem, error) {
	return nil, nil
}

func (ns *NullLoader) Save(int chan RateLimitItem) error {
	return nil
}
