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
	"github.com/mailgun/holster/v4/tracing"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Implements token bucket algorithm for rate limiting. https://en.wikipedia.org/wiki/Token_bucket
func tokenBucket(ctx context.Context, s Store, c Cache, r *RateLimitReq) (resp *RateLimitResp, err error) {
	ctx = tracing.StartScopeDebug(ctx)
	defer func() {
		tracing.EndScope(ctx, err)
	}()
	span := trace.SpanFromContext(ctx)

	tokenBucketTimer := prometheus.NewTimer(funcTimeMetric.WithLabelValues("tokenBucket"))
	defer tokenBucketTimer.ObserveDuration()

	// load value from cache or store
	item, err := loadAndCheckBucket(ctx, s, c, r)
	if err != nil {
		// Item is not found in cache or store, create new.
		return tokenBucketNewItem(ctx, s, c, r)
	}

	// Item found in cache or store.
	span.AddEvent("Check existing rate limit")

	if HasBehavior(r.Behavior, Behavior_RESET_REMAINING) {
		hashKey := r.HashKey()
		c.Remove(hashKey)
		span.AddEvent("c.Remove()")

		if s != nil {
			s.Remove(ctx, hashKey)
			span.AddEvent("s.Remove()")
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
		span.AddEvent("Client switched algorithms; perhaps due to a migration?")

		hashKey := r.HashKey()
		c.Remove(hashKey)
		span.AddEvent("c.Remove()")

		if s != nil {
			s.Remove(ctx, hashKey)
			span.AddEvent("s.Remove()")
		}

		return tokenBucketNewItem(ctx, s, c, r)
	}

	// Update the limit if it changed.
	span.AddEvent("Update the limit if changed")
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
		span.AddEvent("Duration changed")
		expire := t.CreatedAt + r.Duration
		if HasBehavior(r.Behavior, Behavior_DURATION_IS_GREGORIAN) {
			expire, err = GregorianExpiration(clock.Now(), r.Duration)
			if err != nil {
				return nil, err
			}
		}

		// If our new duration means we are currently expired.
		now := MillisecondNow()
		if expire <= now {
			// Renew item.
			span.AddEvent("Limit has expired")
			expire = now + r.Duration
			t.CreatedAt = now
			t.Remaining = t.Limit
		}

		item.ExpireAt = expire
		t.Duration = r.Duration
		rl.ResetTime = expire
	}

	if s != nil {
		defer func() {
			s.OnChange(ctx, r, item)
			span.AddEvent("defer s.OnChange()")
		}()
	}

	// Client is only interested in retrieving the current status or
	// updating the rate limit config.
	if r.Hits == 0 {
		span.AddEvent("Return current status, apply no change")
		return rl, nil
	}

	// If we are already at the limit.
	if rl.Remaining == 0 && r.Hits > 0 {
		span.AddEvent("Already over the limit")
		rl.Status = Status_OVER_LIMIT
		t.Status = rl.Status
		return rl, nil
	}

	// If requested hits takes the remainder.
	if t.Remaining == r.Hits {
		span.AddEvent("At the limit")
		rl.Remaining = 0
		return rl, nil
	}

	// If requested is more than available, then return over the limit
	// without updating the cache.
	if r.Hits > t.Remaining {
		span.AddEvent("Over the limit")
		rl.Status = Status_OVER_LIMIT
		return rl, nil
	}

	span.AddEvent("Under the limit")
	rl.Remaining = t.Remaining - r.Hits
	return rl, nil
}

