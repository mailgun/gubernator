package gubernator

import (
	"math/rand"
	"net"
	"testing"

	"github.com/mailgun/holster/v3/clock"
	"github.com/segmentio/fasthash/fnv1"
	"github.com/segmentio/fasthash/fnv1a"
	"github.com/stretchr/testify/assert"
)

func TestConsistantHash(t *testing.T) {
	hosts := []string{"a.svc.local", "b.svc.local", "c.svc.local"}

	t.Run("Add", func(t *testing.T) {
		cases := map[string]string{
			"a":                                    hosts[1],
			"foobar":                               hosts[0],
			"192.168.1.2":                          hosts[1],
			"5f46bb53-6c30-49dc-adb4-b7355058adb6": hosts[1],
		}
		hash := NewConsistentHash(nil)
		for _, h := range hosts {
			hash.Add(&PeerClient{conf: PeerConfig{Info: PeerInfo{GRPCAddress: h}}})
		}

		for input, addr := range cases {
			t.Run(input, func(t *testing.T) {
				peer, err := hash.Get(input)
				assert.Nil(t, err)
				assert.Equal(t, addr, peer.Info().GRPCAddress)
			})
		}

	})

	t.Run("Size", func(t *testing.T) {
		hash := NewConsistentHash(nil)

		for _, h := range hosts {
			hash.Add(&PeerClient{conf: PeerConfig{Info: PeerInfo{GRPCAddress: h}}})
		}

		assert.Equal(t, len(hosts), hash.Size())
	})

	t.Run("Host", func(t *testing.T) {
		hash := NewConsistentHash(nil)
		hostMap := map[string]*PeerClient{}

		for _, h := range hosts {
			peer := &PeerClient{conf: PeerConfig{Info: PeerInfo{GRPCAddress: h}}}
			hash.Add(peer)
			hostMap[h] = peer
		}

		for host, peer := range hostMap {
			assert.Equal(t, peer, hash.GetByPeerInfo(PeerInfo{GRPCAddress: host}))
		}
	})

	t.Run("distribution", func(t *testing.T) {
		const cases = 10000
		rand.Seed(clock.Now().Unix())

		strings := make([]string, cases)

		for i := 0; i < cases; i++ {
			r := rand.Int31()
			ip := net.IPv4(192, byte(r>>16), byte(r>>8), byte(r))
			strings[i] = ip.String()
		}

		hashFuncs := map[string]HashFunc{
			"fasthash/fnv1a": fnv1a.HashBytes32,
			"fasthash/fnv1":  fnv1.HashBytes32,
			"crc32":          nil,
		}

		for name, hashFunc := range hashFuncs {
			t.Run(name, func(t *testing.T) {
				hash := NewConsistentHash(hashFunc)
				hostMap := map[string]int{}

				for _, h := range hosts {
					hash.Add(&PeerClient{conf: PeerConfig{Info: PeerInfo{GRPCAddress: h}}})
					hostMap[h] = 0
				}

				for i := range strings {
					peer, _ := hash.Get(strings[i])
					hostMap[peer.Info().GRPCAddress]++
				}

				for host, a := range hostMap {
					t.Logf("host: %s, percent: %f", host, float64(a)/cases)
				}
			})
		}
	})

}

func BenchmarkConsistantHash(b *testing.B) {
	hashFuncs := map[string]HashFunc{
		"fasthash/fnv1a": fnv1a.HashBytes32,
		"fasthash/fnv1":  fnv1.HashBytes32,
		"crc32":          nil,
	}

	for name, hashFunc := range hashFuncs {
		b.Run(name, func(b *testing.B) {
			ips := make([]string, b.N)
			for i := range ips {
				ips[i] = net.IPv4(byte(i>>24), byte(i>>16), byte(i>>8), byte(i)).String()
			}

			hash := NewConsistentHash(hashFunc)
			hosts := []string{"a.svc.local", "b.svc.local", "c.svc.local"}
			for _, h := range hosts {
				hash.Add(&PeerClient{conf: PeerConfig{Info: PeerInfo{GRPCAddress: h}}})
			}

			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				hash.Get(ips[i])
			}
		})
	}
}
