package gubernator_test

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mailgun/gubernator/v2"
	"github.com/mailgun/holster/v4/clock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSyncLRUCache(t *testing.T) {
	const iterations = 1000
	const concurrency = 100

	t.Run("Happy path", func(t *testing.T) {
		syncCache := gubernator.NewSyncLRUCache(0)
		defer syncCache.Close()
		expireAt := clock.Now().Add(1 * time.Hour).UnixMilli()

		// Populate cache.
		for i := 0; i < iterations; i++ {
			key := strconv.Itoa(i)
			item := gubernator.CacheItem{
				Key:      key,
				Value:    i,
				ExpireAt: expireAt,
			}
			exists := syncCache.Add(item)
			assert.False(t, exists)
		}

		// Validate cache.
		assert.Equal(t, iterations, syncCache.Size())

		for i := 0; i < iterations; i++ {
			key := strconv.Itoa(i)
			item, ok := syncCache.GetItem(key)
			assert.True(t, ok)
			require.NotNil(t, item)
			assert.Equal(t, item.Value, i)
		}

		// Clear cache.
		for i := 0; i < iterations; i++ {
			key := strconv.Itoa(i)
			syncCache.Remove(key)
		}

		assert.Zero(t, syncCache.Size())
	})

	t.Run("Concurrent reads", func(t *testing.T) {
		syncCache := gubernator.NewSyncLRUCache(0)
		defer syncCache.Close()
		expireAt := clock.Now().Add(1 * time.Hour).UnixMilli()

		// Populate cache.
		for i := 0; i < iterations; i++ {
			key := strconv.Itoa(i)
			item := gubernator.CacheItem{
				Key:      key,
				Value:    i,
				ExpireAt: expireAt,
			}
			exists := syncCache.Add(item)
			assert.False(t, exists)
		}

		assert.Equal(t, iterations, syncCache.Size())
		var launchWg, doneWg sync.WaitGroup
		launchWg.Add(1)

		for thread := 0; thread < concurrency; thread++ {
			doneWg.Add(1)

			go func() {
				defer doneWg.Done()
				launchWg.Wait()

				for i := 0; i < iterations; i++ {
					key := strconv.Itoa(i)
					item, ok := syncCache.GetItem(key)
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
		syncCache := gubernator.NewSyncLRUCache(0)
		defer syncCache.Close()
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
					syncCache.Add(item)
				}
			}()
		}

		// Wait for goroutines to finish.
		launchWg.Done()
		doneWg.Wait()
	})

	t.Run("Concurrent reads and writes", func(t *testing.T) {
		syncCache := gubernator.NewSyncLRUCache(0)
		defer syncCache.Close()
		expireAt := clock.Now().Add(1 * time.Hour).UnixMilli()

		// Populate cache.
		for i := 0; i < iterations; i++ {
			key := strconv.Itoa(i)
			item := gubernator.CacheItem{
				Key:      key,
				Value:    i,
				ExpireAt: expireAt,
			}
			exists := syncCache.Add(item)
			assert.False(t, exists)
		}

		assert.Equal(t, iterations, syncCache.Size())
		var launchWg, doneWg sync.WaitGroup
		launchWg.Add(1)

		for thread := 0; thread < concurrency; thread++ {
			doneWg.Add(2)

			go func() {
				defer doneWg.Done()
				launchWg.Wait()

				for i := 0; i < iterations; i++ {
					key := strconv.Itoa(i)
					item, ok := syncCache.GetItem(key)
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
					syncCache.Add(item)
				}
			}()
		}

		// Wait for goroutines to finish.
		launchWg.Done()
		doneWg.Wait()
	})
}

