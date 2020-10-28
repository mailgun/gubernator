/*
Copyright 2018-2019 Mailgun Technologies Inc

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
	"net"
	"testing"

	"github.com/mailgun/gubernator"
	"github.com/mailgun/holster/v3/clock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type v1Server struct {
	conf     gubernator.Config
	listener net.Listener
	srv      *gubernator.V1Instance
}

func (s *v1Server) Close() {
	s.conf.GRPCServer.GracefulStop()
	s.srv.Close()
}

// Start a single instance of V1Server with the provided config and listening address.
func newV1Server(t *testing.T, address string, conf gubernator.Config) *v1Server {
	t.Helper()
	conf.GRPCServer = grpc.NewServer()

	srv, err := gubernator.NewV1Instance(conf)
	require.NoError(t, err)

	listener, err := net.Listen("tcp", address)
	require.NoError(t, err)

	go func() {
		if err := conf.GRPCServer.Serve(listener); err != nil {
			fmt.Printf("while serving: %s\n", err)
		}
	}()

	srv.SetPeers([]gubernator.PeerInfo{{GRPCAddress: listener.Addr().String(), IsOwner: true}})

	ctx, cancel := context.WithTimeout(context.Background(), clock.Second*10)

	err = gubernator.WaitForConnect(ctx, []string{listener.Addr().String()})
	require.NoError(t, err)
	cancel()

	return &v1Server{
		conf:     conf,
		listener: listener,
		srv:      srv,
	}
}

func TestLoader(t *testing.T) {
	loader := gubernator.NewMockLoader()

	srv := newV1Server(t, "", gubernator.Config{
		Behaviors: gubernator.BehaviorConfig{
			GlobalSyncWait: clock.Millisecond * 50, // Suitable for testing but not production
			GlobalTimeout:  clock.Second,
		},
		Loader: loader,
	})

	// loader.Load() should have been called for gubernator startup
	assert.Equal(t, 1, loader.Called["Load()"])
	assert.Equal(t, 0, loader.Called["Save()"])

	client, err := gubernator.DialV1Server(srv.listener.Addr().String(), nil)
	assert.Nil(t, err)

	resp, err := client.GetRateLimits(context.Background(), &gubernator.GetRateLimitsReq{
		Requests: []*gubernator.RateLimitReq{
			{
				Name:      "test_over_limit",
				UniqueKey: "account:1234",
				Algorithm: gubernator.Algorithm_TOKEN_BUCKET,
				Duration:  gubernator.Second,
				Limit:     2,
				Hits:      1,
			},
		},
	})
	require.Nil(t, err)
	require.NotNil(t, resp)
	require.Equal(t, 1, len(resp.Responses))
	require.Equal(t, "", resp.Responses[0].Error)

	srv.Close()

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
	tests := []struct {
		name            string
		firstRemaining  int64
		firstStatus     gubernator.Status
		secondRemaining int64
		secondStatus    gubernator.Status
		algorithm       gubernator.Algorithm
		switchAlgorithm gubernator.Algorithm
		testCase        func(gubernator.RateLimitReq, *gubernator.MockStore)
	}{
		{
			name:            "Given there are no token bucket limits in the store",
			firstRemaining:  int64(9),
			firstStatus:     gubernator.Status_UNDER_LIMIT,
			secondRemaining: int64(8),
			secondStatus:    gubernator.Status_UNDER_LIMIT,
			algorithm:       gubernator.Algorithm_TOKEN_BUCKET,
			switchAlgorithm: gubernator.Algorithm_LEAKY_BUCKET,
			testCase:        func(req gubernator.RateLimitReq, store *gubernator.MockStore) {},
		},
		{
			name:            "Given the store contains a token bucket rate limit not in the guber cache",
			firstRemaining:  int64(0),
			firstStatus:     gubernator.Status_UNDER_LIMIT,
			secondRemaining: int64(0),
			secondStatus:    gubernator.Status_OVER_LIMIT,
			algorithm:       gubernator.Algorithm_TOKEN_BUCKET,
			switchAlgorithm: gubernator.Algorithm_LEAKY_BUCKET,
			testCase: func(req gubernator.RateLimitReq, store *gubernator.MockStore) {
				now := gubernator.MillisecondNow()
				// Expire 1 second from now
				expire := now + gubernator.Second
				store.CacheItems[req.HashKey()] = &gubernator.CacheItem{
					Algorithm: gubernator.Algorithm_TOKEN_BUCKET,
					ExpireAt:  expire,
					Key:       req.HashKey(),
					Value: &gubernator.TokenBucketItem{
						Limit:     req.Limit,
						Duration:  req.Duration,
						CreatedAt: now,
						Remaining: 1,
					},
				}
			},
		},
		{
			name:            "Given there are no leaky bucket limits in the store",
			firstRemaining:  int64(9),
			firstStatus:     gubernator.Status_UNDER_LIMIT,
			secondRemaining: int64(8),
			secondStatus:    gubernator.Status_UNDER_LIMIT,
			algorithm:       gubernator.Algorithm_LEAKY_BUCKET,
			switchAlgorithm: gubernator.Algorithm_TOKEN_BUCKET,
			testCase:        func(req gubernator.RateLimitReq, store *gubernator.MockStore) {},
		},
		{
			name:            "Given the store contains a leaky bucket rate limit not in the guber cache",
			firstRemaining:  int64(0),
			firstStatus:     gubernator.Status_UNDER_LIMIT,
			secondRemaining: int64(0),
			secondStatus:    gubernator.Status_OVER_LIMIT,
			algorithm:       gubernator.Algorithm_LEAKY_BUCKET,
			switchAlgorithm: gubernator.Algorithm_TOKEN_BUCKET,
			testCase: func(req gubernator.RateLimitReq, store *gubernator.MockStore) {
				// Expire 1 second from now
				expire := gubernator.MillisecondNow() + gubernator.Second
				store.CacheItems[req.HashKey()] = &gubernator.CacheItem{
					Algorithm: gubernator.Algorithm_LEAKY_BUCKET,
					ExpireAt:  expire,
					Key:       req.HashKey(),
					Value: &gubernator.LeakyBucketItem{
						UpdatedAt: gubernator.MillisecondNow(),
						Duration:  req.Duration,
						Limit:     req.Limit,
						Remaining: 1,
					},
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := gubernator.NewMockStore()

			srv := newV1Server(t, "", gubernator.Config{
				Behaviors: gubernator.BehaviorConfig{
					GlobalSyncWait: clock.Millisecond * 50, // Suitable for testing but not production
					GlobalTimeout:  clock.Second,
				},
				Store: store,
			})

			// No calls to store
			assert.Equal(t, 0, store.Called["OnChange()"])
			assert.Equal(t, 0, store.Called["Get()"])

			client, err := gubernator.DialV1Server(srv.listener.Addr().String(), nil)
			assert.Nil(t, err)

			req := gubernator.RateLimitReq{
				Name:      "test_over_limit",
				UniqueKey: "account:1234",
				Algorithm: tt.algorithm,
				Duration:  gubernator.Second,
				Limit:     10,
				Hits:      1,
			}

			tt.testCase(req, store)

			// This request for the rate limit should ask the store via Get() and then
			// tell the store about the change to the rate limit by calling OnChange()
			resp, err := client.GetRateLimits(context.Background(), &gubernator.GetRateLimitsReq{
				Requests: []*gubernator.RateLimitReq{&req},
			})
			require.Nil(t, err)
			require.NotNil(t, resp)
			require.Equal(t, 1, len(resp.Responses))
			require.Equal(t, "", resp.Responses[0].Error)
			assert.Equal(t, tt.firstRemaining, resp.Responses[0].Remaining)
			assert.Equal(t, int64(10), resp.Responses[0].Limit)
			assert.Equal(t, tt.firstStatus, resp.Responses[0].Status)

			// Should have called OnChange() and Get()
			assert.Equal(t, 1, store.Called["OnChange()"])
			assert.Equal(t, 1, store.Called["Get()"])

			// Should have updated the store
			assert.Equal(t, tt.firstRemaining, getRemaining(store.CacheItems[req.HashKey()]))

			// Next call should not call `Get()` but only `OnChange()`
			resp, err = client.GetRateLimits(context.Background(), &gubernator.GetRateLimitsReq{
				Requests: []*gubernator.RateLimitReq{&req},
			})
			require.Nil(t, err)
			require.NotNil(t, resp)
			assert.Equal(t, tt.secondRemaining, resp.Responses[0].Remaining)
			assert.Equal(t, int64(10), resp.Responses[0].Limit)
			assert.Equal(t, tt.secondStatus, resp.Responses[0].Status)

			// Should have called OnChange() not Get() since rate limit is in the cache
			assert.Equal(t, 2, store.Called["OnChange()"])
			assert.Equal(t, 1, store.Called["Get()"])

			// Should have updated the store
			assert.Equal(t, tt.secondRemaining, getRemaining(store.CacheItems[req.HashKey()]))

			// Should have called `Remove()` when algorithm changed
			req.Algorithm = tt.switchAlgorithm
			resp, err = client.GetRateLimits(context.Background(), &gubernator.GetRateLimitsReq{
				Requests: []*gubernator.RateLimitReq{&req},
			})
			require.Nil(t, err)
			require.NotNil(t, resp)
			assert.Equal(t, 1, store.Called["Remove()"])
			assert.Equal(t, 3, store.Called["OnChange()"])
			assert.Equal(t, 2, store.Called["Get()"])

			assert.Equal(t, tt.switchAlgorithm, store.CacheItems[req.HashKey()].Algorithm)
		})
	}
}

func getRemaining(item *gubernator.CacheItem) int64 {
	switch item.Algorithm {
	case gubernator.Algorithm_TOKEN_BUCKET:
		return item.Value.(*gubernator.TokenBucketItem).Remaining
	case gubernator.Algorithm_LEAKY_BUCKET:
		return item.Value.(*gubernator.LeakyBucketItem).Remaining
	}
	return 0
}
