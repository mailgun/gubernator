package gubernator_test

import (
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
	expireAt := clock.Now().Add(1 * time.Hour).UnixMilli()
	var mutex sync.Mutex

	t.Run("Happy path", func(t *testing.T) {
		cache := gubernator.NewLRUCache(0)

		// Populate cache.
		for i := 0; i < iterations; i++ {
			key := strconv.Itoa(i)
			item := gubernator.CacheItem{
				Key:      key,
				Value:    i,
				ExpireAt: expireAt,
			}
			mutex.Lock()
			exists := cache.Add(item)
			mutex.Unlock()
			assert.False(t, exists)
		}

		// Validate cache.
		assert.Equal(t, int64(iterations), cache.Size())

		for i := 0; i < iterations; i++ {
			key := strconv.Itoa(i)
			mutex.Lock()
			item, ok := cache.GetItem(key)
			mutex.Unlock()
			require.True(t, ok)
			require.NotNil(t, item)
			assert.Equal(t, item.Value, i)
		}

		// Clear cache.
		for i := 0; i < iterations; i++ {
			key := strconv.Itoa(i)
			mutex.Lock()
			cache.Remove(key)
			mutex.Unlock()
		}

		assert.Zero(t, cache.Size())
	})

	t.Run("Update an existing key", func(t *testing.T) {
		cache := gubernator.NewLRUCache(0)
		const key = "foobar"

		// Add key.
		item1 := gubernator.CacheItem{
			Key:      key,
			Value:    "initial value",
			ExpireAt: expireAt,
		}
		exists1 := cache.Add(item1)
		require.False(t, exists1)

		// Update same key.
		item2 := gubernator.CacheItem{
			Key:      key,
			Value:    "new value",
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
				Key:      key,
				Value:    i,
				ExpireAt: expireAt,
			}
			exists := cache.Add(item)
			assert.False(t, exists)
		}

		assert.Equal(t, int64(iterations), cache.Size())
		var launchWg, doneWg sync.WaitGroup
		launchWg.Add(1)

		for thread := 0; thread < concurrency; thread++ {
			doneWg.Add(1)

			go func() {
				defer doneWg.Done()
				launchWg.Wait()

				for i := 0; i < iterations; i++ {
					key := strconv.Itoa(i)
					mutex.Lock()
					item, ok := cache.GetItem(key)
					mutex.Unlock()
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
						Key:      key,
						Value:    i,
						ExpireAt: expireAt,
					}
					mutex.Lock()
					cache.Add(item)
					mutex.Unlock()
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
				Key:      key,
				Value:    i,
				ExpireAt: expireAt,
			}
			mutex.Lock()
			exists := cache.Add(item)
			mutex.Unlock()
			assert.False(t, exists)
		}

		assert.Equal(t, int64(iterations), cache.Size())
		var launchWg, doneWg sync.WaitGroup
		launchWg.Add(1)

		for thread := 0; thread < concurrency; thread++ {
			doneWg.Add(2)

			go func() {
				defer doneWg.Done()
				launchWg.Wait()

				for i := 0; i < iterations; i++ {
					key := strconv.Itoa(i)
					mutex.Lock()
					item, ok := cache.GetItem(key)
					mutex.Unlock()
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
						Key:      key,
						Value:    i,
						ExpireAt: expireAt,
					}
					mutex.Lock()
					cache.Add(item)
					mutex.Unlock()
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
				Key:      key,
				Value:    i,
				ExpireAt: expireAt,
			}
			mutex.Lock()
			cache.Add(item)
			mutex.Unlock()
		}

		assert.Equal(t, int64(iterations), cache.Size())
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
					mutex.Lock()
					_, _ = cache.GetItem(key)
					mutex.Unlock()

					// Get, cache miss.
					key2 := strconv.Itoa(rand.Intn(1000) + 10000)
					mutex.Lock()
					_, _ = cache.GetItem(key2)
					mutex.Unlock()
				}
			}()

			go func() {
				defer doneWg.Done()
				launchWg.Wait()

				for i := 0; i < iterations; i++ {
					// Add existing.
					key := strconv.Itoa(i)
					item := gubernator.CacheItem{
						Key:      key,
						Value:    i,
						ExpireAt: expireAt,
					}
					mutex.Lock()
					cache.Add(item)
					mutex.Unlock()

					// Add new.
					key2 := strconv.Itoa(rand.Intn(1000) + 20000)
					item2 := gubernator.CacheItem{
						Key:      key2,
						Value:    i,
						ExpireAt: expireAt,
					}
					mutex.Lock()
					cache.Add(item2)
					mutex.Unlock()
				}
			}()

			collector := gubernator.NewLRUCacheCollector()
			collector.AddCache(cache)

			go func() {
				defer doneWg.Done()
				launchWg.Wait()

				for i := 0; i < iterations; i++ {
					// Get metrics.
					ch := make(chan prometheus.Metric, 10)
					collector.Collect(ch)
				}
			}()
		}

		// Wait for goroutines to finish.
		launchWg.Done()
		doneWg.Wait()
	})
}

