/*
Modifications Copyright 2018 Mailgun Technologies Inc

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

This work is derived from github.com/golang/groupcache/lru
*/

package cache

import (
	"container/list"
	"sync"
	"time"

	"github.com/mailgun/gubernator/pb"
	"github.com/mailgun/holster"
	"github.com/mailgun/holster/clock"
	"github.com/sirupsen/logrus"
)

type LRUCacheConfig struct {
	// Max size of the internal cache, The actual size of the cache could change during normal
	// operation, but the cache will never exceed this number of rate limits in the cache.
	// Default: 50,000
	//
	// Cache Memory Usage
	// The size of the struct stored in the cache is 40 bytes, not including any additional metadata
	// that might be attached. The key which is formatted `domain_<key_value>_<key_value>` will also
	// effect the cache size.
	MaxCacheSize int `json:"max-cache-size"`

	// The initial cache size. If not provided defaults to 30% of the max cache size.
	InitialCacheSize int `json:"initial-cache-size"`

	// Interval at which the cache should check if it needs to shrink or grow
	InspectInterval clock.DurationJSON `json:"inspect-interval"`
}

// Cache is an thread unsafe LRU cache that supports expiration
type LRUCache struct {
	cache     map[interface{}]*list.Element
	wg        holster.WaitGroup
	conf      LRUCacheConfig
	log       *logrus.Entry
	mutex     sync.Mutex
	ll        *list.List
	stats     Stats
	cacheSize int
}

type cacheRecord struct {
	key      Key
	value    interface{}
	expireAt int64
}

// New creates a new Cache.
func NewLRUCache(conf LRUCacheConfig) *LRUCache {
	holster.SetDefault(&conf.MaxCacheSize, 50000)
	// If not provided init cache with 30 percent of the max cache size
	holster.SetDefault(&conf.InitialCacheSize, int(float32(conf.MaxCacheSize)*0.30))
	// Inspect the cache for possible resize every 30 seconds
	holster.SetDefault(&conf.InspectInterval.Duration, time.Second*30)

	return &LRUCache{
		log:       logrus.WithField("category", "lru-cache"),
		cache:     make(map[interface{}]*list.Element),
		cacheSize: conf.InitialCacheSize,
		conf:      conf,
		ll:        list.New(),
	}
}

// Inspect old entries at the bottom of the cache and decided if we
// should expand the size of the cache.
func (c *LRUCache) inspectAndResize() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Inspect the bottom 20% of the cache for expired items
	inspectSize := int(float32(c.cacheSize) * 0.20)
	ele := c.ll.Back()
	if ele == nil {
		// return if the cache is empty
		return
	}

	var prev *list.Element
	var count, expired = 0, 0
	for {
		if count == inspectSize || ele == nil {
			break
		}

		count++
		entry := ele.Value.(*cacheRecord)
		// Remove the entry if expired
		if entry.expireAt < MillisecondNow() {
			prev = ele.Prev()
			c.removeElement(ele)
			ele = prev
			expired++
			continue
		}
		ele = ele.Prev()
	}

	c.log.Debugf("Inspected cache [Size: %d, Cap: %d, Expired: %d, Inspected: %d]", c.Size(), c.cacheSize, expired, inspectSize)

	// If all the elements expired, we can shrink the cache size
	if expired == inspectSize {
		// Decrease the cache size by 30%
		newSize := c.cacheSize - int(float32(c.cacheSize)*0.30)
		// Don't shrink beyond the initial cache size
		if newSize < c.conf.InitialCacheSize {
			c.cacheSize = c.conf.InitialCacheSize
			return
		}
		c.log.Debugf("Shrinking cache from '%d' to '%d'", c.cacheSize, newSize)
		c.cacheSize = newSize
		return
	}

	// If less than 50% of the inspected elements expired
	if expired < int(float32(inspectSize)*0.50) {
		// Increase the cache size by 30%
		newSize := c.cacheSize + int(float32(c.cacheSize)*0.30)
		// Until we reach max size
		if newSize > c.conf.MaxCacheSize {
			c.cacheSize = c.conf.MaxCacheSize
			return
		}
		c.log.Debugf("Growing cache from '%d' to '%d'", c.cacheSize, newSize)
		c.cacheSize = newSize
		return
	}
}