// Called by tokenBucket() when adding a new item in the store.
func tokenBucketNewItem(ctx context.Context, s Store, c Cache, r *RateLimitReq) (resp *RateLimitResp, err error) {
	ctx = tracing.StartScopeDebug(ctx)
	defer func() {
		tracing.EndScope(ctx, err)
	}()
	span := trace.SpanFromContext(ctx)

	now := MillisecondNow()
	expire := now + r.Duration

	t := &TokenBucketItem{
		Limit:     r.Limit,
		Duration:  r.Duration,
		Remaining: r.Limit,
		CreatedAt: now,
	}
	item := &CacheItem{
		Algorithm: Algorithm_TOKEN_BUCKET,
		Key:       r.HashKey(),
		Value:     t,
		ExpireAt:  expire,
	}

	// Add a new rate limit to the cache.
	span.AddEvent("Add a new rate limit to the cache")
	if HasBehavior(r.Behavior, Behavior_DURATION_IS_GREGORIAN) {
		expire, err = GregorianExpiration(clock.Now(), r.Duration)
		if err != nil {
			return nil, err
		}
	}

	rl := &RateLimitResp{
		Status:    Status_UNDER_LIMIT,
		Limit:     r.Limit,
		Remaining: r.Limit - r.Hits,
		ResetTime: expire,
	}

	// Client could be requesting that we always return OVER_LIMIT.
	if r.Hits > r.Limit {
		span.AddEvent("Over the limit")
		rl.Status = Status_OVER_LIMIT
		rl.Remaining = r.Limit
		t.Remaining = r.Limit
	}

	c.Add(item)
	span.AddEvent("c.Add()")

	if s != nil {
		s.OnChange(ctx, r, item)
		span.AddEvent("s.OnChange()")
	}

	return rl, nil
}

// Implements leaky bucket algorithm for rate limiting https://en.wikipedia.org/wiki/Leaky_bucket
func leakyBucket(ctx context.Context, s Store, c Cache, r *RateLimitReq) (resp *RateLimitResp, err error) {
	ctx = tracing.StartScopeDebug(ctx)
	defer func() {
		tracing.EndScope(ctx, err)
	}()
	span := trace.SpanFromContext(ctx)

	leakyBucketTimer := prometheus.NewTimer(funcTimeMetric.WithLabelValues("V1Instance.getRateLimit_leakyBucket"))
	defer leakyBucketTimer.ObserveDuration()

	if r.Burst == 0 {
		r.Burst = r.Limit
	}

	now := MillisecondNow()

	// load value from cache or store
	item, err := loadAndCheckBucket(ctx, s, c, r)
	if err != nil {
		// Item is not found in cache or store, create new.
		return leakyBucketNewItem(ctx, s, c, r)
	}

	// Item found in cache or store.
	span.AddEvent("Check existing rate limit")

	b, ok := item.Value.(*LeakyBucketItem)
	if !ok {
		// Client switched algorithms; perhaps due to a migration?
		hashKey := r.HashKey()
		c.Remove(hashKey)
		span.AddEvent("c.Remove()")

		if s != nil {
			s.Remove(ctx, hashKey)
			span.AddEvent("s.Remove()")
		}

		return leakyBucketNewItem(ctx, s, c, r)
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
		c.UpdateExpiration(r.HashKey(), now+duration)
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

	// TODO: Feature missing: check for Duration change between item/request.

	if s != nil {
		defer func() {
			s.OnChange(ctx, r, item)
			span.AddEvent("s.OnChange()")
		}()
	}

	// If we are already at the limit
	if int64(b.Remaining) == 0 && r.Hits > 0 {
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
		rl.Status = Status_OVER_LIMIT
		return rl, nil
	}

	// Client is only interested in retrieving the current status
	if r.Hits == 0 {
		return rl, nil
	}

	rl.Remaining = int64(b.Remaining - float64(r.Hits))
	rl.ResetTime = now + (rl.Limit-rl.Remaining)*int64(rate)
	return rl, nil
}

// Called by leakyBucket() when adding a new item in the store.
func leakyBucketNewItem(ctx context.Context, s Store, c Cache, r *RateLimitReq) (resp *RateLimitResp, err error) {
	ctx = tracing.StartScopeDebug(ctx)
	defer func() {
		tracing.EndScope(ctx, err)
	}()
	span := trace.SpanFromContext(ctx)

	now := MillisecondNow()
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
		Remaining: float64(r.Burst),
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
		rl.Status = Status_OVER_LIMIT
		rl.Remaining = 0
		rl.ResetTime = now + (rl.Limit-rl.Remaining)*int64(rate)
	}

	item := &CacheItem{
		ExpireAt:  now + duration,
		Algorithm: r.Algorithm,
		Key:       r.HashKey(),
		Value:     &b,
	}

	c.Add(item)
	span.AddEvent("c.Add()")

	if s != nil {
		s.OnChange(ctx, r, item)
		span.AddEvent("s.OnChange()")
	}

	return &rl, nil
}

