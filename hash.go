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
	"reflect"
	"sort"
	"strconv"
	"unsafe"

	"github.com/pkg/errors"
	"github.com/segmentio/fasthash/fnv1"
)

const DefaultReplicas = 512

type HashFunc func(data []byte) uint64

var DefaultHash HashFunc = fnv1.HashBytes64

// Implements PeerPicker
type ConsistantHash struct {
	hashFunc HashFunc
	peerKeys []peerInfo
	peers    map[string]*PeerClient
	replicas int
}

type peerInfo struct {
	hash uint64
	peer *PeerClient
}

func NewConsistantHash(fn HashFunc) *ConsistantHash {
	ch := &ConsistantHash{
		hashFunc: fn,
		peers:    make(map[string]*PeerClient),
		replicas: DefaultReplicas,
	}

	if ch.hashFunc == nil {
		ch.hashFunc = DefaultHash
	}
	return ch
}

func (ch *ConsistantHash) New() PeerPicker {
	return &ConsistantHash{
		hashFunc: ch.hashFunc,
		peers:    make(map[string]*PeerClient),
		replicas: ch.replicas,
	}
}

func (ch *ConsistantHash) Peers() []*PeerClient {
	var results []*PeerClient
	for _, v := range ch.peers {
		results = append(results, v)
	}
	return results
}

// Adds a peer to the hash
func (ch *ConsistantHash) Add(peer *PeerClient) {
	ch.peers[peer.host] = peer

	for i := 0; i < ch.replicas; i++ {
		hash := ch.hashFunc(bytes(strconv.Itoa(i) + peer.host))
		ch.peerKeys = append(ch.peerKeys, peerInfo{
			hash: hash,
			peer: peer,
		})
	}

	sort.Slice(ch.peerKeys, func(i, j int) bool { return ch.peerKeys[i].hash < ch.peerKeys[j].hash })
}

// Returns number of peers in the picker
func (ch *ConsistantHash) Size() int {
	return len(ch.peers)
}

// Returns the peer by hostname
func (ch *ConsistantHash) GetPeerByHost(host string) *PeerClient {
	return ch.peers[host]
}

// Given a key, return the peer that key is assigned too
func (ch *ConsistantHash) Get(key string) (*PeerClient, error) {
	if ch.Size() == 0 {
		return nil, errors.New("unable to pick a peer; pool is empty")
	}

	hash := ch.hashFunc(bytes(key))

	// Binary search for appropriate peer
	idx := sort.Search(len(ch.peerKeys), func(i int) bool { return ch.peerKeys[i].hash >= hash })

	// Means we have cycled back to the first peer
	if idx == len(ch.peerKeys) {
		idx = 0
	}

	return ch.peerKeys[idx].peer, nil
}

// unsafely return the underlying bytes of a string
// the caller cannot alter the returned byte slice
func bytes(str string) []byte {
	hdr := *(*reflect.StringHeader)(unsafe.Pointer(&str))
	return *(*[]byte)(unsafe.Pointer(&reflect.SliceHeader{
		Data: hdr.Data,
		Len:  hdr.Len,
		Cap:  hdr.Len,
	}))
}
