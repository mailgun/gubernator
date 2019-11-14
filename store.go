package gubernator

type RateLimitItem interface {
	Algorithm() Algorithm
}

type LeakyBucketItem struct {
	Limit          int64
	Duration       int64
	LimitRemaining int64
	TimeStamp      int64
}

func (LeakyBucketItem) Algorithm() Algorithm {
	return Algorithm_LEAKY_BUCKET
}

type TokenBucketItem RateLimitResp

func (TokenBucketItem) Algorithm() Algorithm {
	return Algorithm_TOKEN_BUCKET
}

// Store methods should avoid blocking as much as possible as these methods are called
// on every rate limit request and will effect the performance of gubernator.
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

var _ Store = &NullStore{}

type NullStore struct {
}

func (ns *NullStore) OnChange(r *RateLimitReq, item RateLimitItem, expireAt int64) {
	return
}

func (ns *NullStore) Get(r *RateLimitReq) (RateLimitItem, bool) {
	return nil, false
}
