package gubernator

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"strings"
	"time"

	ml "github.com/hashicorp/memberlist"
	"github.com/pkg/errors"
)

type MemberlistPool struct {
	memberlist *ml.Memberlist
	conf       MemberlistPoolConfig
}

type MemberlistPoolConfig struct {
	AdvertiseAddress        string
	AdvertisePort           int
	KnownNodes              []string
	DataCenter              string
	GubernatorListenAddress string
	OnUpdate                UpdateFunc
}

func NewMemberlistPool(conf MemberlistPoolConfig) (*MemberlistPool, error) {
	memberlistPool := &MemberlistPool{conf: conf}

	// Configure memberlist event handler
	events := newMemberListEventHandler(conf.OnUpdate)

	// Configure memberlist
	config := ml.DefaultWANConfig()
	config.Events = events
	config.AdvertiseAddr = conf.AdvertiseAddress
	config.AdvertisePort = conf.AdvertisePort

	// Create and set memberlist
	memberlist, err := ml.Create(config)
	if err != nil {
		return nil, err
	}
	memberlistPool.memberlist = memberlist

	// Prep metadata
	gob.Register(memberlistMetadata{})
	gubernatorPort := strings.Split(conf.GubernatorListenAddress, ":")[1]
	metadata := memberlistMetadata{DataCenter: conf.DataCenter, GubernatorPort: gubernatorPort}

	// Join memberlist pool
	err = memberlistPool.joinPool(conf.KnownNodes, metadata)
	if err != nil {
		return nil, err
	}

	return memberlistPool, nil
}

func (m *MemberlistPool) joinPool(knownNodes []string, metadata memberlistMetadata) error {
	// Get local node and set metadata
	node := m.memberlist.LocalNode()
	serializedMetadata, err := serializeMemberlistMetadata(metadata)
	if err != nil {
		return err
	}
	node.Meta = serializedMetadata

	// Join memberlist
	_, err = m.memberlist.Join(knownNodes)
	if err != nil {
		return errors.Wrap(err, "while joining memberlist")
	}

	return nil
}

func (m *MemberlistPool) Close() {
	err := m.memberlist.Leave(time.Second)
	if err != nil {
		log.Warn(errors.Wrap(err, "while leaving memberlist"))
	}
}

type memberlistEventHandler struct {
	peers    map[string]PeerInfo
	OnUpdate UpdateFunc
}

func newMemberListEventHandler(onUpdate UpdateFunc) *memberlistEventHandler {
	eventhandler := memberlistEventHandler{OnUpdate: onUpdate}
	eventhandler.peers = make(map[string]PeerInfo)
	return &eventhandler
}

func (e *memberlistEventHandler) NotifyJoin(node *ml.Node) {
	address := strings.Split(node.Address(), ":")[0]

	// Deserialize metadata
	metadata, err := deserializeMemberlistMetadata(node.Meta)
	if err != nil {
		// This is called during memberlist initialization due to the fact that the local node
		// has no metadata yet
		log.Warn(errors.Wrap(err, "while joining memberlist"))
	} else {
		// Construct Gubernator address and create PeerInfo
		gubernatorAddress := fmt.Sprintf("%s:%s", address, metadata.GubernatorPort)
		e.peers[address] = PeerInfo{Address: gubernatorAddress, DataCenter: metadata.DataCenter}
		e.callOnUpdate()
	}
}

func (e *memberlistEventHandler) NotifyLeave(node *ml.Node) {
	address := node.Address()

	// Remove PeerInfo
	delete(e.peers, address)

	e.callOnUpdate()
}

func (e *memberlistEventHandler) NotifyUpdate(node *ml.Node) {
	address := strings.Split(node.Address(), ":")[0]

	// Deserialize metadata
	metadata, err := deserializeMemberlistMetadata(node.Meta)
	if err != nil {
		log.Warn(errors.Wrap(err, "while updating memberlist"))
	} else {
		// Construct Gubernator address and create PeerInfo
		gubernatorAddress := fmt.Sprintf("%s:%s", address, metadata.GubernatorPort)
		e.peers[address] = PeerInfo{Address: gubernatorAddress, DataCenter: metadata.DataCenter}
		e.callOnUpdate()
	}
}

func (e *memberlistEventHandler) callOnUpdate() {
	var peers = []PeerInfo{}

	for _, p := range e.peers {
		peers = append(peers, p)
	}

	e.OnUpdate(peers)
}

type memberlistMetadata struct {
	DataCenter     string
	GubernatorPort string
}

func serializeMemberlistMetadata(metadata memberlistMetadata) ([]byte, error) {
	buf := bytes.Buffer{}
	encoder := gob.NewEncoder(&buf)

	err := encoder.Encode(metadata)
	if err != nil {
		log.Warn(errors.Wrap(err, "error encoding"))
		return nil, err
	}

	return buf.Bytes(), nil
}

func deserializeMemberlistMetadata(metadataAsByteSlice []byte) (*memberlistMetadata, error) {
	metadata := memberlistMetadata{}
	buf := bytes.Buffer{}

	buf.Write(metadataAsByteSlice)

	decoder := gob.NewDecoder(&buf)

	err := decoder.Decode(&metadata)
	if err != nil {
		log.Warn(errors.Wrap(err, "error decoding"))
		return nil, err
	}

	return &metadata, nil
}