func BenchmarkLRUCache(b *testing.B) {
	var mutex sync.Mutex

	b.Run("Sequential reads", func(b *testing.B) {
		cache := gubernator.NewLRUCache(b.N)
		expireAt := clock.Now().Add(1 * time.Hour).UnixMilli()

		// Populate cache.
		for i := 0; i < b.N; i++ {
			key := strconv.Itoa(i)
			item := gubernator.CacheItem{
				Key:      key,
				Value:    i,
				ExpireAt: expireAt,
			}
			exists := cache.Add(item)
			assert.False(b, exists)
		}

		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			key := strconv.Itoa(i)
			mutex.Lock()
			_, _ = cache.GetItem(key)
			mutex.Unlock()
		}
	})

	b.Run("Sequential writes", func(b *testing.B) {
		cache := gubernator.NewLRUCache(0)
		expireAt := clock.Now().Add(1 * time.Hour).UnixMilli()

		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			key := strconv.Itoa(i)
			item := gubernator.CacheItem{
				Key:      key,
				Value:    i,
				ExpireAt: expireAt,
			}
			mutex.Lock()
			cache.Add(item)
			mutex.Unlock()
		}
	})

	b.Run("Concurrent reads", func(b *testing.B) {
		cache := gubernator.NewLRUCache(b.N)
		expireAt := clock.Now().Add(1 * time.Hour).UnixMilli()

		// Populate cache.
		for i := 0; i < b.N; i++ {
			key := strconv.Itoa(i)
			item := gubernator.CacheItem{
				Key:      key,
				Value:    i,
				ExpireAt: expireAt,
			}
			exists := cache.Add(item)
			assert.False(b, exists)
		}

		var launchWg, doneWg sync.WaitGroup
		launchWg.Add(1)

		for i := 0; i < b.N; i++ {
			key := strconv.Itoa(i)
			doneWg.Add(1)

			go func() {
				defer doneWg.Done()
				launchWg.Wait()

				mutex.Lock()
				_, _ = cache.GetItem(key)
				mutex.Unlock()
			}()
		}

		b.ReportAllocs()
		b.ResetTimer()
		launchWg.Done()
		doneWg.Wait()
	})

	b.Run("Concurrent writes", func(b *testing.B) {
		cache := gubernator.NewLRUCache(0)
		expireAt := clock.Now().Add(1 * time.Hour).UnixMilli()
		var launchWg, doneWg sync.WaitGroup
		launchWg.Add(1)

		for i := 0; i < b.N; i++ {
			key := strconv.Itoa(i)
			doneWg.Add(1)

			go func(i int) {
				defer doneWg.Done()
				launchWg.Wait()

				item := gubernator.CacheItem{
					Key:      key,
					Value:    i,
					ExpireAt: expireAt,
				}
				mutex.Lock()
				cache.Add(item)
				mutex.Unlock()
			}(i)
		}

		b.ReportAllocs()
		b.ResetTimer()
		launchWg.Done()
		doneWg.Wait()
	})

	b.Run("Concurrent reads and writes of existing keys", func(b *testing.B) {
		cache := gubernator.NewLRUCache(0)
		expireAt := clock.Now().Add(1 * time.Hour).UnixMilli()
		var launchWg, doneWg sync.WaitGroup
		launchWg.Add(1)

		// Populate cache.
		for i := 0; i < b.N; i++ {
			key := strconv.Itoa(i)
			item := gubernator.CacheItem{
				Key:      key,
				Value:    i,
				ExpireAt: expireAt,
			}
			exists := cache.Add(item)
			assert.False(b, exists)
		}

		for i := 0; i < b.N; i++ {
			key := strconv.Itoa(i)
			doneWg.Add(2)

			go func() {
				defer doneWg.Done()
				launchWg.Wait()

				mutex.Lock()
				_, _ = cache.GetItem(key)
				mutex.Unlock()
			}()

			go func(i int) {
				defer doneWg.Done()
				launchWg.Wait()

				item := gubernator.CacheItem{
					Key:      key,
					Value:    i,
					ExpireAt: expireAt,
				}
				mutex.Lock()
				cache.Add(item)
				mutex.Unlock()
			}(i)
		}

		b.ReportAllocs()
		b.ResetTimer()
		launchWg.Done()
		doneWg.Wait()
	})

	b.Run("Concurrent reads and writes of non-existent keys", func(b *testing.B) {
		cache := gubernator.NewLRUCache(0)
		expireAt := clock.Now().Add(1 * time.Hour).UnixMilli()
		var launchWg, doneWg sync.WaitGroup
		launchWg.Add(1)

		for i := 0; i < b.N; i++ {
			doneWg.Add(2)

			go func(i int) {
				defer doneWg.Done()
				launchWg.Wait()

				key := strconv.Itoa(i)
				mutex.Lock()
				_, _ = cache.GetItem(key)
				mutex.Unlock()
			}(i)

			go func(i int) {
				defer doneWg.Done()
				launchWg.Wait()

				key := "z" + strconv.Itoa(i)
				item := gubernator.CacheItem{
					Key:      key,
					Value:    i,
					ExpireAt: expireAt,
				}
				mutex.Lock()
				cache.Add(item)
				mutex.Unlock()
			}(i)
		}

		b.ReportAllocs()
		b.ResetTimer()
		launchWg.Done()
		doneWg.Wait()
	})
}
