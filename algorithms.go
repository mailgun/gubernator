/*
Copyright 2018-2019 Mailgun Technologies Inc

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

import (
	"time"
)

// Implements token bucket algorithm for rate limiting. https://en.wikipedia.org/wiki/Token_bucket
func tokenBucket(s Store, c Cache, r *RateLimitReq) (resp *RateLimitResp, err error) {
	item, ok := c.GetItem(r.HashKey())
	if s != nil {
		if !ok {
			// Check our store for the item
			if item, ok = s.Get(r); ok {
				c.Add(item)
			}
		}
	}

	if ok {
		if HasBehavior(r.Behavior, Behavior_RESET_REMAINING) {
			c.Remove(r.HashKey())
			if s != nil {
				s.Remove(r.HashKey())
			}
			return &RateLimitResp{
				Status:    Status_UNDER_LIMIT,
				Limit:     r.Limit,
				Remaining: r.Limit,
				ResetTime: 0,
			}, nil
		}

		// The following semantic allows for requests of more than the limit to be rejected, but subsequent
		// requests within the same duration that are under the limit to succeed. IE: client attempts to
		// send 1000 emails but 100 is their limit. The request is rejected as over the limit, but since we
		// don't store OVER_LIMIT in the cache the client can retry within the same rate limit duration with
		// 100 emails and the request will succeed.
		t, ok := item.Value.(*TokenBucketItem)
		if !ok {
			// Client switched algorithms; perhaps due to a migration?
			c.Remove(r.HashKey())
			if s != nil {
				s.Remove(r.HashKey())
			}
			return tokenBucket(s, c, r)
		}

		if s != nil {
			defer func() {
				s.OnChange(r, item)
			}()
		}

		// Update the limit if it changed
		if t.Limit != r.Limit {
			// Add difference to remaining
			t.Remaining += r.Limit - t.Limit
			if t.Remaining < 0 {
				t.Remaining = 0
			}
			t.Limit = r.Limit
		}

		rl := &RateLimitResp{
			Status:    t.Status,
			Limit:     r.Limit,
			Remaining: t.Remaining,
			ResetTime: item.ExpireAt,
		}

		// If the duration config changed, update the new ExpireAt
		if t.Duration != r.Duration {
			expire := t.CreatedAt + r.Duration
			if HasBehavior(r.Behavior, Behavior_DURATION_IS_GREGORIAN) {
				expire, err = GregorianExpiration(time.Now(), r.Duration)
				if err != nil {
					return nil, err
				}
			}
			// If our new duration means we are currently expired
			if expire < MillisecondNow() {
				// Update this so s.OnChange() will get the new expire change
				item.ExpireAt = expire
				c.Remove(item.Key)
				return tokenBucket(s, c, r)
			}
			item.ExpireAt = expire
			rl.ResetTime = expire
		}

		// Client is only interested in retrieving the current status or updating the rate limit config
		if r.Hits == 0 {
			return rl, nil
		}

		// If we are already at the limit
		if rl.Remaining == 0 {
			rl.Status = Status_OVER_LIMIT
			t.Status = rl.Status
			return rl, nil
		}

		// If requested hits takes the remainder
		if t.Remaining == r.Hits {
			t.Remaining = 0
			rl.Remaining = 0
			return rl, nil
		}

		// If requested is more than available, then return over the limit without updating the cache.
		if r.Hits > t.Remaining {
			rl.Status = Status_OVER_LIMIT
			return rl, nil
		}

		t.Remaining -= r.Hits
		rl.Remaining = t.Remaining
		return rl, nil
	}

	// Add a new rate limit to the cache
	now := MillisecondNow()
	expire := now + r.Duration
	if HasBehavior(r.Behavior, Behavior_DURATION_IS_GREGORIAN) {
		expire, err = GregorianExpiration(time.Now(), r.Duration)
		if err != nil {
			return nil, err
		}
	}

	t := &TokenBucketItem{
		Limit:     r.Limit,
		Duration:  r.Duration,
		Remaining: r.Limit - r.Hits,
		CreatedAt: now,
	}

	rl := &RateLimitResp{
		Status:    Status_UNDER_LIMIT,
		Limit:     r.Limit,
		Remaining: t.Remaining,
		ResetTime: expire,
	}

	// Client could be requesting that we always return OVER_LIMIT
	if r.Hits > r.Limit {
		rl.Status = Status_OVER_LIMIT
		rl.Remaining = r.Limit
		t.Remaining = r.Limit
	}

	item = &CacheItem{
		Algorithm: r.Algorithm,
		Key:       r.HashKey(),
		Value:     t,
		ExpireAt:  expire,
	}

	c.Add(item)
	if s != nil {
		s.OnChange(r, item)
	}
	return rl, nil
}

// Implements leaky bucket algorithm for rate limiting https://en.wikipedia.org/wiki/Leaky_bucket
func leakyBucket(s Store, c Cache, r *RateLimitReq) (resp *RateLimitResp, err error) {
	now := MillisecondNow()
	item, ok := c.GetItem(r.HashKey())
	if s != nil {
		if !ok {
			// Check our store for the item
			if item, ok = s.Get(r); ok {
				c.Add(item)
			}
		}
	}

	if ok {
		b, ok := item.Value.(*LeakyBucketItem)
		if !ok {
			// Client switched algorithms; perhaps due to a migration?
			c.Remove(r.HashKey())
			if s != nil {
				s.Remove(r.HashKey())
			}
			return leakyBucket(s, c, r)
		}

		if HasBehavior(r.Behavior, Behavior_RESET_REMAINING) {
			b.Remaining = r.Limit
		}

		// Update limit and duration if they changed
		b.Limit = r.Limit
		b.Duration = r.Duration

		duration := r.Duration
		rate := float64(duration) / float64(r.Limit)
		if HasBehavior(r.Behavior, Behavior_DURATION_IS_GREGORIAN) {
			d, err := GregorianDuration(time.Now(), r.Duration)
			if err != nil {
				return nil, err
			}
			n := time.Now()
			expire, err := GregorianExpiration(n, r.Duration)
			if err != nil {
				return nil, err
			}

			// Calculate the rate using the entire duration of the gregorian interval
			// IE: Minute = 60,000 milliseconds, etc.. etc..
			rate = float64(d) / float64(r.Limit)
			// Update the duration to be the end of the gregorian interval
			duration = expire - (n.UnixNano() / 1000000)
		}

		// Calculate how much leaked out of the bucket since the last hit
		elapsed := now - b.UpdatedAt
		leak := int64(float64(elapsed) / rate)

		b.Remaining += leak
		if b.Remaining > b.Limit {
			b.Remaining = b.Limit
		}

		rl := &RateLimitResp{
			Limit:     b.Limit,
			Remaining: b.Remaining,
			Status:    Status_UNDER_LIMIT,
			ResetTime: now + int64(rate),
		}

		if s != nil {
			defer func() {
				s.OnChange(r, item)
			}()
		}

		// If we are already at the limit
		if b.Remaining == 0 {
			rl.Status = Status_OVER_LIMIT
			return rl, nil
		}

		// Only update the timestamp if client is incrementing the hit
		if r.Hits != 0 {
			b.UpdatedAt = now
		}

		// If requested hits takes the remainder
		if b.Remaining == r.Hits {
			b.Remaining = 0
			rl.Remaining = 0
			return rl, nil
		}

		// If requested is more than available, then return over the limit
		// without updating the bucket.
		if r.Hits > b.Remaining {
			rl.Status = Status_OVER_LIMIT
			return rl, nil
		}

		// Client is only interested in retrieving the current status
		if r.Hits == 0 {
			return rl, nil
		}

		b.Remaining -= r.Hits
		rl.Remaining = b.Remaining
		c.UpdateExpiration(r.HashKey(), now*duration)
		return rl, nil
	}

	duration := r.Duration
	if HasBehavior(r.Behavior, Behavior_DURATION_IS_GREGORIAN) {
		n := time.Now()
		expire, err := GregorianExpiration(n, r.Duration)
		if err != nil {
			return nil, err
		}
		// Set the initial duration as the remainder of time until
		// the end of the gregorian interval.
		duration = expire - (n.UnixNano() / 1000000)
	}

	// Create a new leaky bucket
	b := LeakyBucketItem{
		Remaining: r.Limit - r.Hits,
		Limit:     r.Limit,
		Duration:  duration,
		UpdatedAt: now,
	}

	rl := RateLimitResp{
		Status:    Status_UNDER_LIMIT,
		Limit:     r.Limit,
		Remaining: r.Limit - r.Hits,
		ResetTime: duration / r.Limit,
	}

	// Client could be requesting that we start with the bucket OVER_LIMIT
	if r.Hits > r.Limit {
		rl.Status = Status_OVER_LIMIT
		rl.Remaining = 0
		b.Remaining = 0
	}

	item = &CacheItem{
		ExpireAt:  now + duration,
		Algorithm: r.Algorithm,
		Key:       r.HashKey(),
		Value:     &b,
	}
	c.Add(item)
	if s != nil {
		s.OnChange(r, item)
	}
	return &rl, nil
}
