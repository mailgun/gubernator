package gubernator

import (
	"github.com/mailgun/gubernator/golang/cache"
)

// Implements token bucket algorithm for rate limiting. https://en.wikipedia.org/wiki/Token_bucket
func tokenBucket(c cache.Cache, r *Request) (*RateLimit, error) {
	item, ok := c.Get(r.HashKey())
	if ok {
		// The following semantic allows for requests of more than the limit to be rejected, but subsequent
		// requests within the same duration that are under the limit to succeed. IE: client attempts to
		// send 1000 emails but 100 is their limit. The request is rejected as over the limit, but since we
		// don't store OVER_LIMIT in the cache the client can retry within the same rate limit duration with
		// 100 emails and the request will succeed.

		rl, ok := item.(*RateLimit)
		if !ok {
			// Client switched algorithms; perhaps due to a migration?
			c.Remove(r.HashKey())
			return tokenBucket(c, r)
		}

		// If we are already at the limit
		if rl.LimitRemaining == 0 {
			rl.Status = Status_OVER_LIMIT
			return rl, nil
		}

		// Client is only interested in retrieving the current status
		if r.Hits == 0 {
			return rl, nil
		}

		// If requested hits takes the remainder
		if rl.LimitRemaining == r.Hits {
			rl.LimitRemaining = 0
			return rl, nil
		}

		// If requested is more than available, then return over the limit without updating the cache.
		if r.Hits > rl.LimitRemaining {
			retStatus := *rl
			retStatus.Status = Status_OVER_LIMIT
			return &retStatus, nil
		}

		rl.LimitRemaining -= r.Hits
		return rl, nil
	}

	// Add a new rate limit to the cache
	expire := cache.MillisecondNow() + r.Duration
	status := &RateLimit{
		Status:         Status_UNDER_LIMIT,
		CurrentLimit:   r.Limit,
		LimitRemaining: r.Limit - r.Hits,
		ResetTime:      expire,
	}

	// Client could be requesting that we always return OVER_LIMIT
	if r.Hits > r.Limit {
		status.Status = Status_OVER_LIMIT
		status.LimitRemaining = 0
	}

	c.Add(r.HashKey(), status, expire)
	return status, nil
}

// Implements leaky bucket algorithm for rate limiting https://en.wikipedia.org/wiki/Leaky_bucket
func leakyBucket(c cache.Cache, r *Request) (*RateLimit, error) {
	type LeakyBucket struct {
		Limit          int64
		Duration       int64
		LimitRemaining int64
		TimeStamp      int64
	}

	now := cache.MillisecondNow()

	item, ok := c.Get(r.HashKey())
	if ok {
		b, ok := item.(*LeakyBucket)
		if !ok {
			// Client switched algorithms; perhaps due to a migration?
			c.Remove(r.HashKey())
			return tokenBucket(c, r)
		}

		rate := b.Duration / r.Limit

		// Calculate how much leaked out of the bucket since the last hit
		elapsed := now - b.TimeStamp
		leak := int64(elapsed / rate)

		b.LimitRemaining += leak
		if b.LimitRemaining > b.Limit {
			b.LimitRemaining = b.Limit
		}

		b.TimeStamp = now
		rl := &RateLimit{
			CurrentLimit:   b.Limit,
			LimitRemaining: b.LimitRemaining,
			Status:         Status_UNDER_LIMIT,
		}

		// If we are already at the limit
		if b.LimitRemaining == 0 {
			rl.Status = Status_OVER_LIMIT
			rl.ResetTime = now + rate
			return rl, nil
		}

		// If requested hits takes the remainder
		if b.LimitRemaining == r.Hits {
			b.LimitRemaining = 0
			rl.LimitRemaining = 0
			return rl, nil
		}

		// If requested is more than available, then return over the limit without updating the bucket.
		if r.Hits > b.LimitRemaining {
			rl.Status = Status_OVER_LIMIT
			rl.ResetTime = now + rate
			return rl, nil
		}

		b.LimitRemaining -= r.Hits
		rl.LimitRemaining = b.LimitRemaining
		c.UpdateExpiration(r.HashKey(), now*r.Duration)
		return rl, nil
	}

	// Create a new leaky bucket
	b := LeakyBucket{
		LimitRemaining: r.Limit - r.Hits,
		Limit:          r.Limit,
		Duration:       r.Duration,
		TimeStamp:      now,
	}

	rl := RateLimit{
		Status:         Status_UNDER_LIMIT,
		CurrentLimit:   r.Limit,
		LimitRemaining: r.Limit - r.Hits,
		ResetTime:      0,
	}

	// Client could be requesting that we start with the bucket OVER_LIMIT
	if r.Hits > r.Limit {
		rl.Status = Status_OVER_LIMIT
		rl.LimitRemaining = 0
		b.LimitRemaining = 0
	}

	c.Add(r.HashKey(), &b, now+r.Duration)

	return &rl, nil
}