func (c *LRUCache) Start() error {
	tick := time.NewTicker(c.conf.InspectInterval.Duration)
	c.wg.Until(func(done chan struct{}) bool {
		select {
		case <-tick.C:
			c.inspectAndResize()
			return true
		case <-done:
			tick.Stop()
			return false
		}
		return true
	})
	return nil
}

func (c *LRUCache) Stop() {
	c.wg.Stop()
}

func (c *LRUCache) Lock() {
	c.mutex.Lock()
}

func (c *LRUCache) Unlock() {
	c.mutex.Unlock()
}

// Adds a value to the cache with an expiration
func (c *LRUCache) Add(req *pb.RateLimitRequest, value interface{}, expireAt int64) bool {
	return c.addRecord(&cacheRecord{
		key:      req.Namespace + "_" + req.UniqueKey,
		value:    value,
		expireAt: expireAt,
	})
}

// Adds a value to the cache.
func (c *LRUCache) addRecord(record *cacheRecord) bool {
	// If the key already exist, set the new value
	if ee, ok := c.cache[record.key]; ok {
		c.ll.MoveToFront(ee)
		temp := ee.Value.(*cacheRecord)
		*temp = *record
		return true
	}

	ele := c.ll.PushFront(record)
	c.cache[record.key] = ele
	if c.cacheSize != 0 && c.ll.Len() > c.cacheSize {
		c.removeOldest()
	}
	return false
}

// Return unix epoch in milliseconds
func MillisecondNow() int64 {
	return time.Now().UnixNano() / 1000000
}

// Get looks up a key's value from the cache.
func (c *LRUCache) Get(req *pb.RateLimitRequest) (value interface{}, ok bool) {

	if ele, hit := c.cache[req.Namespace+"_"+req.UniqueKey]; hit {
		entry := ele.Value.(*cacheRecord)

		// If the entry has expired, remove it from the cache
		if entry.expireAt < MillisecondNow() {
			c.removeElement(ele)
			c.stats.Miss++
			return
		}
		c.stats.Hit++
		c.ll.MoveToFront(ele)
		return entry.value, true
	}
	c.stats.Miss++
	return
}

// Remove removes the provided key from the cache.
func (c *LRUCache) Remove(req *pb.RateLimitRequest) {
	if ele, hit := c.cache[req.Namespace+"_"+req.UniqueKey]; hit {
		c.removeElement(ele)
	}
}

// RemoveOldest removes the oldest item from the cache.
func (c *LRUCache) removeOldest() {
	ele := c.ll.Back()
	if ele != nil {
		c.removeElement(ele)
	}
}

func (c *LRUCache) removeElement(e *list.Element) {
	c.ll.Remove(e)
	kv := e.Value.(*cacheRecord)
	delete(c.cache, kv.key)
}

// Len returns the number of items in the cache.
func (c *LRUCache) Size() int {
	return c.ll.Len()
}

// Returns stats about the current state of the cache
func (c *LRUCache) Stats(clear bool) Stats {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if clear {
		defer func() {
			c.stats = Stats{}
		}()
	}
	c.stats.Size = int64(len(c.cache))
	return c.stats
}

// Update the expiration time for the key
func (c *LRUCache) UpdateExpiration(req *pb.RateLimitRequest, expireAt int64) bool {
	if ele, hit := c.cache[req.Namespace+"_"+req.UniqueKey]; hit {
		entry := ele.Value.(*cacheRecord)
		entry.expireAt = expireAt
		return true
	}
	return false
}
