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

package gubernator

import (
	"net"
	"testing"

	"github.com/segmentio/fasthash/fnv1"
	"github.com/segmentio/fasthash/fnv1a"
	"github.com/stretchr/testify/assert"
)

func TestReplicatedConsistentHash(t *testing.T) {
	hosts := []string{"a.svc.local", "b.svc.local", "c.svc.local"}

	t.Run("Size", func(t *testing.T) {
		hash := NewReplicatedConsistentHash(nil, defaultReplicas)

		for _, h := range hosts {
			hash.Add(&PeerClient{conf: PeerConfig{Info: PeerInfo{GRPCAddress: h}}})
		}

		assert.Equal(t, len(hosts), hash.Size())
	})

	t.Run("Host", func(t *testing.T) {
		hash := NewReplicatedConsistentHash(nil, defaultReplicas)
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
			inHashFunc      HashString64
			outDistribution map[string]int
		}{{
			name: "default",
			outDistribution: map[string]int{
				"a.svc.local": 2948, "b.svc.local": 3592, "c.svc.local": 3460,
			},
		}, {
			name:       "fasthash/fnv1a",
			inHashFunc: fnv1a.HashString64,
			outDistribution: map[string]int{
				"a.svc.local": 3110, "b.svc.local": 3856, "c.svc.local": 3034,
			},
		}, {
			name:       "fasthash/fnv1",
			inHashFunc: fnv1.HashString64,
			outDistribution: map[string]int{
				"a.svc.local": 2948, "b.svc.local": 3592, "c.svc.local": 3460,
			},
		}} {
			t.Run(tc.name, func(t *testing.T) {
				hash := NewReplicatedConsistentHash(tc.inHashFunc, defaultReplicas)
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

func BenchmarkReplicatedConsistantHash(b *testing.B) {
	hashFuncs := map[string]HashString64{
		"fasthash/fnv1a": fnv1a.HashString64,
		"fasthash/fnv1":  fnv1.HashString64,
	}

	for name, hashFunc := range hashFuncs {
		b.Run(name, func(b *testing.B) {
			ips := make([]string, b.N)
			for i := range ips {
				ips[i] = net.IPv4(byte(i>>24), byte(i>>16), byte(i>>8), byte(i)).String()
			}

			hash := NewReplicatedConsistentHash(hashFunc, defaultReplicas)
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