func BenchmarkSyncLRUCache(b *testing.B) {
	b.Run("Sequential reads", func(b *testing.B) {
		syncCache := gubernator.NewSyncLRUCache(0)
		defer syncCache.Close()
		expireAt := clock.Now().Add(1 * time.Hour).UnixMilli()

		// Populate cache.
		for i := 0; i < b.N; i++ {
			key := strconv.Itoa(i)
			item := gubernator.CacheItem{
				Key:      key,
				Value:    i,
				ExpireAt: expireAt,
			}
			exists := syncCache.Add(item)
			assert.False(b, exists)
		}

		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			key := strconv.Itoa(i)
			_, _ = syncCache.GetItem(key)
		}
	})

	b.Run("Sequential writes", func(b *testing.B) {
		syncCache := gubernator.NewSyncLRUCache(0)
		defer syncCache.Close()
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
			_ = syncCache.Add(item)
		}
	})

	b.Run("Concurrent reads", func(b *testing.B) {
		syncCache := gubernator.NewSyncLRUCache(0)
		defer syncCache.Close()
		expireAt := clock.Now().Add(1 * time.Hour).UnixMilli()

		// Populate cache.
		for i := 0; i < b.N; i++ {
			key := strconv.Itoa(i)
			item := gubernator.CacheItem{
				Key:      key,
				Value:    i,
				ExpireAt: expireAt,
			}
			exists := syncCache.Add(item)
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

				_, _ = syncCache.GetItem(key)
			}()
		}

		b.ReportAllocs()
		b.ResetTimer()
		launchWg.Done()
		doneWg.Wait()
	})

	b.Run("Concurrent writes", func(b *testing.B) {
		syncCache := gubernator.NewSyncLRUCache(0)
		defer syncCache.Close()
		expireAt := clock.Now().Add(1 * time.Hour).UnixMilli()
		var launchWg, doneWg sync.WaitGroup
		launchWg.Add(1)

		for i := 0; i < b.N; i++ {
			key := strconv.Itoa(i)
			doneWg.Add(1)

			go func() {
				defer doneWg.Done()
				launchWg.Wait()

				item := gubernator.CacheItem{
					Key:      key,
					Value:    i,
					ExpireAt: expireAt,
				}
				_ = syncCache.Add(item)
			}()
		}

		b.ReportAllocs()
		b.ResetTimer()
		launchWg.Done()
		doneWg.Wait()
	})

	b.Run("Concurrent reads and writes of existing keys", func(b *testing.B) {
		syncCache := gubernator.NewSyncLRUCache(0)
		defer syncCache.Close()
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
			exists := syncCache.Add(item)
			assert.False(b, exists)
		}

		for i := 0; i < b.N; i++ {
			key := strconv.Itoa(i)
			doneWg.Add(2)

			go func() {
				defer doneWg.Done()
				launchWg.Wait()

				_, _ = syncCache.GetItem(key)
			}()

			go func() {
				defer doneWg.Done()
				launchWg.Wait()

				item := gubernator.CacheItem{
					Key:      key,
					Value:    i,
					ExpireAt: expireAt,
				}
				_ = syncCache.Add(item)
			}()
		}

		b.ReportAllocs()
		b.ResetTimer()
		launchWg.Done()
		doneWg.Wait()
	})

	b.Run("Concurrent reads and writes of non-existent keys", func(b *testing.B) {
		syncCache := gubernator.NewSyncLRUCache(0)
		defer syncCache.Close()
		expireAt := clock.Now().Add(1 * time.Hour).UnixMilli()
		var launchWg, doneWg sync.WaitGroup
		launchWg.Add(1)

		for i := 0; i < b.N; i++ {
			doneWg.Add(2)

			go func() {
				defer doneWg.Done()
				launchWg.Wait()

				key := strconv.Itoa(i)
				_, _ = syncCache.GetItem(key)
			}()

			go func() {
				defer doneWg.Done()
				launchWg.Wait()

				key := "z" + strconv.Itoa(i)
				item := gubernator.CacheItem{
					Key:      key,
					Value:    i,
					ExpireAt: expireAt,
				}
				_ = syncCache.Add(item)
			}()
		}

		b.ReportAllocs()
		b.ResetTimer()
		launchWg.Done()
		doneWg.Wait()
	})
}
