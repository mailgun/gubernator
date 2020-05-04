package gubernator

import (
	"github.com/mailgun/holster/v3/syncutil"
)

type RegionPeerPicker interface {
	GetClients(string) ([]*PeerClient, error)
	GetByPeerInfo(PeerInfo) *PeerClient
	Add(*PeerClient)
	QueueHits(r *RateLimitReq)
}

// RegionPicker encapsulates pickers for a set of regions
type RegionPicker struct {
	// A map of all the pickers by region
	regions map[string]PeerPicker
	// The implementation of picker we will use for each region
	picker   PeerPicker
	conf     BehaviorConfig
	wg       syncutil.WaitGroup
	reqQueue chan *RateLimitReq
}

func NewRegionPicker(picker PeerPicker) *RegionPicker {
	rp := &RegionPicker{
		regions:  make(map[string]PeerPicker),
		picker:   picker,
		reqQueue: make(chan *RateLimitReq, 0),
	}
	rp.runAsyncReqs()
	return rp
}

// TODO: Sending cross DC should mainly update the hits, the config should not be sent, or ignored when received
// TODO: Calculation of OVERLIMIT should not occur when sending hits cross DC

// GetClients returns all the PeerClients that match this key in all regions
func (rp *RegionPicker) GetClients(key string) ([]*PeerClient, error) {
	result := make([]*PeerClient, len(rp.regions))
	var i int
	for _, picker := range rp.regions {
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
func (rp *RegionPicker) GetByPeerInfo(info PeerInfo) *PeerClient {
	for _, picker := range rp.regions {
		if client := picker.GetByPeerInfo(info); client != nil {
			return client
		}
	}
	return nil
}

func (rp *RegionPicker) Add(peer *PeerClient) {
	picker, ok := rp.regions[peer.info.DataCenter]
	if !ok {
		picker = rp.picker.New()
		rp.regions[peer.info.DataCenter] = picker
	}
	picker.Add(peer)
}

// QueueHits writes the RateLimitReq to be asyncronously sent to other regions
func (mp *RegionPicker) QueueHits(r *RateLimitReq) {
	mp.reqQueue <- r
}

func (rp *RegionPicker) runAsyncReqs() {
	var interval = NewInterval(rp.conf.MultiRegionSyncWait)
	// Keys are data centers with a value that is the slice of requests to be sent
	hits := make([]*RateLimitReq, rp.conf.MultiRegionBatchLimit)

	rp.wg.Until(func(done chan struct{}) bool {
		select {
		case r := <-rp.reqQueue:
			hits = append(hits, r)
			if len(hits) == rp.conf.MultiRegionBatchLimit {
				for _, picker := range rp.regions {
					rp.sendHits(hits, picker)
				}
				hits = make([]*RateLimitReq, rp.conf.MultiRegionBatchLimit)
				return true
			}
		case <-interval.C:
			if len(hits) > 0 {
				for _, picker := range rp.regions {
					rp.sendHits(hits, picker)
				}
				hits = make([]*RateLimitReq, rp.conf.MultiRegionBatchLimit)
			}
		case <-done:
			return false
		}
		return true
	})
}

func (rp *RegionPicker) sendHits(r []*RateLimitReq, picker PeerPicker) {
	// Does nothing for now
	log.Infof("Sending %v hits to picker", len(r))
}
