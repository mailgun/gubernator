package gubernator

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGubernatorPool(t *testing.T) {
	ctx := context.Background()

	testCases := []struct {
		name        string
		concurrency int
	}{
		{"Single-threaded", 1},
		{"Multi-threaded", 4},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			// Setup mock data.
			const NumCacheItems = 100
			cacheItems := []CacheItem{}
			for i := 0; i < NumCacheItems; i++ {
				cacheItems = append(cacheItems, CacheItem{
					Key:      fmt.Sprintf("Foobar%04d", i),
					Value:    fmt.Sprintf("Stuff%04d", i),
					ExpireAt: 4131978658000,
				})
			}

			t.Run("Load()", func(t *testing.T) {
				mockLoader := &MockLoader2{}
				mockCache := &MockCache{}
				conf := &Config{
					CacheFactory: func() Cache {
						return mockCache
					},
					Loader: mockLoader,
				}
				conf.SetDefaults()
				chp := newGubernatorPool(conf, testCase.concurrency)

				// Mock Loader.
				fakeLoadCh := make(chan CacheItem, NumCacheItems)
				for _, item := range cacheItems {
					fakeLoadCh <- item
				}
				close(fakeLoadCh)
				mockLoader.On("Load").Once().Return(fakeLoadCh, nil)

				// Mock Cache.
				for _, item := range cacheItems {
					mockCache.On("Add", item).Once().Return(false)
				}

				// Call code.
				err := chp.Load(ctx)

				// Verify.
				require.NoError(t, err, "Error in chp.Load")
			})

			t.Run("Store()", func(t *testing.T) {
				mockLoader := &MockLoader2{}
				mockCache := &MockCache{}
				conf := &Config{
					CacheFactory: func() Cache {
						return mockCache
					},
					Loader: mockLoader,
					PoolWorkerHashRingRedundancy: 1,
				}
				conf.SetDefaults()
				chp := newGubernatorPool(conf, testCase.concurrency)

				// Mock Loader.
				mockLoader.On("Save", mock.Anything).Once().Return(nil).
					Run(func(args mock.Arguments) {
						// Verify items sent over the channel passed to Save().
						saveCh := args.Get(0).(chan CacheItem)
						savedItems := []CacheItem{}
						for item := range saveCh {
							savedItems = append(savedItems, item)
						}

						// Verify saved result.
						sort.Slice(savedItems, func(a, b int) bool {
							return savedItems[a].Key < savedItems[b].Key
						})
						assert.Equal(t, cacheItems, savedItems)
					})

				// Mock Cache.
				eachCh := make(chan CacheItem, NumCacheItems)
				for _, item := range cacheItems {
					eachCh <- item
				}
				close(eachCh)
				mockCache.On("Each").Times(testCase.concurrency).Return(eachCh)

				// Call code.
				err := chp.Store(ctx)

				// Verify.
				require.NoError(t, err, "Error in chp.Store")
			})
		})
	}
}
