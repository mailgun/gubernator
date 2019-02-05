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
	"github.com/mailgun/gubernator/pb"
	"sync"
	"time"

	"github.com/mailgun/holster"
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
	MaxCacheSize int

	// The initial cache size. If not provided defaults to 30% of the max cache size.
	InitialCacheSize int
}

// Cache is an thread unsafe LRU cache that supports expiration
type LRUCache struct {
	cache     map[interface{}]*list.Element
	wg        holster.WaitGroup
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

	return &LRUCache{
		cacheSize: conf.InitialCacheSize,
		ll:        list.New(),
		cache:     make(map[interface{}]*list.Element),
	}
}

func (c *LRUCache) Start() error {
	// TODO: Allow resizing the cache on the fly depending on the number of cache
	// TODO: hits, so we don't use the MAX cache all the time

	tick := time.NewTicker(time.Second * 5)
	c.wg.Until(func(done chan struct{}) bool {
		select {
		case <-tick.C:
			c.mutex.Lock()
			// TODO: Perhaps use number of cache misses, as a percent of max cache size to determine needed size
			//stats := s.conf.cache.Stats(false)
			//stats.
			c.mutex.Unlock()
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
func (c *LRUCache) Remove(key Key) {
	if ele, hit := c.cache[key]; hit {
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
