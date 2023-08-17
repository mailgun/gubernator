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
	"testing"

	"github.com/mailgun/gubernator/v3"
	"github.com/mailgun/holster/v4/clock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestLoader(t *testing.T) {
	loader := gubernator.NewMockLoader()

	d, err := gubernator.SpawnDaemon(context.Background(), gubernator.DaemonConfig{
		HTTPListenAddress: "localhost:0",
		Behaviors: gubernator.BehaviorConfig{
			// Suitable for testing but not production
			GlobalSyncWait: clock.Millisecond * 50, // Suitable for testing but not production
			GlobalTimeout:  clock.Second,
		},
		Loader: loader,
	})

	assert.NoError(t, err)
	conf := d.Config()
	d.SetPeers([]gubernator.PeerInfo{{HTTPAddress: conf.HTTPListenAddress, IsOwner: true}})

	// loader.Load() should have been called for gubernator startup
	assert.Equal(t, 1, loader.Called["Load()"])
	assert.Equal(t, 0, loader.Called["Save()"])

	client, err := gubernator.NewClient(gubernator.WithNoTLS(d.Listener.Addr().String()))
	assert.NoError(t, err)

	var resp gubernator.CheckRateLimitsResponse
	err = client.CheckRateLimits(context.Background(), &gubernator.CheckRateLimitsRequest{
		Requests: []*gubernator.RateLimitRequest{
			{
				Name:      "test_over_limit",
				UniqueKey: "account:1234",
				Algorithm: gubernator.Algorithm_TOKEN_BUCKET,
				Duration:  gubernator.Second,
				Limit:     2,
				Hits:      1,
			},
		},
	}, &resp)
	require.NoError(t, err)
	require.Equal(t, 1, len(resp.Responses))
	require.Equal(t, "", resp.Responses[0].Error)

	d.Close(context.Background())

	// Loader.Save() should been called during gubernator shutdown
	assert.Equal(t, 1, loader.Called["Load()"])
	assert.Equal(t, 1, loader.Called["Save()"])

	// Loader instance should have 1 rate limit
	require.Equal(t, 1, len(loader.CacheItems))
	item, ok := loader.CacheItems[0].Value.(*gubernator.TokenBucketItem)
	require.Equal(t, true, ok)
	assert.Equal(t, int64(2), item.Limit)
	assert.Equal(t, int64(1), item.Remaining)
	assert.Equal(t, gubernator.Status_UNDER_LIMIT, item.Status)
}

