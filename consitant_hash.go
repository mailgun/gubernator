package gubernator

import (
	"github.com/pkg/errors"
	"github.com/valyala/bytebufferpool"
	"hash/crc32"
	"sort"
)

type HashFunc func(data []byte) uint32

// Implements PeerPicker
type consistantHash struct {
	hashFunc HashFunc
	peerKeys []int
	peerMap  map[int]*PeerInfo
}

func newConsitantHashPicker(peers []*PeerInfo, fn HashFunc) *consistantHash {
	ch := &consistantHash{
		hashFunc: fn,
		peerMap:  make(map[int]*PeerInfo),
	}

	if ch.hashFunc == nil {
		ch.hashFunc = crc32.ChecksumIEEE
	}

	for _, peer := range peers {
		hash := int(ch.hashFunc([]byte(peer.HostName)))
		ch.peerKeys = append(ch.peerKeys, hash)
		ch.peerMap[hash] = peer
	}
	sort.Ints(ch.peerKeys)
	return ch
}

// Returns number of peers in the picker
func (ch *consistantHash) Size() int {
	return len(ch.peerKeys)
}

// Returns the peer by hostname
func (ch *consistantHash) GetPeer(host string) *PeerInfo {
	return ch.peerMap[int(ch.hashFunc([]byte(host)))]
}

// Given a key, return the peer that key is assigned too
func (ch *consistantHash) Get(key *bytebufferpool.ByteBuffer, peer *PeerInfo) error {
	if ch.Size() == 0 {
		return errors.New("unable to pick a peer; peer pool is empty")
	}

	hash := int(ch.hashFunc(key.Bytes()))

	// Binary search for appropriate peer
	idx := sort.Search(len(ch.peerKeys), func(i int) bool { return ch.peerKeys[i] >= hash })

	// Means we have cycled back to the first peer
	if idx == len(ch.peerKeys) {
		idx = 0
	}

	peer = ch.peerMap[ch.peerKeys[idx]]
	return nil
}
