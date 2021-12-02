package gubernator_test

import (
	"context"
	"math/rand"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mailgun/gubernator/v2"
	"github.com/mailgun/holster/v4/clock"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLRUCache(t *testing.T) {
	const iterations = 1000
	const concurrency = 100
	ctx := context.Background()
	expireAt := clock.Now().Add(1 * time.Hour).UnixMilli()

	t.Run("Happy path", func(t *testing.T) {
		cache := gubernator.NewLRUCache(0)

		// Populate cache.
		for i := 0; i < iterations; i++ {
			key := strconv.Itoa(i)
			item := gubernator.CacheItem{
				Key: key,
				Value: i,
				ExpireAt: expireAt,
			}
			cache.Lock(ctx)
			exists := cache.Add(item)
			cache.Unlock()
			assert.False(t, exists)
		}

		// Validate cache.
		assert.Equal(t, iterations, cache.Size())

		for i := 0; i < iterations; i++ {
			key := strconv.Itoa(i)
			cache.Lock(ctx)
			item, ok := cache.GetItem(key)
			cache.Unlock()
			require.True(t, ok)
			require.NotNil(t, item)
			assert.Equal(t, item.Value, i)
		}

		// Clear cache.
		for i := 0; i < iterations; i++ {
			key := strconv.Itoa(i)
			cache.Lock(ctx)
			cache.Remove(key)
			cache.Unlock()
		}

		assert.Zero(t, cache.Size())
	})

	t.Run("Update an existing key", func(t *testing.T) {
		cache := gubernator.NewLRUCache(0)
		const key = "foobar"

		// Add key.
		item1 := gubernator.CacheItem{
			Key: key,
			Value: "initial value",
			ExpireAt: expireAt,
		}
		exists1 := cache.Add(item1)
		require.False(t, exists1)

		// Update same key.
		item2 := gubernator.CacheItem{
			Key: key,
			Value: "new value",
			ExpireAt: expireAt,
		}
		exists2 := cache.Add(item2)
		require.True(t, exists2)

		// Verify.
		verifyItem, ok := cache.GetItem(key)
		require.True(t, ok)
		assert.Equal(t, item2, verifyItem)
	})

	t.Run("Concurrent reads", func(t *testing.T) {
		cache := gubernator.NewLRUCache(0)

		// Populate cache.
		for i := 0; i < iterations; i++ {
			key := strconv.Itoa(i)
			item := gubernator.CacheItem{
				Key: key,
				Value: i,
				ExpireAt: expireAt,
			}
			exists := cache.Add(item)
			assert.False(t, exists)
		}

		assert.Equal(t, iterations, cache.Size())
		var launchWg, doneWg sync.WaitGroup
		launchWg.Add(1)

		for thread := 0; thread < concurrency; thread++ {
			doneWg.Add(1)

			go func() {
				defer doneWg.Done()
				launchWg.Wait()

				for i := 0; i < iterations; i++ {
					key := strconv.Itoa(i)
					cache.Lock(ctx)
					item, ok := cache.GetItem(key)
					cache.Unlock()
					assert.True(t, ok)
					require.NotNil(t, item)
					assert.Equal(t, item.Value, i)
				}
			}()
		}

		// Wait for goroutines to finish.
		launchWg.Done()
		doneWg.Wait()
	})

	t.Run("Concurrent writes", func(t *testing.T) {
		cache := gubernator.NewLRUCache(0)
		expireAt := clock.Now().Add(1 * time.Hour).UnixMilli()
		var launchWg, doneWg sync.WaitGroup
		launchWg.Add(1)

		for thread := 0; thread < concurrency; thread++ {
			doneWg.Add(1)

			go func() {
				defer doneWg.Done()
				launchWg.Wait()

				for i := 0; i < iterations; i++ {
					key := strconv.Itoa(i)
					item := gubernator.CacheItem{
						Key: key,
						Value: i,
						ExpireAt: expireAt,
					}
					cache.Lock(ctx)
					cache.Add(item)
					cache.Unlock()
				}
			}()
		}

		// Wait for goroutines to finish.
		launchWg.Done()
		doneWg.Wait()
	})


	t.Run("Concurrent reads and writes", func(t *testing.T) {
		cache := gubernator.NewLRUCache(0)

		// Populate cache.
		for i := 0; i < iterations; i++ {
			key := strconv.Itoa(i)
			item := gubernator.CacheItem{
				Key: key,
				Value: i,
				ExpireAt: expireAt,
			}
			cache.Lock(ctx)
			exists := cache.Add(item)
			cache.Unlock()
			assert.False(t, exists)
		}

		assert.Equal(t, iterations, cache.Size())
		var launchWg, doneWg sync.WaitGroup
		launchWg.Add(1)

		for thread := 0; thread < concurrency; thread++ {
			doneWg.Add(2)

			go func() {
				defer doneWg.Done()
				launchWg.Wait()

				for i := 0; i < iterations; i++ {
					key := strconv.Itoa(i)
					cache.Lock(ctx)
					item, ok := cache.GetItem(key)
					cache.Unlock()
					assert.True(t, ok)
					require.NotNil(t, item)
					assert.Equal(t, item.Value, i)
				}
			}()

			go func() {
				defer doneWg.Done()
				launchWg.Wait()

				for i := 0; i < iterations; i++ {
					key := strconv.Itoa(i)
					item := gubernator.CacheItem{
						Key: key,
						Value: i,
						ExpireAt: expireAt,
					}
					cache.Lock(ctx)
					cache.Add(item)
					cache.Unlock()
				}
			}()
		}

		// Wait for goroutines to finish.
		launchWg.Done()
		doneWg.Wait()
	})

	t.Run("Collect metrics during concurrent reads/writes", func(t *testing.T) {
		cache := gubernator.NewLRUCache(0)

		// Populate cache.
		for i := 0; i < iterations; i++ {
			key := strconv.Itoa(i)
			item := gubernator.CacheItem{
				Key: key,
				Value: i,
				ExpireAt: expireAt,
			}
			cache.Lock(ctx)
			cache.Add(item)
			cache.Unlock()
		}

		assert.Equal(t, iterations, cache.Size())
		var launchWg, doneWg sync.WaitGroup
		launchWg.Add(1)

		for thread := 0; thread < concurrency; thread++ {
			doneWg.Add(3)

			go func() {
				defer doneWg.Done()
				launchWg.Wait()

				for i := 0; i < iterations; i++ {
					// Get, cache hit.
					key := strconv.Itoa(i)
					cache.Lock(ctx)
					_, _ = cache.GetItem(key)
					cache.Unlock()

					// Get, cache miss.
					key2 := strconv.Itoa(rand.Intn(1000) + 10000)
					cache.Lock(ctx)
					_, _ = cache.GetItem(key2)
					cache.Unlock()
				}
			}()

			go func() {
				defer doneWg.Done()
				launchWg.Wait()

				for i := 0; i < iterations; i++ {
					// Add existing.
					key := strconv.Itoa(i)
					item := gubernator.CacheItem{
						Key: key,
						Value: i,
						ExpireAt: expireAt,
					}
					cache.Lock(ctx)
					cache.Add(item)
					cache.Unlock()

					// Add new.
					key2 := strconv.Itoa(rand.Intn(1000) + 20000)
					item2 := gubernator.CacheItem{
						Key: key2,
						Value: i,
						ExpireAt: expireAt,
					}
					cache.Lock(ctx)
					cache.Add(item2)
					cache.Unlock()
				}
			}()

			go func() {
				defer doneWg.Done()
				launchWg.Wait()

				for i := 0; i < iterations; i++ {
					// Get metrics.
					ch := make(chan prometheus.Metric, 10)
					cache.Collect(ch)
				}
			}()
		}

		// Wait for goroutines to finish.
		launchWg.Done()
		doneWg.Wait()
	})
}
