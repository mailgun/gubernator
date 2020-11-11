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

package gubernator

import (
	"container/list"
	"sync"

	"github.com/mailgun/holster/v3/clock"
	"github.com/mailgun/holster/v3/setter"
	"github.com/prometheus/client_golang/prometheus"
)

// So algorithms can interface with different cache implementations
type Cache interface {
	// Access methods
	Add(*CacheItem) bool
	UpdateExpiration(key interface{}, expireAt int64) bool
	GetItem(key interface{}) (value *CacheItem, ok bool)
	Each() chan *CacheItem
	Remove(key interface{})

	// If the cache is exclusive, this will control access to the cache
	Unlock()
	Lock()
}

// Holds stats collected about the cache
type cachStats struct {
	Size int64
	Miss int64
	Hit  int64
}

// Cache is an thread unsafe LRU cache that supports expiration
type LRUCache struct {
	cache     map[interface{}]*list.Element
	mutex     sync.Mutex
	ll        *list.List
	stats     cachStats
	cacheSize int

	// Stats
	sizeMetric   *prometheus.Desc
	accessMetric *prometheus.Desc
}

type CacheItem struct {
	Algorithm Algorithm
	Key       string
	Value     interface{}

	// Timestamp when rate limit expires
	ExpireAt int64
	// Timestamp when the cache should invalidate this rate limit. This is useful when used in conjunction with
	// a persistent store to ensure our node has the most up to date info from the store. Ignored if set to `0`
	// It is set by the persistent store implementation to indicate when the node should query the persistent store
	// for the latest rate limit data.
	InvalidAt int64
}

var _ Cache = &LRUCache{}

// New creates a new Cache with a maximum size
func NewLRUCache(maxSize int) *LRUCache {
	setter.SetDefault(&maxSize, 50000)

	return &LRUCache{
		cache:     make(map[interface{}]*list.Element),
		ll:        list.New(),
		cacheSize: maxSize,
		sizeMetric: prometheus.NewDesc("gubernator_cache_size",
			"Size of the LRU Cache which holds the rate limits.", nil, nil),
		accessMetric: prometheus.NewDesc("gubernator_cache_access_count",
			"Cache access counts.", []string{"type"}, nil),
	}
}

func (c *LRUCache) Lock() {
	c.mutex.Lock()
}

func (c *LRUCache) Unlock() {
	c.mutex.Unlock()
}

func (c *LRUCache) Each() chan *CacheItem {
	out := make(chan *CacheItem)
	go func() {
		for _, ele := range c.cache {
			out <- ele.Value.(*CacheItem)
		}
		close(out)
	}()
	return out
}

// Adds a value to the cache.
func (c *LRUCache) Add(record *CacheItem) bool {
	// If the key already exist, set the new value
	if ee, ok := c.cache[record.Key]; ok {
		c.ll.MoveToFront(ee)
		temp := ee.Value.(*CacheItem)
		*temp = *record
		return true
	}

	ele := c.ll.PushFront(record)
	c.cache[record.Key] = ele
	if c.cacheSize != 0 && c.ll.Len() > c.cacheSize {
		c.removeOldest()
	}
	return false
}

// Return unix epoch in milliseconds
func MillisecondNow() int64 {
	return clock.Now().UnixNano() / 1000000
}

// GetItem returns the item stored in the cache
func (c *LRUCache) GetItem(key interface{}) (item *CacheItem, ok bool) {

	if ele, hit := c.cache[key]; hit {
		entry := ele.Value.(*CacheItem)

		now := MillisecondNow()
		// If the entry is invalidated
		if entry.InvalidAt != 0 && entry.InvalidAt < now {
			c.removeElement(ele)
			c.stats.Miss++
			return
		}

		// If the entry has expired, remove it from the cache
		if entry.ExpireAt < now {
			c.removeElement(ele)
			c.stats.Miss++
			return
		}
		c.stats.Hit++
		c.ll.MoveToFront(ele)
		return entry, true
	}
	c.stats.Miss++
	return
}

// Remove removes the provided key from the cache.
func (c *LRUCache) Remove(key interface{}) {
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
	kv := e.Value.(*CacheItem)
	delete(c.cache, kv.Key)
}

// Len returns the number of items in the cache.
func (c *LRUCache) Size() int {
	return c.ll.Len()
}

func (c *LRUCache) Stats(_ bool) cachStats {
	return c.stats
}

// Update the expiration time for the key
func (c *LRUCache) UpdateExpiration(key interface{}, expireAt int64) bool {
	if ele, hit := c.cache[key]; hit {
		entry := ele.Value.(*CacheItem)
		entry.ExpireAt = expireAt
		return true
	}
	return false
}

// Describe fetches prometheus metrics to be registered
func (c *LRUCache) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.sizeMetric
	ch <- c.accessMetric
}

// Collect fetches metric counts and gauges from the cache
func (c *LRUCache) Collect(ch chan<- prometheus.Metric) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	ch <- prometheus.MustNewConstMetric(c.accessMetric, prometheus.CounterValue, float64(c.stats.Hit), "hit")
	ch <- prometheus.MustNewConstMetric(c.accessMetric, prometheus.CounterValue, float64(c.stats.Miss), "miss")
	ch <- prometheus.MustNewConstMetric(c.sizeMetric, prometheus.GaugeValue, float64(len(c.cache)))
}
