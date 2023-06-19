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

package gubernator_test

import (
	"context"
	"fmt"
	"sort"
	"testing"

	guber "github.com/mailgun/gubernator/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGubernatorPool(t *testing.T) {
	ctx := context.Background()

	testCases := []struct {
		name    string
		workers int
	}{
		{"Single-threaded", 1},
		{"Multi-threaded", 4},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			// Setup mock data.
			const NumCacheItems = 100
			cacheItems := []*guber.CacheItem{}
			for i := 0; i < NumCacheItems; i++ {
				cacheItems = append(cacheItems, &guber.CacheItem{
					Key:      fmt.Sprintf("Foobar%04d", i),
					Value:    fmt.Sprintf("Stuff%04d", i),
					ExpireAt: 4131978658000,
				})
			}

			t.Run("Load()", func(t *testing.T) {
				mockLoader := &MockLoader2{}
				mockCache := &MockCache{}
				conf := &guber.Config{
					CacheFactory: func(maxSize int) guber.Cache {
						return mockCache
					},
					Loader:  mockLoader,
					Workers: testCase.workers,
				}
				conf.SetDefaults()
				chp := guber.NewWorkerPool(conf)

				// Mock Loader.
				fakeLoadCh := make(chan *guber.CacheItem, NumCacheItems)
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
				conf := &guber.Config{
					CacheFactory: func(maxSize int) guber.Cache {
						return mockCache
					},
					Loader:  mockLoader,
					Workers: testCase.workers,
				}
				require.NoError(t, conf.SetDefaults())
				chp := guber.NewWorkerPool(conf)

				// Mock Loader.
				mockLoader.On("Save", mock.Anything).Once().Return(nil).
					Run(func(args mock.Arguments) {
						// Verify items sent over the channel passed to Save().
						saveCh := args.Get(0).(chan *guber.CacheItem)
						savedItems := []*guber.CacheItem{}
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
				eachCh := make(chan *guber.CacheItem, NumCacheItems)
				for _, item := range cacheItems {
					eachCh <- item
				}
				close(eachCh)
				mockCache.On("Each").Times(testCase.workers).Return(eachCh)

				// Call code.
				err := chp.Store(ctx)

				// Verify.
				require.NoError(t, err, "Error in chp.Store")
			})
		})
	}
}
