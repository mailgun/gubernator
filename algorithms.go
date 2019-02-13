package gubernator

import (
	"github.com/mailgun/gubernator/cache"
	"github.com/mailgun/gubernator/pb"
)

// Implements token bucket algorithm for rate limiting. https://en.wikipedia.org/wiki/Token_bucket
func tokenBucket(c cache.Cache, req *pb.RateLimitRequest) (*pb.RateLimitResponse, error) {
	item, ok := c.Get(req)
	if ok {
		// The following semantic allows for requests of more than the limit to be rejected, but subsequent
		// requests within the same duration that are under the limit to succeed. IE: client attempts to
		// send 1000 emails but 100 is their limit. The request is rejected as over the limit, but since we
		// don't store OVER_LIMIT in the cache the client can retry within the same rate limit duration with
		// 100 emails and the request will succeed.

		status, ok := item.(*pb.RateLimitResponse)
		if !ok {
			// Client switched algorithms; perhaps due to a migration?
			c.Remove(req)
			return tokenBucket(c, req)
		}

		// If we are already at the limit
		if status.LimitRemaining == 0 {
			status.Status = pb.RateLimitResponse_OVER_LIMIT
			return status, nil
		}

		// Client is only interested in retrieving the current status
		if req.Hits == 0 {
			return status, nil
		}

		// If requested hits takes the remainder
		if status.LimitRemaining == req.Hits {
			status.LimitRemaining = 0
			return status, nil
		}

		// If requested is more than available, then return over the limit without updating the cache.
		if req.Hits > status.LimitRemaining {
			retStatus := *status
			retStatus.Status = pb.RateLimitResponse_OVER_LIMIT
			return &retStatus, nil
		}

		status.LimitRemaining -= req.Hits
		return status, nil
	}

	// Add a new rate limit to the cache
	expire := cache.MillisecondNow() + req.RateLimitConfig.Duration
	status := &pb.RateLimitResponse{
		Status:         pb.RateLimitResponse_UNDER_LIMIT,
		CurrentLimit:   req.RateLimitConfig.Limit,
		LimitRemaining: req.RateLimitConfig.Limit - req.Hits,
		ResetTime:      expire,
	}

	// Kind of a weird corner case, but the client could be dumb
	if req.Hits > req.RateLimitConfig.Limit {
		status.LimitRemaining = 0
	}

	c.Add(req, status, expire)
	return status, nil
}

// Implements leaky bucket algorithm for rate limiting https://en.wikipedia.org/wiki/Leaky_bucket
func leakyBucket(c cache.Cache, req *pb.RateLimitRequest) (*pb.RateLimitResponse, error) {
	type LeakyBucket struct {
		RateLimitConfig pb.RateLimitConfig
		LimitRemaining  int64
		TimeStamp       int64
	}

	now := cache.MillisecondNow()

	item, ok := c.Get(req)
	if ok {
		bucket, ok := item.(*LeakyBucket)
		if !ok {
			// Client switched algorithms; perhaps due to a migration?
			c.Remove(req)
			return tokenBucket(c, req)
		}

		rate := bucket.RateLimitConfig.Duration / req.RateLimitConfig.Limit

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
		if bucket.LimitRemaining == req.Hits {
			bucket.LimitRemaining = 0
			status.LimitRemaining = 0
			return status, nil
		}

		// If requested is more than available, then return over the limit without updating the bucket.
		if req.Hits > bucket.LimitRemaining {
			status.Status = pb.RateLimitResponse_OVER_LIMIT
			return status, nil
		}

		bucket.LimitRemaining -= req.Hits
		status.LimitRemaining = bucket.LimitRemaining
		c.UpdateExpiration(req, now*req.RateLimitConfig.Duration)
		return status, nil
	}

	// Create a new leaky bucket
	bucket := LeakyBucket{
		LimitRemaining:  req.RateLimitConfig.Limit - req.Hits,
		RateLimitConfig: *req.RateLimitConfig,
		TimeStamp:       now,
	}

	// Kind of a weird corner case, but the client could be dumb
	if req.Hits > req.RateLimitConfig.Limit {
		bucket.LimitRemaining = 0
	}

	c.Add(req, &bucket, now+req.RateLimitConfig.Duration)

	return &pb.RateLimitResponse{
		Status:         pb.RateLimitResponse_UNDER_LIMIT,
		CurrentLimit:   req.RateLimitConfig.Limit,
		LimitRemaining: req.RateLimitConfig.Limit - req.Hits,
		ResetTime:      0,
	}, nil
}
