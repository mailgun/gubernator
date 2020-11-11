package gubernator_test

import (
	"context"
	"runtime"
	"sync"
	"testing"

	"github.com/mailgun/gubernator"
	"github.com/mailgun/gubernator/cluster"
	"github.com/mailgun/holster/v3/clock"
	"github.com/stretchr/testify/assert"
)

func TestPeerClientShutdown(t *testing.T) {
	type test struct {
		Name     string
		Behavior gubernator.Behavior
	}

	const threads = 10

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
			client := gubernator.NewPeerClient(gubernator.PeerConfig{
				Info:     cluster.GetRandomPeer(),
				Behavior: config,
			})

			wg := sync.WaitGroup{}
			wg.Add(threads)
			// Spawn a whole bunch of concurrent requests to test shutdown in various states
			for i := 0; i < threads; i++ {
				go func() {
					defer wg.Done()
					ctx := context.Background()
					_, err := client.GetPeerRateLimit(ctx, &gubernator.RateLimitReq{
						Hits:     1,
						Limit:    100,
						Behavior: c.Behavior,
					})

					isExpectedErr := false

					switch err.(type) {
					case *gubernator.PeerErr:
						isExpectedErr = true
					case nil:
						isExpectedErr = true
					}

					assert.True(t, true, isExpectedErr)

				}()
			}

			// yield the processor that way we allow other goroutines to start their request
			runtime.Gosched()

			err := client.Shutdown(context.Background())
			assert.NoError(t, err)

			wg.Wait()
		})

	}
}
