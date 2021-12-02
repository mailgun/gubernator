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

func TestLRUCache(t *testing.T) {
	const iterations = 1000
	const concurrency = 100

	t.Run("Happy path", func(t *testing.T) {
       cache := gubernator.NewLRUCache(0)
       expireAt := clock.Now().Add(1 * time.Hour).UnixMilli()

       // Populate cache.
       for i := 0; i < iterations; i++ {
               key := strconv.Itoa(i)
               item := &gubernator.CacheItem{
                       Key: key,
                       Value: i,
                       ExpireAt: expireAt,
               }
			   cache.Lock()
               exists := cache.Add(item)
			   cache.Unlock()
               assert.False(t, exists)
       }

       // Validate cache.
       assert.Equal(t, iterations, cache.Size())

       for i := 0; i < iterations; i++ {
               key := strconv.Itoa(i)
			   cache.Lock()
               item, ok := cache.GetItem(key)
			   cache.Unlock()
               require.True(t, ok)
               require.NotNil(t, item)
               assert.Equal(t, item.Value, i)
       }

       // Clear cache.
       for i := 0; i < iterations; i++ {
               key := strconv.Itoa(i)
			   cache.Lock()
               cache.Remove(key)
			   cache.Unlock()
       }

       assert.Zero(t, cache.Size())
	})

	t.Run("Concurrent reads", func(t *testing.T) {
		cache := gubernator.NewLRUCache(0)
		expireAt := clock.Now().Add(1 * time.Hour).UnixMilli()

		// Populate cache.
		for i := 0; i < iterations; i++ {
			key := strconv.Itoa(i)
			item := &gubernator.CacheItem{
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
					cache.Lock()
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
					item := &gubernator.CacheItem{
						Key: key,
						Value: i,
						ExpireAt: expireAt,
					}
					cache.Lock()
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
		expireAt := clock.Now().Add(1 * time.Hour).UnixMilli()

		// Populate cache.
		for i := 0; i < iterations; i++ {
			key := strconv.Itoa(i)
			item := &gubernator.CacheItem{
				Key: key,
				Value: i,
				ExpireAt: expireAt,
			}
			cache.Lock()
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
					cache.Lock()
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
					item := &gubernator.CacheItem{
						Key: key,
						Value: i,
						ExpireAt: expireAt,
					}
					cache.Lock()
					cache.Add(item)
					cache.Unlock()
				}
			}()
		}

		// Wait for goroutines to finish.
		launchWg.Done()
		doneWg.Wait()
	})
}
