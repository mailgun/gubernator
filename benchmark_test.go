package gubernator_test

import (
	"context"
	"github.com/mailgun/gubernator"
	"github.com/mailgun/gubernator/pb"
	"math/rand"
	"testing"
)

func randomString(n int, prefix string) string {
	const alphanum = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	var bytes = make([]byte, n)
	rand.Read(bytes)
	for i, b := range bytes {
		bytes[i] = alphanum[b%byte(len(alphanum))]
	}
	return prefix + string(bytes)
}

func BenchmarkServer_GetRateLimitByKey(b *testing.B) {

	var servers []*gubernator.Server
	var peers []string
	for i := 0; i < 6; i++ {
		srv, err := gubernator.NewServer("")
		if err != nil {
			b.Errorf("NewServer() err: %s", err)
		}
		peers = append(peers, srv.Address())
		go srv.Run()
		servers = append(servers, srv)
	}

	client, errs := gubernator.NewClient(peers)
	if errs != nil {
		for host, err := range errs {
			b.Logf("NewClient err: %s - %s", host, err)
		}
		b.Errorf("NewClient() had errors")
	}

	b.Run("GetRateLimitByKey", func(b *testing.B) {
		for n := 0; n < b.N; n++ {
			_, err := client.RateLimit(context.Background(), "", &pb.RateLimitDescriptor{
				Entries: []*pb.RateLimitDescriptor_Entry{
					{
						Key: randomString(10, "ID-"),
					},
				},
			})
			if err != nil {
				b.Errorf("client.RateLimit() err: %s", err)
			}
		}
	})

	// Stop all servers
	for _, srv := range servers {
		srv.Stop()
	}

}