func TestStore(t *testing.T) {
	ctx := context.Background()
	setup := func() (*MockStore2, *gubernator.Daemon, gubernator.Client) {
		store := &MockStore2{}

		d, err := gubernator.SpawnDaemon(context.Background(), gubernator.DaemonConfig{
			HTTPListenAddress: "localhost:0",
			Behaviors: gubernator.BehaviorConfig{
				GlobalSyncWait: clock.Millisecond * 50,
				GlobalTimeout:  clock.Second,
			},
			Store: store,
		})
		assert.NoError(t, err)
		conf := d.Config()
		d.SetPeers([]gubernator.PeerInfo{{HTTPAddress: conf.HTTPListenAddress, IsOwner: true}})

		client, err := gubernator.NewClient(gubernator.WithNoTLS(d.Listener.Addr().String()))
		require.NoError(t, err)

		return store, d, client
	}

	tearDown := func(d *gubernator.Daemon) {
		d.Close(context.Background())
	}

	// Create a mock argument matcher for a request by name/key.
	matchReq := func(req *gubernator.RateLimitRequest) interface{} {
		return mock.MatchedBy(func(req2 *gubernator.RateLimitRequest) bool {
			return req2.Name == req.Name &&
				req2.UniqueKey == req.UniqueKey
		})
	}

	// Create a mock argument matcher for CacheItem input.
	// Verify item matches expected algorithm, limit, and duration.
	matchItem := func(req *gubernator.RateLimitRequest) interface{} {
		switch req.Algorithm {
		case gubernator.Algorithm_TOKEN_BUCKET:
			return mock.MatchedBy(func(item *gubernator.CacheItem) bool {
				titem, ok := item.Value.(*gubernator.TokenBucketItem)
				if !ok {
					return false
				}

				return item.Algorithm == req.Algorithm &&
					item.Key == req.HashKey() &&
					titem.Limit == req.Limit &&
					titem.Duration == req.Duration
			})

		case gubernator.Algorithm_LEAKY_BUCKET:
			return mock.MatchedBy(func(item *gubernator.CacheItem) bool {
				litem, ok := item.Value.(*gubernator.LeakyBucketItem)
				if !ok {
					return false
				}

				return item.Algorithm == req.Algorithm &&
					item.Key == req.HashKey() &&
					litem.Limit == req.Limit &&
					litem.Duration == req.Duration
			})

		default:
			assert.Fail(t, "Unknown algorithm")
			return nil
		}
	}

	// Create a bucket item matching the request.
	createBucketItem := func(req *gubernator.RateLimitRequest) interface{} {
		switch req.Algorithm {
		case gubernator.Algorithm_TOKEN_BUCKET:
			return &gubernator.TokenBucketItem{
				Limit:     req.Limit,
				Duration:  req.Duration,
				CreatedAt: gubernator.MillisecondNow(),
				Remaining: req.Limit,
			}

		case gubernator.Algorithm_LEAKY_BUCKET:
			return &gubernator.LeakyBucketItem{
				Limit:     req.Limit,
				Duration:  req.Duration,
				UpdatedAt: gubernator.MillisecondNow(),
			}

		default:
			assert.Fail(t, "Unknown algorithm")
			return nil
		}
	}

	testCases := []struct {
		Name      string
		Algorithm gubernator.Algorithm
	}{
		{"Token bucket", gubernator.Algorithm_TOKEN_BUCKET},
		{"Leaky bucket", gubernator.Algorithm_LEAKY_BUCKET},
	}

	for _, testCase := range testCases {
		t.Run(testCase.Name, func(t *testing.T) {
			t.Run("First rate check pulls from store", func(t *testing.T) {
				store, srv, client := setup()
				defer tearDown(srv)

				req := &gubernator.RateLimitRequest{
					Name:      "test_over_limit",
					UniqueKey: "account:1234",
					Algorithm: testCase.Algorithm,
					Duration:  gubernator.Second,
					Limit:     10,
					Hits:      1,
				}

				// Setup mocks.
				store.On("Get", mock.Anything, matchReq(req)).Once().Return(nil, false)
				store.On("OnChange", mock.Anything, matchReq(req), matchItem(req)).Once()

				// Call code.
				var resp gubernator.CheckRateLimitsResponse
				err := client.CheckRateLimits(ctx, &gubernator.CheckRateLimitsRequest{
					Requests: []*gubernator.RateLimitRequest{req},
				}, &resp)
				require.NoError(t, err)
				assert.Len(t, resp.Responses, 1)
				assert.Equal(t, "", resp.Responses[0].Error)
				assert.Equal(t, req.Limit, resp.Responses[0].Limit)
				assert.Equal(t, gubernator.Status_UNDER_LIMIT, resp.Responses[0].Status)
				store.AssertExpectations(t)

				t.Run("Second rate check pulls from cache", func(t *testing.T) {
					// Setup mocks.
					store.On("OnChange", mock.Anything, matchReq(req), matchItem(req)).Once()

					// Call code.
					var resp gubernator.CheckRateLimitsResponse
					err := client.CheckRateLimits(ctx, &gubernator.CheckRateLimitsRequest{
						Requests: []*gubernator.RateLimitRequest{req},
					}, &resp)
					require.NoError(t, err)
					assert.Len(t, resp.Responses, 1)
					assert.Equal(t, "", resp.Responses[0].Error)
					assert.Equal(t, req.Limit, resp.Responses[0].Limit)
					assert.Equal(t, gubernator.Status_UNDER_LIMIT, resp.Responses[0].Status)
					store.AssertExpectations(t)
				})
			})

			t.Run("Found in store after cache miss", func(t *testing.T) {
				store, srv, client := setup()
				defer tearDown(srv)

				req := &gubernator.RateLimitRequest{
					Name:      "test_over_limit",
					UniqueKey: "account:1234",
					Algorithm: testCase.Algorithm,
					Duration:  gubernator.Second,
					Limit:     10,
					Hits:      1,
				}

				// Setup mocks.
				now := gubernator.MillisecondNow()
				expire := now + req.Duration
				storedItem := &gubernator.CacheItem{
					Algorithm: req.Algorithm,
					ExpireAt:  expire,
					Key:       req.HashKey(),
					Value:     createBucketItem(req),
				}

				store.On("Get", mock.Anything, matchReq(req)).Once().Return(storedItem, true)
				store.On("OnChange", mock.Anything, matchReq(req), matchItem(req)).Once()

				// Call code.
				var resp gubernator.CheckRateLimitsResponse
				err := client.CheckRateLimits(ctx, &gubernator.CheckRateLimitsRequest{
					Requests: []*gubernator.RateLimitRequest{req},
				}, &resp)
				require.NoError(t, err)
				assert.Len(t, resp.Responses, 1)
				assert.Equal(t, "", resp.Responses[0].Error)
				assert.Equal(t, req.Limit, resp.Responses[0].Limit)
				assert.Equal(t, gubernator.Status_UNDER_LIMIT, resp.Responses[0].Status)
				store.AssertExpectations(t)
			})

			t.Run("Algorithm changed", func(t *testing.T) {
				// Removes stored item, then creates new.
				store, srv, client := setup()
				defer tearDown(srv)

				req := &gubernator.RateLimitRequest{
					Name:      "test_over_limit",
					UniqueKey: "account:1234",
					Algorithm: testCase.Algorithm,
					Duration:  gubernator.Second,
					Limit:     10,
					Hits:      1,
				}

				// Setup mocks.
				now := gubernator.MillisecondNow()
				expire := now + req.Duration
				storedItem := &gubernator.CacheItem{
					Algorithm: req.Algorithm,
					ExpireAt:  expire,
					Key:       req.HashKey(),
					Value:     &struct{}{},
				}

				store.On("Get", mock.Anything, matchReq(req)).Once().Return(storedItem, true)
				store.On("Remove", mock.Anything, req.HashKey()).Once()
				store.On("OnChange", mock.Anything, matchReq(req), matchItem(req)).Once()

				// Call code.
				var resp gubernator.CheckRateLimitsResponse
				err := client.CheckRateLimits(ctx, &gubernator.CheckRateLimitsRequest{
					Requests: []*gubernator.RateLimitRequest{req},
				}, &resp)
				require.NoError(t, err)
				assert.Len(t, resp.Responses, 1)
				assert.Equal(t, "", resp.Responses[0].Error)
				assert.Equal(t, req.Limit, resp.Responses[0].Limit)
				assert.Equal(t, gubernator.Status_UNDER_LIMIT, resp.Responses[0].Status)
				store.AssertExpectations(t)
			})

			// Discovered a bug where changing the duration of rate limit caused infinite recursion.
			// This test exercises that condition.  See PR #123.
			// Duration changed logic implemented only in token bucket.
			if testCase.Algorithm == gubernator.Algorithm_TOKEN_BUCKET {
				t.Run("Duration changed", func(t *testing.T) {
					// Updates expiration timestamp in store.
					store, srv, client := setup()
					defer tearDown(srv)

					oldDuration := int64(5000)
					newDuration := int64(8000)
					req := &gubernator.RateLimitRequest{
						Name:      "test_over_limit",
						UniqueKey: "account:1234",
						Algorithm: testCase.Algorithm,
						Duration:  newDuration,
						Limit:     10,
						Hits:      1,
					}

					// Setup mocks.
					now := gubernator.MillisecondNow()
					oldExpire := now + oldDuration
					bucketItem := createBucketItem(req)
					switch req.Algorithm {
					case gubernator.Algorithm_TOKEN_BUCKET:
						bucketItem.(*gubernator.TokenBucketItem).Duration = oldDuration

					case gubernator.Algorithm_LEAKY_BUCKET:
						bucketItem.(*gubernator.LeakyBucketItem).Duration = oldDuration
					}
					storedItem := &gubernator.CacheItem{
						Algorithm: req.Algorithm,
						ExpireAt:  oldExpire,
						Key:       req.HashKey(),
						Value:     bucketItem,
					}

					store.On("Get", mock.Anything, matchReq(req)).Once().Return(storedItem, true)

					store.On("OnChange",
						mock.Anything,
						matchReq(req),
						mock.MatchedBy(func(item *gubernator.CacheItem) bool {
							switch req.Algorithm {
							case gubernator.Algorithm_TOKEN_BUCKET:
								titem, ok := item.Value.(*gubernator.TokenBucketItem)
								if !ok {
									return false
								}

								return item.Algorithm == req.Algorithm &&
									item.Key == req.HashKey() &&
									item.ExpireAt == titem.CreatedAt+newDuration &&
									titem.Limit == req.Limit &&
									titem.Duration == req.Duration

							case gubernator.Algorithm_LEAKY_BUCKET:
								litem, ok := item.Value.(*gubernator.LeakyBucketItem)
								if !ok {
									return false
								}

								return item.Algorithm == req.Algorithm &&
									item.Key == req.HashKey() &&
									item.ExpireAt == litem.UpdatedAt+newDuration &&
									litem.Limit == req.Limit &&
									litem.Duration == req.Duration

							default:
								assert.Fail(t, "Unknown algorithm")
								return false
							}
						}),
					).
						Once()

					// Call code.
					var resp gubernator.CheckRateLimitsResponse
					err := client.CheckRateLimits(ctx, &gubernator.CheckRateLimitsRequest{
						Requests: []*gubernator.RateLimitRequest{req},
					}, &resp)
					require.NoError(t, err)
					assert.Len(t, resp.Responses, 1)
					assert.Equal(t, "", resp.Responses[0].Error)
					assert.Equal(t, req.Limit, resp.Responses[0].Limit)
					assert.Equal(t, gubernator.Status_UNDER_LIMIT, resp.Responses[0].Status)
					store.AssertExpectations(t)
				})

				t.Run("Duration changed and immediately expired", func(t *testing.T) {
					// Occurs when new duration is shorter and is immediately expired
					// because CreatedAt + NewDuration < Now.
					// Stores new item with renewed expiration and resets remaining.
					store, srv, client := setup()
					defer tearDown(srv)

					oldDuration := int64(500000)
					newDuration := int64(8000)
					req := &gubernator.RateLimitRequest{
						Name:      "test_over_limit",
						UniqueKey: "account:1234",
						Algorithm: testCase.Algorithm,
						Duration:  newDuration,
						Limit:     10,
						Hits:      1,
					}

					// Setup mocks.
					now := gubernator.MillisecondNow()
					longTimeAgo := now - 100000
					oldExpire := longTimeAgo + oldDuration
					bucketItem := createBucketItem(req)
					switch req.Algorithm {
					case gubernator.Algorithm_TOKEN_BUCKET:
						bucketItem.(*gubernator.TokenBucketItem).Duration = oldDuration
						bucketItem.(*gubernator.TokenBucketItem).CreatedAt = longTimeAgo

					case gubernator.Algorithm_LEAKY_BUCKET:
						bucketItem.(*gubernator.LeakyBucketItem).Duration = oldDuration
						bucketItem.(*gubernator.LeakyBucketItem).UpdatedAt = longTimeAgo
					}
					storedItem := &gubernator.CacheItem{
						Algorithm: req.Algorithm,
						ExpireAt:  oldExpire,
						Key:       req.HashKey(),
						Value:     bucketItem,
					}

					store.On("Get", mock.Anything, matchReq(req)).Once().Return(storedItem, true)

					store.On("OnChange",
						mock.Anything,
						matchReq(req),
						mock.MatchedBy(func(item *gubernator.CacheItem) bool {
							switch req.Algorithm {
							case gubernator.Algorithm_TOKEN_BUCKET:
								titem, ok := item.Value.(*gubernator.TokenBucketItem)
								if !ok {
									return false
								}

								return item.Algorithm == req.Algorithm &&
									item.Key == req.HashKey() &&
									item.ExpireAt == titem.CreatedAt+newDuration &&
									titem.Limit == req.Limit &&
									titem.Duration == req.Duration

							case gubernator.Algorithm_LEAKY_BUCKET:
								litem, ok := item.Value.(*gubernator.LeakyBucketItem)
								if !ok {
									return false
								}

								return item.Algorithm == req.Algorithm &&
									item.Key == req.HashKey() &&
									item.ExpireAt == litem.UpdatedAt+newDuration &&
									litem.Limit == req.Limit &&
									litem.Duration == req.Duration

							default:
								assert.Fail(t, "Unknown algorithm")
								return false
							}
						}),
					).
						Once()

					// Call code.
					var resp gubernator.CheckRateLimitsResponse
					err := client.CheckRateLimits(ctx, &gubernator.CheckRateLimitsRequest{
						Requests: []*gubernator.RateLimitRequest{req},
					}, &resp)
					require.NoError(t, err)
					assert.Len(t, resp.Responses, 1)
					assert.Equal(t, "", resp.Responses[0].Error)
					assert.Equal(t, req.Limit, resp.Responses[0].Limit)
					assert.Equal(t, gubernator.Status_UNDER_LIMIT, resp.Responses[0].Status)
					store.AssertExpectations(t)
				})
			}
		})
	}
}
