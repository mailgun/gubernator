package gubernator

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"io"
	l "log"
	"strconv"
	"strings"
	"time"

	ml "github.com/hashicorp/memberlist"
	"github.com/pkg/errors"
)

type MemberlistPool struct {
	memberlist *ml.Memberlist
	conf       MemberlistPoolConfig
	events     *memberlistEventHandler
}

type MemberlistPoolConfig struct {
	AdvertiseAddress string
	AdvertisePort    int
	KnownNodes       []string
	LoggerOutput     io.Writer
	Logger           *l.Logger
	DataCenter       string
	GubernatorPort   int
	OnUpdate         UpdateFunc
	Enabled          bool
}

func NewMemberlistPool(conf MemberlistPoolConfig) (*MemberlistPool, error) {
	memberlistPool := &MemberlistPool{conf: conf}

	// Configure memberlist event handler
	memberlistPool.events = newMemberListEventHandler(conf.OnUpdate)

	// Configure memberlist
	config := ml.DefaultWANConfig()
	config.Events = memberlistPool.events
	config.AdvertiseAddr = conf.AdvertiseAddress
	config.AdvertisePort = conf.AdvertisePort

	if conf.LoggerOutput != nil {
		config.LogOutput = conf.LoggerOutput
	}

	if conf.Logger != nil {
		config.Logger = conf.Logger
	}

	// Create and set memberlist
	memberlist, err := ml.Create(config)
	if err != nil {
		return nil, err
	}
	memberlistPool.memberlist = memberlist

	// Prep metadata
	gob.Register(memberlistMetadata{})
	metadata := memberlistMetadata{DataCenter: conf.DataCenter, GubernatorPort: conf.GubernatorPort}

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

	// Add the local node to the event handler's peer list
	m.events.addPeer(node)

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

func (e *memberlistEventHandler) addPeer(node *ml.Node) {
	ip := getIP(node.Address())

	// Deserialize metadata
	metadata, err := deserializeMemberlistMetadata(node.Meta)
	if err != nil {
		log.Warn(errors.Wrap(err, "while adding to peers"))
	} else {
		// Construct Gubernator address and create PeerInfo
		gubernatorAddress := makeAddress(ip, metadata.GubernatorPort)
		e.peers[ip] = PeerInfo{Address: gubernatorAddress, DataCenter: metadata.DataCenter}
		e.callOnUpdate()
	}
}

func (e *memberlistEventHandler) NotifyJoin(node *ml.Node) {
	ip := getIP(node.Address())

	// Deserialize metadata
	metadata, err := deserializeMemberlistMetadata(node.Meta)
	if err != nil {
		// This is called during memberlist initialization due to the fact that the local node
		// has no metadata yet
		log.Warn(errors.Wrap(err, "while joining memberlist"))
	} else {
		// Construct Gubernator address and create PeerInfo
		gubernatorAddress := makeAddress(ip, metadata.GubernatorPort)
		e.peers[ip] = PeerInfo{Address: gubernatorAddress, DataCenter: metadata.DataCenter}
		e.callOnUpdate()
	}
}

func (e *memberlistEventHandler) NotifyLeave(node *ml.Node) {
	ip := getIP(node.Address())

	// Remove PeerInfo
	delete(e.peers, ip)

	e.callOnUpdate()
}

func (e *memberlistEventHandler) NotifyUpdate(node *ml.Node) {
	ip := getIP(node.Address())

	// Deserialize metadata
	metadata, err := deserializeMemberlistMetadata(node.Meta)
	if err != nil {
		log.Warn(errors.Wrap(err, "while updating memberlist"))
	} else {
		// Construct Gubernator address and create PeerInfo
		gubernatorAddress := makeAddress(ip, metadata.GubernatorPort)
		e.peers[ip] = PeerInfo{Address: gubernatorAddress, DataCenter: metadata.DataCenter}
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

func getIP(address string) string {
	return strings.Split(address, ":")[0]
}

func makeAddress(ip string, port int) string {
	return fmt.Sprintf("%s:%s", ip, strconv.Itoa(port))
}

type memberlistMetadata struct {
	DataCenter     string
	GubernatorPort int
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
