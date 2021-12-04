package gubernator

import (
	"fmt"
	"sort"

	"github.com/OneOfOne/xxhash"
	"github.com/mailgun/holster/v4/errors"
)

type HandlerPool struct {
	keys []chanInfo
}

type chanInfo struct {
	hash uint64
	ch   *chan request
}

// spawnHandlerPool create a new pool of threads to handle incoming rate limit requests
func spawnHandlerPool(conf Config, count int) *HandlerPool {
	cp := &HandlerPool{}

	for i := 0; i < count; i++ {
		// I picked an arbitrary 1k queue size, change later?
		ch := make(chan request, 1_000)
		// TODO: Move this into it's own function
		go func() {
			// TODO: Doing this causes the very first instance of cache to be thrown away....
			//   Perhaps conf.Cache could be a function that returns a new instance as configured according to the user.
			//   Or we introduce a new thing in the config `conf.InstantiateCache()` ?
			localCache := conf.Cache.New()
			for {
				select {
				case r, ok := <-ch:
					if !ok {
						//fmt.Printf("[GO] Not ok\n")
						return
					}
					var rl *RateLimitResp
					var err error

					switch r.request.Algorithm {
					case Algorithm_TOKEN_BUCKET:
						//fmt.Printf("[GO] Token Bucket\n")
						rl, err = tokenBucket(conf.Store, localCache, r.request)
					case Algorithm_LEAKY_BUCKET:
						rl, err = leakyBucket(conf.Store, localCache, r.request)
					default:
						err = errors.Errorf("invalid rate limit algorithm '%d'", r.request.Algorithm)
					}
					//fmt.Printf("[GO] Send Resp\n")
					r.resp <- &response{
						rl:  rl,
						err: err,
					}
				}
			}
		}()
		cp.add(&ch)
	}
	return cp
}

func (cp *HandlerPool) Handle(r *RateLimitReq) (*RateLimitResp, error) {
	ch := cp.get(r.UniqueKey)
	req := request{
		resp:    make(chan *response, 1),
		request: r,
	}

	//fmt.Printf("Send Req\n")
	// Send the request to the thread pool
	*ch <- req

	// TODO: Not sure if we should check a context here, if one of the go
	//  routines doesn't return, that is more likely a code error than a runtime error.
	resp := <-req.resp
	//fmt.Printf("Got Resp\n")
	return resp.rl, resp.err
}

// Close closed all handlers in the pool. User should only be called once we can guarantee
// no more requests to HandlerPool will be made.
func (cp *HandlerPool) Close() {
	// TODO: Close all the go routine channels, which should signal the routines to close
}

// add a channel tied to a go routine to the pool
func (cp *HandlerPool) add(ch *chan request) {
	key := fmt.Sprintf("%x", ch)
	hash := xxhash.ChecksumString64S(key, 0)
	cp.keys = append(cp.keys, chanInfo{
		hash: hash,
		ch:   ch,
	})
	sort.Slice(cp.keys, func(i, j int) bool { return cp.keys[i].hash < cp.keys[j].hash })
}

// get returns the  that key is assigned too
func (cp *HandlerPool) get(key string) *chan request {
	hash := xxhash.ChecksumString64S(key, 0)

	// Binary search for appropriate channel
	idx := sort.Search(len(cp.keys), func(i int) bool { return cp.keys[i].hash >= hash })

	// Means we have cycled back to the first
	if idx == len(cp.keys) {
		idx = 0
	}
	//fmt.Printf("Channel: %d\n", idx)
	return cp.keys[idx].ch
}
