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
	"github.com/prometheus/common/log"
	"github.com/sirupsen/logrus"
)

type MemberListPool struct {
	log        *logrus.Entry
	memberList *ml.Memberlist
	conf       MemberListPoolConfig
	events     *memberListEventHandler
}

type MemberListPoolConfig struct {
	AdvertiseAddress string
	AdvertisePort    int
	NodeName         string
	KnownNodes       []string
	LoggerOutput     io.Writer
	Logger           *l.Logger
	DataCenter       string
	GubernatorPort   int
	OnUpdate         UpdateFunc
	Enabled          bool
}

func NewMemberListPool(conf MemberListPoolConfig) (*MemberListPool, error) {
	m := &MemberListPool{
		log:  logrus.WithField("category", "member-list"),
		conf: conf,
	}

	// Configure member list event handler
	m.events = newMemberListEventHandler(m.log, conf.OnUpdate)

	// Configure member list
	config := ml.DefaultWANConfig()
	config.Events = m.events
	config.AdvertiseAddr = conf.AdvertiseAddress
	config.AdvertisePort = conf.AdvertisePort

	if conf.NodeName != "" {
		config.Name = conf.NodeName
	}

	if conf.LoggerOutput != nil {
		config.LogOutput = conf.LoggerOutput
	}

	if conf.Logger != nil {
		config.Logger = conf.Logger
	}

	// Create and set member list
	memberlist, err := ml.Create(config)
	if err != nil {
		return nil, err
	}
	m.memberList = memberlist

	// Prep metadata
	gob.Register(memberListMetadata{})
	metadata := memberListMetadata{DataCenter: conf.DataCenter, GubernatorPort: conf.GubernatorPort}

	// Join member list pool
	err = m.joinPool(conf.KnownNodes, metadata)
	if err != nil {
		return nil, err
	}

	return m, nil
}

func (m *MemberListPool) joinPool(knownNodes []string, metadata memberListMetadata) error {
	// Get local node and set metadata
	node := m.memberList.LocalNode()
	serializedMetadata, err := serializeMemberListMetadata(metadata)
	if err != nil {
		return err
	}
	node.Meta = serializedMetadata

	// Join member list
	_, err = m.memberList.Join(knownNodes)
	if err != nil {
		return errors.Wrap(err, "while joining memberlist")
	}

	// Add the local node to the event handler's peer list
	m.events.addPeer(node)

	return nil
}

func (m *MemberListPool) Close() {
	err := m.memberList.Leave(time.Second)
	if err != nil {
		m.log.Warn(errors.Wrap(err, "while leaving memberlist"))
	}
}

type memberListEventHandler struct {
	peers    map[string]PeerInfo
	log      *logrus.Entry
	OnUpdate UpdateFunc
}

func newMemberListEventHandler(log *logrus.Entry, onUpdate UpdateFunc) *memberListEventHandler {
	handler := memberListEventHandler{
		OnUpdate: onUpdate,
		log:      log,
	}
	handler.peers = make(map[string]PeerInfo)
	return &handler
}

func (e *memberListEventHandler) addPeer(node *ml.Node) {
	ip := getIP(node.Address())

	// Deserialize metadata
	metadata, err := deserializeMemberListMetadata(node.Meta)
	if err != nil {
		e.log.Warn(errors.Wrap(err, "while adding to peers"))
	} else {
		// Construct Gubernator address and create PeerInfo
		gubernatorAddress := makeAddress(ip, metadata.GubernatorPort)
		e.peers[ip] = PeerInfo{GRPCAddress: gubernatorAddress, DataCenter: metadata.DataCenter}
		e.callOnUpdate()
	}
}

func (e *memberListEventHandler) NotifyJoin(node *ml.Node) {
	ip := getIP(node.Address())

	// Deserialize metadata
	metadata, err := deserializeMemberListMetadata(node.Meta)
	if err != nil {
		// This is called during memberlist initialization due to the fact that the local node
		// has no metadata yet
		log.Warn(errors.Wrap(err, "while joining memberlist"))
	} else {
		// Construct Gubernator address and create PeerInfo
		gubernatorAddress := makeAddress(ip, metadata.GubernatorPort)
		e.peers[ip] = PeerInfo{GRPCAddress: gubernatorAddress, DataCenter: metadata.DataCenter}
		e.callOnUpdate()
	}
}

func (e *memberListEventHandler) NotifyLeave(node *ml.Node) {
	ip := getIP(node.Address())

	// Remove PeerInfo
	delete(e.peers, ip)

	e.callOnUpdate()
}

func (e *memberListEventHandler) NotifyUpdate(node *ml.Node) {
	ip := getIP(node.Address())

	// Deserialize metadata
	metadata, err := deserializeMemberListMetadata(node.Meta)
	if err != nil {
		log.Warn(errors.Wrap(err, "while updating memberlist"))
	} else {
		// Construct Gubernator address and create PeerInfo
		gubernatorAddress := makeAddress(ip, metadata.GubernatorPort)
		e.peers[ip] = PeerInfo{GRPCAddress: gubernatorAddress, DataCenter: metadata.DataCenter}
		e.callOnUpdate()
	}
}

func (e *memberListEventHandler) callOnUpdate() {
	var peers []PeerInfo

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

type memberListMetadata struct {
	DataCenter     string
	GubernatorPort int
}

func serializeMemberListMetadata(metadata memberListMetadata) ([]byte, error) {
	buf := bytes.Buffer{}
	encoder := gob.NewEncoder(&buf)

	err := encoder.Encode(metadata)
	if err != nil {
		log.Warn(errors.Wrap(err, "error encoding"))
		return nil, err
	}

	return buf.Bytes(), nil
}

func deserializeMemberListMetadata(metadataAsByteSlice []byte) (*memberListMetadata, error) {
	metadata := memberListMetadata{}
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
