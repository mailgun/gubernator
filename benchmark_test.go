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
	startCluster()
	defer stopCluster()

	// TODO: Make the PeerPicker take a list of PeerClients instead of PeerInfo
	// TODO: Get rid of the PeerInfo, This will allow us to remove a peer from the pool and
	// TODO: PeerClient will be responsible for closing the connection after the last connection
	// TODO: In flight is complete
	client := gubernator.NewPeerClient()
	peer := gubernator.PeerInfo{
		Host: peers[0],
	}

	b.Run("GetRateLimitByKey", func(b *testing.B) {
		for n := 0; n < b.N; n++ {
			_, err := client.GetRateLimitByKey(context.Background(), &peer, &pb.RateLimitKeyRequest_Entry{
				Key: randomBytes(10),
				RateLimit: &pb.RateLimitDuration{
					Requests: 10,
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
	startCluster()
	defer stopCluster()

	client, err := gubernator.NewClient("domain", peers)
	if err != nil {
		b.Errorf("NewClient err: %s - %s", err)
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
