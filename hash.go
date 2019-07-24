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

package gubernator

import (
	"github.com/pkg/errors"
	"hash/crc32"
	"sort"
)

type HashFunc func(data []byte) uint32

// Implements PeerPicker
type ConsistantHash struct {
	hashFunc HashFunc
	peerKeys []int
	peerMap  map[int]*PeerClient
}

func NewConsistantHash(fn HashFunc) *ConsistantHash {
	ch := &ConsistantHash{
		hashFunc: fn,
		peerMap:  make(map[int]*PeerClient),
	}

	if ch.hashFunc == nil {
		ch.hashFunc = crc32.ChecksumIEEE
	}
	return ch
}

func (ch *ConsistantHash) New() PeerPicker {
	return &ConsistantHash{
		hashFunc: ch.hashFunc,
		peerMap:  make(map[int]*PeerClient),
	}
}

func (ch *ConsistantHash) Peers() []*PeerClient {
	var results []*PeerClient
	for _, v := range ch.peerMap {
		results = append(results, v)
	}
	return results
}

// Adds a peer to the hash
func (ch *ConsistantHash) Add(peer *PeerClient) {
	hash := int(ch.hashFunc([]byte(peer.host)))
	ch.peerKeys = append(ch.peerKeys, hash)
	ch.peerMap[hash] = peer
	sort.Ints(ch.peerKeys)
}

// Returns number of peers in the picker
func (ch *ConsistantHash) Size() int {
	return len(ch.peerKeys)
}

// Returns the peer by hostname
func (ch *ConsistantHash) GetPeerByHost(host string) *PeerClient {
	return ch.peerMap[int(ch.hashFunc([]byte(host)))]
}

// Given a key, return the peer that key is assigned too
func (ch *ConsistantHash) Get(key string) (*PeerClient, error) {
	if ch.Size() == 0 {
		return nil, errors.New("unable to pick a peer; pool is empty")
	}

	hash := int(ch.hashFunc([]byte(key)))

	// Binary search for appropriate peer
	idx := sort.Search(len(ch.peerKeys), func(i int) bool { return ch.peerKeys[i] >= hash })

	// Means we have cycled back to the first peer
	if idx == len(ch.peerKeys) {
		idx = 0
	}

	return ch.peerMap[ch.peerKeys[idx]], nil
}
