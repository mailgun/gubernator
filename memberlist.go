package gubernator

import (
	"fmt"
	"strings"
	"time"

	ml "github.com/hashicorp/memberlist"
	"github.com/pkg/errors"
)

const (
	delimiter = ","
)

type MemberlistPool struct {
	memberlist *ml.Memberlist
	conf       MemberlistPoolConfig
}

type MemberlistPoolConfig struct {
	AdvertiseAddress string
	AdvertisePort    int
	KnownNodes       []string
	DataCenter       string
	OnUpdate         UpdateFunc
}

func NewMemberlistPool(conf MemberlistPoolConfig) (*MemberlistPool, error) {
	memberlistPool := &MemberlistPool{conf: conf}

	// Configure memberlist event handler
	events := newMemberListEventHandler(conf.OnUpdate)

	config := ml.DefaultLANConfig()
	config.Events = events
	config.AdvertiseAddr = conf.AdvertiseAddress
	config.AdvertisePort = conf.AdvertisePort

	if conf.DataCenter != "" {
		config.Name = fmt.Sprintf("%s%s%s", config.Name, delimiter, conf.DataCenter)
	}

	// Create and set memberlist
	memberlist, err := ml.Create(config)
	if err != nil {
		return nil, err
	}
	memberlistPool.memberlist = memberlist

	// Join memberlist pool
	err = memberlistPool.joinPool(conf.KnownNodes)
	if err != nil {
		return nil, err
	}

	return memberlistPool, nil
}

func (m *MemberlistPool) joinPool(knownNodes []string) error {
	log.Infof("joining memberlist with nodes %s", knownNodes)
	_, err := m.memberlist.Join(knownNodes)
	log.Infof("found %v peer(s)", m.memberlist.NumMembers())
	log.Info(m.memberlist.Members())
	log.Info(errors.Wrap(err, "while joining memberlist"))

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
	address := node.Address()
	if e.delimitName(node.Name) {
		dataCenter := strings.Split(node.Name, delimiter)[1]
		e.peers[address] = PeerInfo{Address: address, DataCenter: dataCenter}
	} else {
		e.peers[address] = PeerInfo{Address: address}
	}

	e.callOnUpdate()
	log.Infof("notify join")
}

func (e *memberlistEventHandler) NotifyLeave(node *ml.Node) {
	address := node.Address()
	delete(e.peers, address)

	e.callOnUpdate()
	log.Infof("notify leave")
}

func (e *memberlistEventHandler) NotifyUpdate(node *ml.Node) {
	address := node.Address()
	if e.delimitName(node.Name) {
		dataCenter := strings.Split(node.Name, delimiter)[1]
		e.peers[address] = PeerInfo{Address: address, DataCenter: dataCenter}
	} else {
		e.peers[address] = PeerInfo{Address: address}
	}

	e.callOnUpdate()
	log.Infof("notify update")
}

func (e *memberlistEventHandler) delimitName(name string) bool {
	if strings.Split(name, delimiter)[0] == name {
		return false
	}
	return true
}

func (e *memberlistEventHandler) callOnUpdate() {
	var peers []PeerInfo

	log.Infof("gathering %v peer(s) for gubernator", len(e.peers))

	for _, p := range e.peers {
		peers = append(peers, p)
	}

	log.Info(peers)

	e.OnUpdate(peers)
}
