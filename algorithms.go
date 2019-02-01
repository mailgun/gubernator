package gubernator

import (
	"github.com/mailgun/gubernator/cache"
	"github.com/mailgun/gubernator/pb"
	"github.com/pkg/errors"
)

// Implements token bucket algorithm for rate limiting. https://en.wikipedia.org/wiki/Token_bucket
func tokenBucket(c cache.Cache, entry *pb.RateLimitKeyRequest_Entry) (*pb.DescriptorStatus, error) {
	item, ok := c.Get(string(entry.Key))
	if ok {
		// The following semantic allows for requests of more than the limit to be rejected, but subsequent
		// requests within the same duration that are under the limit to succeed. IE: client attempts to
		// send 1000 emails but 100 is their limit. The request is rejected as over the limit, but since we
		// don't store OVER_LIMIT in the cache the client can retry within the same rate limit duration with
		// 100 emails and the request will succeed.

		status, ok := item.(*pb.DescriptorStatus)
		if !ok {
			return nil, errors.New("incorrect algorithm; don't change algorithms on subsequent requests")
		}

		// If we are already at the limit
		if status.LimitRemaining == 0 {
			status.Status = pb.DescriptorStatus_OVER_LIMIT
			return status, nil
		}

		// If requested hits takes the remainder
		if status.LimitRemaining == entry.Hits {
			status.LimitRemaining = 0
			return status, nil
		}

		// If requested is more than available, then return over the limit without updating the cache.
		if entry.Hits > status.LimitRemaining {
			retStatus := *status
			retStatus.Status = pb.DescriptorStatus_OVER_LIMIT
			return &retStatus, nil
		}

		status.LimitRemaining -= entry.Hits
		return status, nil
	}

	// Add a new rate limit to the cache
	expire := cache.MillisecondNow() + entry.RateLimitConfig.Duration
	status := &pb.DescriptorStatus{
		Status:         pb.DescriptorStatus_OK,
		CurrentLimit:   entry.RateLimitConfig.Limit,
		LimitRemaining: entry.RateLimitConfig.Limit - entry.Hits,
		ResetTime:      expire,
	}

	// Kind of a weird corner case, but the client could be dumb
	if entry.Hits > entry.RateLimitConfig.Limit {
		status.LimitRemaining = 0
	}

	c.Add(string(entry.Key), status, expire)
	return status, nil
}

// Implements leaky bucket algorithm for rate limiting https://en.wikipedia.org/wiki/Leaky_bucket
func leakyBucket(c cache.Cache, entry *pb.RateLimitKeyRequest_Entry) (*pb.DescriptorStatus, error) {
	type LeakyBucket struct {
		RateLimitConfig pb.RateLimitConfig
		LimitRemaining  int64
		TimeStamp       int64
	}

	now := cache.MillisecondNow()
	key := string(entry.Key)

	item, ok := c.Get(key)
	if ok {
		bucket, ok := item.(*LeakyBucket)
		if !ok {
			return nil, errors.New("incorrect algorithm; don't change algorithms on subsequent requests")
		}

		rate := bucket.RateLimitConfig.Duration / entry.RateLimitConfig.Limit

		// Calculate how much leaked out of the bucket since the last hit
		elapsed := now - bucket.TimeStamp
		leak := int64(elapsed / rate)

		bucket.LimitRemaining += leak
		if bucket.LimitRemaining > bucket.RateLimitConfig.Limit {
			bucket.LimitRemaining = bucket.RateLimitConfig.Limit
		}

		bucket.TimeStamp = now
		status := &pb.DescriptorStatus{
			CurrentLimit:   bucket.RateLimitConfig.Limit,
			LimitRemaining: bucket.LimitRemaining,
			Status:         pb.DescriptorStatus_OK,
		}

		// If we are already at the limit
		if bucket.LimitRemaining == 0 {
			status.Status = pb.DescriptorStatus_OVER_LIMIT
			status.ResetTime = now + rate
			return status, nil
		}

		// If requested hits takes the remainder
		if bucket.LimitRemaining == entry.Hits {
			bucket.LimitRemaining = 0
			status.LimitRemaining = 0
			return status, nil
		}

		// If requested is more than available, then return over the limit without updating the bucket.
		if entry.Hits > bucket.LimitRemaining {
			status.Status = pb.DescriptorStatus_OVER_LIMIT
			return status, nil
		}

		bucket.LimitRemaining -= entry.Hits
		status.LimitRemaining = bucket.LimitRemaining
		c.UpdateExpiration(key, now*entry.RateLimitConfig.Duration)
		return status, nil
	}

	// Create a new leaky bucket
	bucket := LeakyBucket{
		LimitRemaining:  entry.RateLimitConfig.Limit - entry.Hits,
		RateLimitConfig: *entry.RateLimitConfig,
		TimeStamp:       now,
	}

	// Kind of a weird corner case, but the client could be dumb
	if entry.Hits > entry.RateLimitConfig.Limit {
		bucket.LimitRemaining = 0
	}

	c.Add(string(entry.Key), &bucket, now+entry.RateLimitConfig.Duration)

	return &pb.DescriptorStatus{
		Status:         pb.DescriptorStatus_OK,
		CurrentLimit:   entry.RateLimitConfig.Limit,
		LimitRemaining: entry.RateLimitConfig.Limit - entry.Hits,
		ResetTime:      0,
	}, nil
}
