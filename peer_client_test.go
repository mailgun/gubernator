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
	"runtime"
	"strings"
	"testing"

	gubernator "github.com/mailgun/gubernator/v2"
	"github.com/mailgun/gubernator/v2/cluster"
	"github.com/mailgun/holster/v4/clock"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestPeerClientShutdown(t *testing.T) {
	type test struct {
		Name     string
		Behavior gubernator.Behavior
	}

	const threads = 10
	requestTime := epochMillis(clock.Now())

	cases := []test{
		{"No batching", gubernator.Behavior_NO_BATCHING},
		{"Batching", gubernator.Behavior_BATCHING},
		{"Global", gubernator.Behavior_GLOBAL},
	}

	config := gubernator.BehaviorConfig{
		BatchTimeout: 250 * clock.Millisecond,
		BatchWait:    250 * clock.Millisecond,
		BatchLimit:   100,

		GlobalSyncWait:   250 * clock.Millisecond,
		GlobalTimeout:    250 * clock.Millisecond,
		GlobalBatchLimit: 100,
	}

	for i := range cases {
		c := cases[i]

		t.Run(c.Name, func(t *testing.T) {
			client, err := gubernator.NewPeerClient(gubernator.PeerConfig{
				Info:     cluster.GetRandomPeer(cluster.DataCenterNone),
				Behavior: config,
			})
			require.NoError(t, err)

			wg := errgroup.Group{}
			wg.SetLimit(threads)
			// Spawn a whole bunch of concurrent requests to test shutdown in various states
			for j := 0; j < threads; j++ {
				wg.Go(func() error {
					ctx := context.Background()
					_, err := client.GetPeerRateLimit(ctx, &gubernator.RateLimitReq{
						Hits:        1,
						Limit:       100,
						Behavior:    c.Behavior,
						RequestTime: &requestTime,
					})

					if err != nil {
						if !strings.Contains(err.Error(), "client connection is closing") {
							return errors.Wrap(err, "unexpected error in test")
						}
					}
					return nil
				})
			}

			// yield the processor that way we allow other goroutines to start their request
			runtime.Gosched()

			shutDownErr := client.Shutdown(context.Background())

			err = wg.Wait()
			if err != nil {
				t.Error(err)
				t.Fail()
			}
			require.NoError(t, shutDownErr)
		})
	}
}
