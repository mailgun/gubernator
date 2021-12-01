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
	"context"

	"github.com/mailgun/gubernator/v2/tracing"
	"github.com/mailgun/holster/v4/clock"
)

// Implements token bucket algorithm for rate limiting. https://en.wikipedia.org/wiki/Token_bucket
func tokenBucket(ctx context.Context, s Store, c Cache, r *RateLimitReq) (resp *RateLimitResp, err error) {
	span, ctx := tracing.StartSpan(ctx)
	defer span.Finish()

	getSpan, _ := tracing.StartNamedSpan(ctx, "c.GetItem()")
	item, ok := c.GetItem(r.HashKey())
	getSpan.Finish()

	if s != nil {
		if !ok {
			// Check our store for the item
			if item, ok = s.Get(r); ok {
				addSpan, _ := tracing.StartNamedSpan(ctx, "Check store for rate limit")
				c.Add(item)
				addSpan.Finish()
			}
		}
	}

	if ok {
		tracing.LogInfo(span, "Update existing rate limit")

		if HasBehavior(r.Behavior, Behavior_RESET_REMAINING) {
			removeSpan, _ := tracing.StartNamedSpan(ctx, "c.Remove()")
			c.Remove(r.HashKey())
			removeSpan.Finish()

			if s != nil {
				removeSpan, _ := tracing.StartNamedSpan(ctx, "s.Remove()")
				s.Remove(r.HashKey())
				removeSpan.Finish()
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
			tracing.LogInfo(span, "Client switched algorithms; perhaps due to a migration?")

			removeSpan, _ := tracing.StartNamedSpan(ctx, "c.Remove()")
			c.Remove(r.HashKey())
			removeSpan.Finish()

			if s != nil {
				removeSpan, _ := tracing.StartNamedSpan(ctx, "s.Remove()")
				s.Remove(r.HashKey())
				removeSpan.Finish()
			}
			return tokenBucket(ctx, s, c, r)
		}

		if s != nil {
			defer func() {
				onChangeSpan, _ := tracing.StartNamedSpan(ctx, "s.OnChange()")
				s.OnChange(r, item)
				onChangeSpan.Finish()
			}()
		}

		// Update the limit if it changed
		tracing.LogInfo(span, "Update the limit if changed")
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
			tracing.LogInfo(span, "Duration config changed")
			expire := t.CreatedAt + r.Duration
			if HasBehavior(r.Behavior, Behavior_DURATION_IS_GREGORIAN) {
				expire, err = GregorianExpiration(clock.Now(), r.Duration)
				if err != nil {
					return nil, err
				}
			}
			// If our new duration means we are currently expired
			if expire < MillisecondNow() {
				// Update this so s.OnChange() will get the new expire change
				item.ExpireAt = expire

				removeSpan, _ := tracing.StartNamedSpan(ctx, "c.Remove()")
				c.Remove(item.Key)
				removeSpan.Finish()

				return tokenBucket(ctx, s, c, r)
			}
			item.ExpireAt = expire
			rl.ResetTime = expire
		}

		// Client is only interested in retrieving the current status or updating the rate limit config
		if r.Hits == 0 {
			tracing.LogInfo(span, "Return current status, apply no change")
			return rl, nil
		}

		// If we are already at the limit
		if rl.Remaining == 0 {
			tracing.LogInfo(span, "Already over the limit")
			overLimitCounter.Add(1)
			rl.Status = Status_OVER_LIMIT
			t.Status = rl.Status
			return rl, nil
		}

		// If requested hits takes the remainder
		if t.Remaining == r.Hits {
			tracing.LogInfo(span, "At the limit")
			t.Remaining = 0
			rl.Remaining = 0
			return rl, nil
		}

		// If requested is more than available, then return over the limit without updating the cache.
		if r.Hits > t.Remaining {
			tracing.LogInfo(span, "Over the limit")
			overLimitCounter.Add(1)
			rl.Status = Status_OVER_LIMIT
			return rl, nil
		}

		tracing.LogInfo(span, "Under the limit")
		t.Remaining -= r.Hits
		rl.Remaining = t.Remaining
		return rl, nil
	}

	// Add a new rate limit to the cache
	tracing.LogInfo(span, "Add a new rate limit to the cache")
	now := MillisecondNow()
	expire := now + r.Duration
	if HasBehavior(r.Behavior, Behavior_DURATION_IS_GREGORIAN) {
		expire, err = GregorianExpiration(clock.Now(), r.Duration)
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
		tracing.LogInfo(span, "Over the limit")
		overLimitCounter.Add(1)
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

	addSpan, _ := tracing.StartNamedSpan(ctx, "c.Add()")
	c.Add(item)
	addSpan.Finish()

	if s != nil {
		onChangeSpan, _ := tracing.StartNamedSpan(ctx, "s.OnChange()")
		s.OnChange(r, item)
		onChangeSpan.Finish()
	}
	return rl, nil
}

// Implements leaky bucket algorithm for rate limiting https://en.wikipedia.org/wiki/Leaky_bucket
func leakyBucket(ctx context.Context, s Store, c Cache, r *RateLimitReq) (resp *RateLimitResp, err error) {
	span, ctx := tracing.StartSpan(ctx)
	defer span.Finish()

	if r.Burst == 0 {
		r.Burst = r.Limit
	}

	now := MillisecondNow()
	item, ok := c.GetItem(r.HashKey())
	if s != nil {
		if !ok {
			// Check our store for the item
			span2, _ := tracing.StartNamedSpan(ctx, "Check our store for the item")
			if item, ok = s.Get(r); ok {
				c.Add(item)
			}
			span2.Finish()
		}
	}

	if ok {
		b, ok := item.Value.(*LeakyBucketItem)
		if !ok {
			// Client switched algorithms; perhaps due to a migration?
			c.Remove(r.HashKey())
			if s != nil {
				span2, _ := tracing.StartNamedSpan(ctx, "s.Remove()")
				s.Remove(r.HashKey())
				span2.Finish()
			}
			return leakyBucket(ctx, s, c, r)
		}

		if HasBehavior(r.Behavior, Behavior_RESET_REMAINING) {
			b.Remaining = float64(r.Burst)
		}

		// Update burst, limit and duration if they changed
		if b.Burst != r.Burst {
			if r.Burst > int64(b.Remaining) {
				b.Remaining = float64(r.Burst)
			}
			b.Burst = r.Burst
		}

		b.Limit = r.Limit
		b.Duration = r.Duration

		duration := r.Duration
		rate := float64(duration) / float64(r.Limit)
		if HasBehavior(r.Behavior, Behavior_DURATION_IS_GREGORIAN) {
			d, err := GregorianDuration(clock.Now(), r.Duration)
			if err != nil {
				return nil, err
			}
			n := clock.Now()
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

		// Calculate how much leaked out of the bucket since the last time we leaked a hit
		elapsed := now - b.UpdatedAt
		leak := float64(elapsed) / rate

		if int64(leak) > 0 {
			b.Remaining += leak
			b.UpdatedAt = now
		}

		if int64(b.Remaining) > b.Burst {
			b.Remaining = float64(b.Burst)
		}

		rl := &RateLimitResp{
			Limit:     b.Limit,
			Remaining: int64(b.Remaining),
			Status:    Status_UNDER_LIMIT,
			ResetTime: now + (b.Limit-int64(b.Remaining))*int64(rate),
		}

		if s != nil {
			defer func() {
				span2, _ := tracing.StartNamedSpan(ctx, "s.OnChange()")
				s.OnChange(r, item)
				span2.Finish()
			}()
		}

		// If we are already at the limit
		if int64(b.Remaining) == 0 {
			overLimitCounter.Add(1)
			rl.Status = Status_OVER_LIMIT
			return rl, nil
		}

		// If requested hits takes the remainder
		if int64(b.Remaining) == r.Hits {
			b.Remaining -= float64(r.Hits)
			rl.Remaining = 0
			rl.ResetTime = now + (rl.Limit-rl.Remaining)*int64(rate)
			return rl, nil
		}

		// If requested is more than available, then return over the limit
		// without updating the bucket.
		if r.Hits > int64(b.Remaining) {
			overLimitCounter.Add(1)
			rl.Status = Status_OVER_LIMIT
			return rl, nil
		}

		// Client is only interested in retrieving the current status
		if r.Hits == 0 {
			return rl, nil
		}

		b.Remaining -= float64(r.Hits)
		rl.Remaining = int64(b.Remaining)
		rl.ResetTime = now + (rl.Limit-rl.Remaining)*int64(rate)
		c.UpdateExpiration(r.HashKey(), now+duration)
		return rl, nil
	}

	duration := r.Duration
	rate := float64(duration) / float64(r.Limit)
	if HasBehavior(r.Behavior, Behavior_DURATION_IS_GREGORIAN) {
		n := clock.Now()
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
		Remaining: float64(r.Burst - r.Hits),
		Limit:     r.Limit,
		Duration:  duration,
		UpdatedAt: now,
		Burst:     r.Burst,
	}

	rl := RateLimitResp{
		Status:    Status_UNDER_LIMIT,
		Limit:     b.Limit,
		Remaining: r.Burst - r.Hits,
		ResetTime: now + (b.Limit-(r.Burst-r.Hits))*int64(rate),
	}

	// Client could be requesting that we start with the bucket OVER_LIMIT
	if r.Hits > r.Burst {
		overLimitCounter.Add(1)
		rl.Status = Status_OVER_LIMIT
		rl.Remaining = 0
		rl.ResetTime = now + (rl.Limit-rl.Remaining)*int64(rate)
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
		span2, _ := tracing.StartNamedSpan(ctx, "s.OnChange()")
		s.OnChange(r, item)
		span2.Finish()
	}
	return &rl, nil
}
