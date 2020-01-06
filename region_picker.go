package gubernator

type RegionPeerPicker interface {
	GetClients(string) ([]*PeerClient, error)
	GetByPeerInfo(PeerInfo) *PeerClient
	Add(*PeerClient)
}

// RegionPicker encapsulates pickers for a set of regions
type RegionPicker struct {
	// A map of all the pickers by region
	regions map[string]PeerPicker
	// The implementation of picker we will use for each region
	picker PeerPicker
}

func NewRegionPicker(picker PeerPicker) *RegionPicker {
	return &RegionPicker{
		regions: make(map[string]PeerPicker),
		picker:  picker,
	}
}

// TODO: Sending cross DC should mainly update the hits, the config should not be sent, or ignored when received
// TODO: Calculation of OVERLIMIT should not occur when sending hits cross DC

// GetClients returns all the PeerClients that match this key in all regions
func (mp *RegionPicker) GetClients(key string) ([]*PeerClient, error) {
	result := make([]*PeerClient, len(mp.regions))
	var i int
	for _, picker := range mp.regions {
		peer, err := picker.Get(key)
		if err != nil {
			return nil, err
		}
		result[i] = peer
		i++
	}
	return result, nil
}

// GetByPeerInfo returns the first PeerClient the PeerInfo.HasKey() matches
func (mp *RegionPicker) GetByPeerInfo(info PeerInfo) *PeerClient {
	for _, picker := range mp.regions {
		if client := picker.GetByPeerInfo(info); client != nil {
			return client
		}
	}
	return nil
}

func (mp *RegionPicker) Add(peer *PeerClient) {
	picker, ok := mp.regions[peer.info.DataCenter]
	if !ok {
		picker = mp.picker.New()
		mp.regions[peer.info.DataCenter] = picker
	}
	picker.Add(peer)
}
