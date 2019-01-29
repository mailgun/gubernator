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

// Adds a peer to the hash
func (ch *ConsistantHash) Add(peer *PeerClient) {
	hash := int(ch.hashFunc([]byte(peer.Host)))
	ch.peerKeys = append(ch.peerKeys, hash)
	ch.peerMap[hash] = peer
	sort.Ints(ch.peerKeys)
}

// Returns number of peers in the picker
func (ch *ConsistantHash) Size() int {
	return len(ch.peerKeys)
}

// Returns the peer by hostname
func (ch *ConsistantHash) GetPeer(host string) *PeerClient {
	return ch.peerMap[int(ch.hashFunc([]byte(host)))]
}

// Given a key, return the peer that key is assigned too
func (ch *ConsistantHash) Get(key []byte, peerInfo *PeerClient) error {
	if ch.Size() == 0 {
		return errors.New("unable to pick a peer; pool is empty")
	}

	hash := int(ch.hashFunc(key))

	// Binary search for appropriate peer
	idx := sort.Search(len(ch.peerKeys), func(i int) bool { return ch.peerKeys[i] >= hash })

	// Means we have cycled back to the first peer
	if idx == len(ch.peerKeys) {
		idx = 0
	}

	item := ch.peerMap[ch.peerKeys[idx]]
	*peerInfo = *item
	return nil
}
