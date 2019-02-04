package gubernator_test

import (
	"context"
	"github.com/mailgun/gubernator"
	"github.com/mailgun/gubernator/pb"
	"math/rand"
	"testing"
	"time"
)

func randomBytes(n int) []byte {
	const alphanum = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	var bytes = make([]byte, n)
	rand.Read(bytes)
	for i, b := range bytes {
		bytes[i] = alphanum[b%byte(len(alphanum))]
	}
	return bytes
}

func BenchmarkServer_GetRateLimitByKey(b *testing.B) {
	client := gubernator.NewPeerClient(peers[0])

	b.Run("GetRateLimitByKey", func(b *testing.B) {
		for n := 0; n < b.N; n++ {
			_, err := client.GetRateLimitByKey(context.Background(), &pb.RateLimitKeyRequest_Entry{
				Key: randomBytes(10),
				RateLimitConfig: &pb.RateLimitConfig{
					Limit:    10,
					Duration: 5,
				},
				Hits: 1,
			})
			if err != nil {
				b.Errorf("client.RateLimit() err: %s", err)
			}
		}
	})
}

func BenchmarkServer_GetRateLimit(b *testing.B) {
	client, err := gubernator.NewClient("domain", peers)
	if err != nil {
		b.Errorf("NewClient err: %s", err)
	}

	b.Run("GetRateLimit", func(b *testing.B) {
		for n := 0; n < b.N; n++ {
			_, err := client.GetRateLimit(context.Background(), &gubernator.Request{
				Descriptors: map[string]string{"key": string(randomBytes(10))},
				Limit:       10,
				Duration:    time.Second * 5,
				Hits:        1,
			})
			if err != nil {
				b.Errorf("client.RateLimit() err: %s", err)
			}
		}
	})
}
func BenchmarkServer_NoOp(b *testing.B) {
	client := gubernator.NewPeerClient(peers[0])

	//dur := time.Nanosecond * 117728
	//total := time.Second / dur
	//fmt.Printf("Total: %d\n", total)

	b.Run("NoOp", func(b *testing.B) {
		for n := 0; n < b.N; n++ {
			_, err := client.NoOp(context.Background(), &pb.NoOpRequest{})
			if err != nil {
				b.Errorf("client.NoOp() err: %s", err)
			}
		}
	})
}

// TODO: Benchmark with fanout to simulate thundering heard of simultaneous requests from many clients
