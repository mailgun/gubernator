package gubernator

import (
	"net"
	"testing"

	"github.com/segmentio/fasthash/fnv1"
	"github.com/segmentio/fasthash/fnv1a"
	"github.com/stretchr/testify/assert"
)

func TestConsistentHash(t *testing.T) {
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
		strings := make([]string, 10000)
		for i := range strings {
			ip := net.IPv4(192, 168, byte(i>>8), byte(i))
			strings[i] = ip.String()
		}

		for _, tc := range []struct {
			name            string
			inHashFunc      HashFunc
			outDistribution map[string]int
		}{{
			name: "default",
			outDistribution: map[string]int{
				"a.svc.local": 1584, "b.svc.local": 5967, "c.svc.local": 2449,
			},
		}, {
			name:       "fasthash/fnv1a",
			inHashFunc: fnv1a.HashBytes32,
			outDistribution: map[string]int{
				"a.svc.local": 1335, "b.svc.local": 6769, "c.svc.local": 1896,
			},
		}, {
			name:       "fasthash/fnv1",
			inHashFunc: fnv1.HashBytes32,
			outDistribution: map[string]int{
				"a.svc.local": 1818, "b.svc.local": 2494, "c.svc.local": 5688,
			},
		}} {
			t.Run(tc.name, func(t *testing.T) {
				hash := NewConsistentHash(tc.inHashFunc)
				distribution := make(map[string]int)

				for _, h := range hosts {
					hash.Add(&PeerClient{conf: PeerConfig{Info: PeerInfo{GRPCAddress: h}}})
					distribution[h] = 0
				}

				for i := range strings {
					peer, _ := hash.Get(strings[i])
					distribution[peer.Info().GRPCAddress]++
				}
				assert.Equal(t, tc.outDistribution, distribution)
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
