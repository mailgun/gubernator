package gubernator_test

import (
	"strconv"
	"sync"
	"testing"
	"time"

	gubernator "github.com/mailgun/gubernator/v2"
	"github.com/mailgun/holster/v4/clock"
)

func BenchmarkCache(b *testing.B) {
	testCases := []struct {
		Name         string
		NewTestCache func() gubernator.Cache
		LockRequired bool
	}{
		{
			Name: "LRUCache",
			NewTestCache: func() gubernator.Cache {
				return gubernator.NewLRUCache(0)
			},
			LockRequired: true,
		},
	}

	for _, testCase := range testCases {
		b.Run(testCase.Name, func(b *testing.B) {
			b.Run("Sequential reads", func(b *testing.B) {
				cache := testCase.NewTestCache()
				expire := clock.Now().Add(time.Hour).UnixMilli()

				for i := 0; i < b.N; i++ {
					key := strconv.Itoa(i)
					item := &gubernator.CacheItem{
						Key:      key,
						Value:    i,
						ExpireAt: expire,
					}
					cache.Add(item)
				}

				b.ReportAllocs()
				b.ResetTimer()

				for i := 0; i < b.N; i++ {
					key := strconv.Itoa(i)
					_, _ = cache.GetItem(key)
				}
			})

			b.Run("Sequential writes", func(b *testing.B) {
				cache := testCase.NewTestCache()
				expire := clock.Now().Add(time.Hour).UnixMilli()

				b.ReportAllocs()
				b.ResetTimer()

				for i := 0; i < b.N; i++ {
					item := &gubernator.CacheItem{
						Key:      strconv.Itoa(i),
						Value:    i,
						ExpireAt: expire,
					}
					cache.Add(item)
				}
			})

			b.Run("Concurrent reads", func(b *testing.B) {
				cache := testCase.NewTestCache()
				expire := clock.Now().Add(time.Hour).UnixMilli()

				for i := 0; i < b.N; i++ {
					key := strconv.Itoa(i)
					item := &gubernator.CacheItem{
						Key:      key,
						Value:    i,
						ExpireAt: expire,
					}
					cache.Add(item)
				}

				var wg sync.WaitGroup
				var mutex sync.Mutex
				var task func(i int)

				if testCase.LockRequired {
					task = func(i int) {
						mutex.Lock()
						defer mutex.Unlock()
						key := strconv.Itoa(i)
						_, _ = cache.GetItem(key)
						wg.Done()
					}
				} else {
					task = func(i int) {
						key := strconv.Itoa(i)
						_, _ = cache.GetItem(key)
						wg.Done()
					}
				}

				b.ReportAllocs()
				b.ResetTimer()

				for i := 0; i < b.N; i++ {
					wg.Add(1)
					go task(i)
				}

				wg.Wait()
			})

			b.Run("Concurrent writes", func(b *testing.B) {
				cache := testCase.NewTestCache()
				expire := clock.Now().Add(time.Hour).UnixMilli()

				var wg sync.WaitGroup
				var mutex sync.Mutex
				var task func(i int)

				if testCase.LockRequired {
					task = func(i int) {
						mutex.Lock()
						defer mutex.Unlock()
						item := &gubernator.CacheItem{
							Key:      strconv.Itoa(i),
							Value:    i,
							ExpireAt: expire,
						}
						cache.Add(item)
						wg.Done()
					}
				} else {
					task = func(i int) {
						item := &gubernator.CacheItem{
							Key:      strconv.Itoa(i),
							Value:    i,
							ExpireAt: expire,
						}
						cache.Add(item)
						wg.Done()
					}
				}

				b.ReportAllocs()
				b.ResetTimer()

				for i := 0; i < b.N; i++ {
					wg.Add(1)
					go task(i)
				}

				wg.Wait()
			})

		})
	}
}
