package gubernator

import (
	"github.com/mailgun/gubernator/golang/cache"
	"github.com/mailgun/gubernator/golang/pb"
)

// Implements token bucket algorithm for rate limiting. https://en.wikipedia.org/wiki/Token_bucket
func tokenBucket(c cache.Cache, r *pb.RateLimitRequest) (*pb.RateLimitResponse, error) {
	item, ok := c.Get(r)
	if ok {
		// The following semantic allows for requests of more than the limit to be rejected, but subsequent
		// requests within the same duration that are under the limit to succeed. IE: client attempts to
		// send 1000 emails but 100 is their limit. The request is rejected as over the limit, but since we
		// don't store OVER_LIMIT in the cache the client can retry within the same rate limit duration with
		// 100 emails and the request will succeed.

		status, ok := item.(*pb.RateLimitResponse)
		if !ok {
			// Client switched algorithms; perhaps due to a migration?
			c.Remove(r)
			return tokenBucket(c, r)
		}

		// If we are already at the limit
		if status.LimitRemaining == 0 {
			status.Status = pb.RateLimitResponse_OVER_LIMIT
			return status, nil
		}

		// Client is only interested in retrieving the current status
		if r.Hits == 0 {
			return status, nil
		}

		// If requested hits takes the remainder
		if status.LimitRemaining == r.Hits {
			status.LimitRemaining = 0
			return status, nil
		}

		// If requested is more than available, then return over the limit without updating the cache.
		if r.Hits > status.LimitRemaining {
			retStatus := *status
			retStatus.Status = pb.RateLimitResponse_OVER_LIMIT
			return &retStatus, nil
		}

		status.LimitRemaining -= r.Hits
		return status, nil
	}

	// Add a new rate limit to the cache
	expire := cache.MillisecondNow() + r.RateLimitConfig.Duration
	status := &pb.RateLimitResponse{
		Status:         pb.RateLimitResponse_UNDER_LIMIT,
		CurrentLimit:   r.RateLimitConfig.Limit,
		LimitRemaining: r.RateLimitConfig.Limit - r.Hits,
		ResetTime:      expire,
	}

	// Client could be requesting that we always return OVER_LIMIT
	if r.Hits > r.RateLimitConfig.Limit {
		status.Status = pb.RateLimitResponse_OVER_LIMIT
		status.LimitRemaining = 0
	}

	c.Add(r, status, expire)
	return status, nil
}

// Implements leaky bucket algorithm for rate limiting https://en.wikipedia.org/wiki/Leaky_bucket
func leakyBucket(c cache.Cache, r *pb.RateLimitRequest) (*pb.RateLimitResponse, error) {
	type LeakyBucket struct {
		RateLimitConfig pb.RateLimitConfig
		LimitRemaining  int64
		TimeStamp       int64
	}

	now := cache.MillisecondNow()

	item, ok := c.Get(r)
	if ok {
		bucket, ok := item.(*LeakyBucket)
		if !ok {
			// Client switched algorithms; perhaps due to a migration?
			c.Remove(r)
			return tokenBucket(c, r)
		}

		rate := bucket.RateLimitConfig.Duration / r.RateLimitConfig.Limit

		// Calculate how much leaked out of the bucket since the last hit
		elapsed := now - bucket.TimeStamp
		leak := int64(elapsed / rate)

		bucket.LimitRemaining += leak
		if bucket.LimitRemaining > bucket.RateLimitConfig.Limit {
			bucket.LimitRemaining = bucket.RateLimitConfig.Limit
		}

		bucket.TimeStamp = now
		status := &pb.RateLimitResponse{
			CurrentLimit:   bucket.RateLimitConfig.Limit,
			LimitRemaining: bucket.LimitRemaining,
			Status:         pb.RateLimitResponse_UNDER_LIMIT,
		}

		// If we are already at the limit
		if bucket.LimitRemaining == 0 {
			status.Status = pb.RateLimitResponse_OVER_LIMIT
			status.ResetTime = now + rate
			return status, nil
		}

		// If requested hits takes the remainder
		if bucket.LimitRemaining == r.Hits {
			bucket.LimitRemaining = 0
			status.LimitRemaining = 0
			return status, nil
		}

		// If requested is more than available, then return over the limit without updating the bucket.
		if r.Hits > bucket.LimitRemaining {
			status.Status = pb.RateLimitResponse_OVER_LIMIT
			return status, nil
		}

		bucket.LimitRemaining -= r.Hits
		status.LimitRemaining = bucket.LimitRemaining
		c.UpdateExpiration(r, now*r.RateLimitConfig.Duration)
		return status, nil
	}

	// Create a new leaky bucket
	bucket := LeakyBucket{
		LimitRemaining:  r.RateLimitConfig.Limit - r.Hits,
		RateLimitConfig: *r.RateLimitConfig,
		TimeStamp:       now,
	}

	resp := pb.RateLimitResponse{
		Status:         pb.RateLimitResponse_UNDER_LIMIT,
		CurrentLimit:   r.RateLimitConfig.Limit,
		LimitRemaining: r.RateLimitConfig.Limit - r.Hits,
		ResetTime:      0,
	}

	// Client could be requesting that we start with the bucket OVER_LIMIT
	if r.Hits > r.RateLimitConfig.Limit {
		resp.Status = pb.RateLimitResponse_OVER_LIMIT
		resp.LimitRemaining = 0
		bucket.LimitRemaining = 0
	}

	c.Add(r, &bucket, now+r.RateLimitConfig.Duration)

	return &resp, nil
}
