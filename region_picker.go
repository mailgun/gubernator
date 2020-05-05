package gubernator

import (
	"github.com/mailgun/holster/v3/syncutil"
)

type RegionPeerPicker interface {
	GetClients(string) ([]*PeerClient, error)
	GetByPeerInfo(PeerInfo) *PeerClient
	Add(*PeerClient)
	QueueHits(r *RateLimitReq)
	New() RegionPeerPicker
}

// RegionPicker encapsulates pickers for a set of regions
type RegionPicker struct {
	*ConsistentHash

	// A map of all the pickers by region
	regions map[string]PeerPicker
	// The implementation of picker we will use for each region
	conf     BehaviorConfig
	wg       syncutil.WaitGroup
	reqQueue chan *RateLimitReq
}

func NewRegionPicker(fn HashFunc) *RegionPicker {
	rp := &RegionPicker{
		regions:        make(map[string]PeerPicker),
		reqQueue:       make(chan *RateLimitReq, 0),
		ConsistentHash: NewConsistantHash(fn),
	}
	// TODO: Move this out of the picker
	rp.runAsyncReqs()
	return rp
}

func (rp *RegionPicker) New() RegionPeerPicker {
	hash := rp.ConsistentHash.New().(*ConsistentHash)
	return &RegionPicker{
		regions:        make(map[string]PeerPicker),
		reqQueue:       make(chan *RateLimitReq, 0),
		ConsistentHash: hash,
	}
}

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
		picker = rp.ConsistentHash.New()
		rp.regions[peer.info.DataCenter] = picker
	}
	picker.Add(peer)
}

// QueueHits writes the RateLimitReq to be asynchronously sent to other regions
func (rp *RegionPicker) QueueHits(r *RateLimitReq) {
	rp.reqQueue <- r
}

func (rp *RegionPicker) runAsyncReqs() {
	var interval = NewInterval(rp.conf.MultiRegionSyncWait)
	hits := make(map[string]*RateLimitReq)

	rp.wg.Until(func(done chan struct{}) bool {
		select {
		case r := <-rp.reqQueue:
			key := r.HashKey()

			// Aggregate the hits into a single request
			_, ok := hits[key]
			if ok {
				hits[key].Hits += r.Hits
			} else {
				hits[key] = r
			}

			// Send the hits if we reached our batch limit
			if len(hits) == rp.conf.MultiRegionBatchLimit {
				for dc, picker := range rp.regions {
					log.Infof("Sending %v hit(s) to %s picker", len(hits), dc)
					rp.sendHits(hits, picker)
				}
				hits = make(map[string]*RateLimitReq)
			}

			// Queue next interval
			if len(hits) == 1 {
				interval.Next()
			}

		case <-interval.C:
			if len(hits) > 0 {
				for dc, picker := range rp.regions {
					log.Infof("Sending %v hit(s) to %s picker", len(hits), dc)
					rp.sendHits(hits, picker)
				}
				hits = make(map[string]*RateLimitReq)
			}

		case <-done:
			return false
		}
		return true
	})
}

func (rp *RegionPicker) sendHits(r map[string]*RateLimitReq, picker PeerPicker) {
	// Does nothing for now
}
