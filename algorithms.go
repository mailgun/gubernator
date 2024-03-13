/*
Copyright 2018-2022 Mailgun Technologies Inc

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

	"github.com/mailgun/holster/v4/clock"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// ### NOTE ###
// The both token and leaky follow the same semantic which allows for requests of more than the limit
// to be rejected, but subsequent requests within the same window that are under the limit to succeed.
// IE: client attempts to send 1000 emails but 100 is their limit. The request is rejected as over the
// limit, but we do not set the remainder to 0 in the cache. The client can retry within the same window
// with 100 emails and the request will succeed. You can override this default behavior with `DRAIN_OVER_LIMIT`

// Implements token bucket algorithm for rate limiting. https://en.wikipedia.org/wiki/Token_bucket
func tokenBucket(ctx context.Context, s Store, c Cache, r *RateLimitReq, reqState RateLimitReqState) (resp *RateLimitResp, err error) {
	tokenBucketTimer := prometheus.NewTimer(metricFuncTimeDuration.WithLabelValues("tokenBucket"))
	defer tokenBucketTimer.ObserveDuration()

	// Get rate limit from cache.
	hashKey := r.HashKey()
	item, ok := c.GetItem(hashKey)

	if s != nil && !ok {
		// Cache miss.
		// Check our store for the item.
		if item, ok = s.Get(ctx, r); ok {
			c.Add(item)
		}
	}

	// Sanity checks.
	if ok {
		if item.Value == nil {
			msgPart := "tokenBucket: Invalid cache item; Value is nil"
			trace.SpanFromContext(ctx).AddEvent(msgPart, trace.WithAttributes(
				attribute.String("hashKey", hashKey),
				attribute.String("key", r.UniqueKey),
				attribute.String("name", r.Name),
			))
			logrus.Error(msgPart)
			ok = false
		} else if item.Key != hashKey {
			msgPart := "tokenBucket: Invalid cache item; key mismatch"
			trace.SpanFromContext(ctx).AddEvent(msgPart, trace.WithAttributes(
				attribute.String("itemKey", item.Key),
				attribute.String("hashKey", hashKey),
				attribute.String("name", r.Name),
			))
			logrus.Error(msgPart)
			ok = false
		}
	}

	if ok {
		// Item found in cache or store.
		if HasBehavior(r.Behavior, Behavior_RESET_REMAINING) {
			c.Remove(hashKey)

			if s != nil {
				s.Remove(ctx, hashKey)
			}
			return &RateLimitResp{
				Status:    Status_UNDER_LIMIT,
				Limit:     r.Limit,
				Remaining: r.Limit,
				ResetTime: 0,
			}, nil
		}
		t, ok := item.Value.(*TokenBucketItem)
		if !ok {
			// Client switched algorithms; perhaps due to a migration?
			trace.SpanFromContext(ctx).AddEvent("Client switched algorithms; perhaps due to a migration?")

			c.Remove(hashKey)

			if s != nil {
				s.Remove(ctx, hashKey)
			}

			return tokenBucketNewItem(ctx, s, c, r, reqState)
		}

		// Update the limit if it changed.
		if t.Limit != r.Limit {
			// Add difference to remaining.
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

		// If the duration config changed, update the new ExpireAt.
		if t.Duration != r.Duration {
			span := trace.SpanFromContext(ctx)
			span.AddEvent("Duration changed")
			expire := t.CreatedAt + r.Duration
			if HasBehavior(r.Behavior, Behavior_DURATION_IS_GREGORIAN) {
				expire, err = GregorianExpiration(clock.Now(), r.Duration)
				if err != nil {
					return nil, err
				}
			}

			// If our new duration means we are currently expired.
			createdAt := *r.CreatedAt
			if expire <= createdAt {
				// Renew item.
				span.AddEvent("Limit has expired")
				expire = createdAt + r.Duration
				t.CreatedAt = createdAt
				t.Remaining = t.Limit
			}

			item.ExpireAt = expire
			t.Duration = r.Duration
			rl.ResetTime = expire
		}

		if s != nil && reqState.IsOwner {
			defer func() {
				s.OnChange(ctx, r, item)
			}()
		}

		// Client is only interested in retrieving the current status or
		// updating the rate limit config.
		if r.Hits == 0 {
			return rl, nil
		}

		// If we are already at the limit.
		if rl.Remaining == 0 && r.Hits > 0 {
			trace.SpanFromContext(ctx).AddEvent("Already over the limit")
			if reqState.IsOwner {
				metricOverLimitCounter.Add(1)
			}
			rl.Status = Status_OVER_LIMIT
			t.Status = rl.Status
			return rl, nil
		}

		// If requested hits takes the remainder.
		if t.Remaining == r.Hits {
			trace.SpanFromContext(ctx).AddEvent("At the limit")
			t.Remaining = 0
			rl.Remaining = 0
			return rl, nil
		}

		// If requested is more than available, then return over the limit
		// without updating the cache.
		if r.Hits > t.Remaining {
			trace.SpanFromContext(ctx).AddEvent("Over the limit")
			if reqState.IsOwner {
				metricOverLimitCounter.Add(1)
			}
			rl.Status = Status_OVER_LIMIT
			if HasBehavior(r.Behavior, Behavior_DRAIN_OVER_LIMIT) {
				// DRAIN_OVER_LIMIT behavior drains the remaining counter.
				t.Remaining = 0
				rl.Remaining = 0
			}
			return rl, nil
		}

		t.Remaining -= r.Hits
		rl.Remaining = t.Remaining
		return rl, nil
	}

	// Item is not found in cache or store, create new.
	return tokenBucketNewItem(ctx, s, c, r, reqState)
}

// Called by tokenBucket() when adding a new item in the store.
func tokenBucketNewItem(ctx context.Context, s Store, c Cache, r *RateLimitReq, reqState RateLimitReqState) (resp *RateLimitResp, err error) {
	createdAt := *r.CreatedAt
	expire := createdAt + r.Duration

	t := &TokenBucketItem{
		Limit:     r.Limit,
		Duration:  r.Duration,
		Remaining: r.Limit - r.Hits,
		CreatedAt: createdAt,
	}

	// Add a new rate limit to the cache.
	if HasBehavior(r.Behavior, Behavior_DURATION_IS_GREGORIAN) {
		expire, err = GregorianExpiration(clock.Now(), r.Duration)
		if err != nil {
			return nil, err
		}
	}

	item := &CacheItem{
		Algorithm: Algorithm_TOKEN_BUCKET,
		Key:       r.HashKey(),
		Value:     t,
		ExpireAt:  expire,
	}

	rl := &RateLimitResp{
		Status:    Status_UNDER_LIMIT,
		Limit:     r.Limit,
		Remaining: t.Remaining,
		ResetTime: expire,
	}

	// Client could be requesting that we always return OVER_LIMIT.
	if r.Hits > r.Limit {
		trace.SpanFromContext(ctx).AddEvent("Over the limit")
		if reqState.IsOwner {
			metricOverLimitCounter.Add(1)
		}
		rl.Status = Status_OVER_LIMIT
		rl.Remaining = r.Limit
		t.Remaining = r.Limit
	}

	c.Add(item)

	if s != nil && reqState.IsOwner {
		s.OnChange(ctx, r, item)
	}

	return rl, nil
}

// Implements leaky bucket algorithm for rate limiting https://en.wikipedia.org/wiki/Leaky_bucket
func leakyBucket(ctx context.Context, s Store, c Cache, r *RateLimitReq, reqState RateLimitReqState) (resp *RateLimitResp, err error) {
	leakyBucketTimer := prometheus.NewTimer(metricFuncTimeDuration.WithLabelValues("V1Instance.getRateLimit_leakyBucket"))
	defer leakyBucketTimer.ObserveDuration()

	if r.Burst == 0 {
		r.Burst = r.Limit
	}

	createdAt := *r.CreatedAt

	// Get rate limit from cache.
	hashKey := r.HashKey()
	item, ok := c.GetItem(hashKey)

	if s != nil && !ok {
		// Cache miss.
		// Check our store for the item.
		if item, ok = s.Get(ctx, r); ok {
			c.Add(item)
		}
	}

	// Sanity checks.
	if ok {
		if item.Value == nil {
			msgPart := "leakyBucket: Invalid cache item; Value is nil"
			trace.SpanFromContext(ctx).AddEvent(msgPart, trace.WithAttributes(
				attribute.String("hashKey", hashKey),
				attribute.String("key", r.UniqueKey),
				attribute.String("name", r.Name),
			))
			logrus.Error(msgPart)
			ok = false
		} else if item.Key != hashKey {
			msgPart := "leakyBucket: Invalid cache item; key mismatch"
			trace.SpanFromContext(ctx).AddEvent(msgPart, trace.WithAttributes(
				attribute.String("itemKey", item.Key),
				attribute.String("hashKey", hashKey),
				attribute.String("name", r.Name),
			))
			logrus.Error(msgPart)
			ok = false
		}
	}

	if ok {
		// Item found in cache or store.

		b, ok := item.Value.(*LeakyBucketItem)
		if !ok {
			// Client switched algorithms; perhaps due to a migration?
			c.Remove(hashKey)

			if s != nil {
				s.Remove(ctx, hashKey)
			}

			return leakyBucketNewItem(ctx, s, c, r, reqState)
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

		if r.Hits != 0 {
			c.UpdateExpiration(r.HashKey(), createdAt+duration)
		}

		// Calculate how much leaked out of the bucket since the last time we leaked a hit
		elapsed := createdAt - b.UpdatedAt
		leak := float64(elapsed) / rate

		if int64(leak) > 0 {
			b.Remaining += leak
			b.UpdatedAt = createdAt
		}

		if int64(b.Remaining) > b.Burst {
			b.Remaining = float64(b.Burst)
		}

		rl := &RateLimitResp{
			Limit:     b.Limit,
			Remaining: int64(b.Remaining),
			Status:    Status_UNDER_LIMIT,
			ResetTime: createdAt + (b.Limit-int64(b.Remaining))*int64(rate),
		}

		// TODO: Feature missing: check for Duration change between item/request.

		if s != nil && reqState.IsOwner {
			defer func() {
				s.OnChange(ctx, r, item)
			}()
		}

		// If we are already at the limit
		if int64(b.Remaining) == 0 && r.Hits > 0 {
			if reqState.IsOwner {
				metricOverLimitCounter.Add(1)
			}
			rl.Status = Status_OVER_LIMIT
			return rl, nil
		}

		// If requested hits takes the remainder
		if int64(b.Remaining) == r.Hits {
			b.Remaining = 0
			rl.Remaining = int64(b.Remaining)
			rl.ResetTime = createdAt + (rl.Limit-rl.Remaining)*int64(rate)
			return rl, nil
		}

		// If requested is more than available, then return over the limit
		// without updating the bucket, unless `DRAIN_OVER_LIMIT` is set.
		if r.Hits > int64(b.Remaining) {
			if reqState.IsOwner {
				metricOverLimitCounter.Add(1)
			}
			rl.Status = Status_OVER_LIMIT

			// DRAIN_OVER_LIMIT behavior drains the remaining counter.
			if HasBehavior(r.Behavior, Behavior_DRAIN_OVER_LIMIT) {
				b.Remaining = 0
				rl.Remaining = 0
			}

			return rl, nil
		}

		// Client is only interested in retrieving the current status
		if r.Hits == 0 {
			return rl, nil
		}

		b.Remaining -= float64(r.Hits)
		rl.Remaining = int64(b.Remaining)
		rl.ResetTime = createdAt + (rl.Limit-rl.Remaining)*int64(rate)
		return rl, nil
	}

	return leakyBucketNewItem(ctx, s, c, r, reqState)
}

// Called by leakyBucket() when adding a new item in the store.
func leakyBucketNewItem(ctx context.Context, s Store, c Cache, r *RateLimitReq, reqState RateLimitReqState) (resp *RateLimitResp, err error) {
	createdAt := *r.CreatedAt
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
		UpdatedAt: createdAt,
		Burst:     r.Burst,
	}

	rl := RateLimitResp{
		Status:    Status_UNDER_LIMIT,
		Limit:     b.Limit,
		Remaining: r.Burst - r.Hits,
		ResetTime: createdAt + (b.Limit-(r.Burst-r.Hits))*int64(rate),
	}

	// Client could be requesting that we start with the bucket OVER_LIMIT
	if r.Hits > r.Burst {
		if reqState.IsOwner {
			metricOverLimitCounter.Add(1)
		}
		rl.Status = Status_OVER_LIMIT
		rl.Remaining = 0
		rl.ResetTime = createdAt + (rl.Limit-rl.Remaining)*int64(rate)
		b.Remaining = 0
	}

	item := &CacheItem{
		ExpireAt:  createdAt + duration,
		Algorithm: r.Algorithm,
		Key:       r.HashKey(),
		Value:     &b,
	}

	c.Add(item)

	if s != nil && reqState.IsOwner {
		s.OnChange(ctx, r, item)
	}

	return &rl, nil
}
