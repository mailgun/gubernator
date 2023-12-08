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
	"crypto/md5"
	"fmt"
	"sort"
	"strconv"

	"github.com/pkg/errors"
	"github.com/segmentio/fasthash/fnv1"
)

type PeerPicker interface {
	GetByPeerInfo(PeerInfo) *Peer
	Peers() []*Peer
	Get(string) (*Peer, error)
	New() PeerPicker
	Add(*Peer)
}

const defaultReplicas = 512

type HashString64 func(data string) uint64

var defaultHashString64 HashString64 = fnv1.HashString64

// Implements PeerPicker
type ReplicatedConsistentHash struct {
	hashFunc HashString64
	peerKeys []peerInfo
	peers    map[string]*Peer
	replicas int
}

type peerInfo struct {
	hash uint64
	peer *Peer
}

func NewReplicatedConsistentHash(fn HashString64, replicas int) *ReplicatedConsistentHash {
	ch := &ReplicatedConsistentHash{
		hashFunc: fn,
		peers:    make(map[string]*Peer),
		replicas: replicas,
	}

	if ch.hashFunc == nil {
		ch.hashFunc = defaultHashString64
	}
	return ch
}

func (ch *ReplicatedConsistentHash) New() PeerPicker {
	return &ReplicatedConsistentHash{
		hashFunc: ch.hashFunc,
		peers:    make(map[string]*Peer),
		replicas: ch.replicas,
	}
}

func (ch *ReplicatedConsistentHash) Peers() []*Peer {
	var results []*Peer
	for _, v := range ch.peers {
		results = append(results, v)
	}
	return results
}

// Adds a peer to the hash
func (ch *ReplicatedConsistentHash) Add(peer *Peer) {
	ch.peers[peer.Info().HTTPAddress] = peer

	key := fmt.Sprintf("%x", md5.Sum([]byte(peer.Info().HTTPAddress)))
	for i := 0; i < ch.replicas; i++ {
		hash := ch.hashFunc(strconv.Itoa(i) + key)
		ch.peerKeys = append(ch.peerKeys, peerInfo{
			hash: hash,
			peer: peer,
		})
	}

	sort.Slice(ch.peerKeys, func(i, j int) bool { return ch.peerKeys[i].hash < ch.peerKeys[j].hash })
}

// Returns number of peers in the picker
func (ch *ReplicatedConsistentHash) Size() int {
	return len(ch.peers)
}

// Returns the peer by hostname
func (ch *ReplicatedConsistentHash) GetByPeerInfo(peer PeerInfo) *Peer {
	return ch.peers[peer.HTTPAddress]
}

// Given a key, return the peer that key is assigned too
func (ch *ReplicatedConsistentHash) Get(key string) (*Peer, error) {
	if ch.Size() == 0 {
		return nil, errors.New("unable to pick a peer; pool is empty")
	}
	hash := ch.hashFunc(key)

	// Binary search for appropriate peer
	idx := sort.Search(len(ch.peerKeys), func(i int) bool { return ch.peerKeys[i].hash >= hash })

	// Means we have cycled back to the first peer
	if idx == len(ch.peerKeys) {
		idx = 0
	}

	return ch.peerKeys[idx].peer, nil
}