// bucket has been checked and response generated using the algorithms, persist the hits only
func persistBucketHits(ctx context.Context, s Store, c Cache, r *RateLimitReq, status int32) error {
	var err error
	ctx = tracing.StartScopeDebug(ctx)
	defer func() {
		tracing.EndScope(ctx, err)
	}()
	span := trace.SpanFromContext(ctx)

	// load value from cache or store
	item, err := loadAndCheckBucket(ctx, s, c, r)
	if err != nil {
		return err
	}

	span.AddEvent("Updating rate limit")

	// for leaky bucket we update remaining as it will need to be zero if over limit
	if r.Algorithm == Algorithm_LEAKY_BUCKET {
		b, ok := item.Value.(*LeakyBucketItem)
		if ok {
			// for leaky bucket the remaining will need to be zero as a minumum
			b.Remaining -= float64(r.Hits)
			if b.Remaining < 0 {
				b.Remaining = 0
			}
		} else {
			return errors.New("unexpected cache value on update, expected LeakyBucketItem")
		}
	}
	// handle over limit response, otherwise apply update if it is token bucket
	if status == int32(Status_OVER_LIMIT) {
		overLimitCounter.Add(1)
	} else {
		// handle a token bucket update
		if r.Algorithm == Algorithm_TOKEN_BUCKET {
			t, ok := item.Value.(*TokenBucketItem)
			if ok {
				t.Remaining -= r.Hits
			} else {
				return errors.New("unexpected cache value on update, expected TokenBucketItem")
			}
		}
	}
	return nil
}

func loadAndCheckBucket(ctx context.Context, s Store, c Cache, r *RateLimitReq) (*CacheItem, error) {
	var err error
	ctx = tracing.StartScopeDebug(ctx)
	defer func() {
		tracing.EndScope(ctx, err)
	}()
	span := trace.SpanFromContext(ctx)

	// read cache otherwise get from store
	// Get rate limit from cache.
	hashKey := r.HashKey()
	item, ok := c.GetItem(hashKey)
	span.AddEvent("c.GetItem()")

	if s != nil && !ok {
		// Cache miss.
		// Check our store for the item.
		if item, ok = s.Get(ctx, r); ok {
			span.AddEvent("Check store for rate limit")
			c.Add(item)
			span.AddEvent("c.Add()")
		}
	}

	// Sanity checks.
	if ok {
		if item.Value == nil {
			msgPart := "Invalid cache item; Value is nil"
			span.AddEvent(msgPart, trace.WithAttributes(
				attribute.String("hashKey", hashKey),
				attribute.String("key", r.UniqueKey),
				attribute.String("name", r.Name),
			))
			logrus.Error(msgPart)
			return nil, errors.New("cache item integrity check failed")
		} else if item.Key != hashKey {
			msgPart := "Invalid cache item; key mismatch"
			span.AddEvent(msgPart, trace.WithAttributes(
				attribute.String("itemKey", item.Key),
				attribute.String("hashKey", hashKey),
				attribute.String("name", r.Name),
			))
			logrus.Error(msgPart)
			return nil, errors.New("cache item integrity check failed")
		}
	} else {
		return nil, errors.New("no item in store")
	}
	return item, nil
}
