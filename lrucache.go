/*
Modifications Copyright 2018-2021 Mailgun Technologies Inc

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
	"sync/atomic"

	"github.com/mailgun/holster/v4/clock"
	"github.com/mailgun/holster/v4/setter"
	"github.com/prometheus/client_golang/prometheus"
)

// Cache is an LRU cache that supports expiration.
// Not thread-safe.  Be sure to use a mutex to prevent concurrent method calls.
type LRUCache struct {
	cache     map[string]*list.Element
	mutex     sync.Mutex
	ll        *list.List
	cacheSize int
	cacheLen  int64
}

var _ Cache = &LRUCache{}

var sizeMetric = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "gubernator_cache_size",
	Help: "The number of items in LRU Cache which holds the rate limits.",
})
var accessMetric = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "gubernator_cache_access_count",
	Help: "Cache access counts.  Label \"type\" = hit|miss.",
}, []string{"type"})

// New creates a new Cache with a maximum size.
func NewLRUCache(maxSize int) *LRUCache {
	setter.SetDefault(&maxSize, 50_000)

	return &LRUCache{
		cache:     make(map[string]*list.Element),
		ll:        list.New(),
		cacheSize: maxSize,
	}
}

func (c *LRUCache) Lock() {
	c.mutex.Lock()
}

func (c *LRUCache) Unlock() {
	c.mutex.Unlock()
}

// FIXME: Not threadsafe.  Each() maintains a goroutine that iterates.
// Cannot safely access other Cache methods while iterating.
// It would be safer if this were done using an iterator or delegate pattern
// that doesn't require a goroutine.
// May need to reassess functional requirements.
func (c *LRUCache) Each() chan CacheItem {
	out := make(chan CacheItem)
	go func() {
		for _, ele := range c.cache {
			out <- ele.Value.(CacheItem)
		}
		close(out)
	}()
	return out
}

// Adds a value to the cache.
func (c *LRUCache) Add(item CacheItem) bool {
	// If the key already exist, set the new value
	if ee, ok := c.cache[item.Key]; ok {
		c.ll.MoveToFront(ee)
		ee.Value = item
		return true
	}

	ele := c.ll.PushFront(item)
	c.cache[item.Key] = ele
	if c.cacheSize != 0 && c.ll.Len() > c.cacheSize {
		c.removeOldest()
	}
	atomic.StoreInt64(&c.cacheLen, int64(c.ll.Len()))
	return false
}

// Return unix epoch in milliseconds
func MillisecondNow() int64 {
	return clock.Now().UnixNano() / 1000000
}

// GetItem returns the item stored in the cache
func (c *LRUCache) GetItem(key string) (item CacheItem, ok bool) {
	if ele, hit := c.cache[key]; hit {
		entry := ele.Value.(CacheItem)

		now := MillisecondNow()
		// If the entry is invalidated
		if entry.InvalidAt != 0 && entry.InvalidAt < now {
			c.removeElement(ele)
			accessMetric.WithLabelValues("miss").Add(1)
			return
		}

		// If the entry has expired, remove it from the cache
		if entry.ExpireAt < now {
			c.removeElement(ele)
			accessMetric.WithLabelValues("miss").Add(1)
			return
		}

		accessMetric.WithLabelValues("hit").Add(1)
		c.ll.MoveToFront(ele)
		return entry, true
	}

	accessMetric.WithLabelValues("miss").Add(1)
	return
}

// Remove removes the provided key from the cache.
func (c *LRUCache) Remove(key string) {
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
	kv := e.Value.(CacheItem)
	delete(c.cache, kv.Key)
	atomic.StoreInt64(&c.cacheLen, int64(c.ll.Len()))
}

// Len returns the number of items in the cache.
func (c *LRUCache) Size() int {
	return c.ll.Len()
}

// Update the expiration time for the key
func (c *LRUCache) UpdateExpiration(key string, expireAt int64) bool {
	if ele, hit := c.cache[key]; hit {
		entry := ele.Value.(CacheItem)
		entry.ExpireAt = expireAt
		return true
	}
	return false
}

// Describe fetches prometheus metrics to be registered
func (c *LRUCache) Describe(ch chan<- *prometheus.Desc) {
	sizeMetric.Describe(ch)
	accessMetric.Describe(ch)
}

// Collect fetches metric counts and gauges from the cache
func (c *LRUCache) Collect(ch chan<- prometheus.Metric) {
	sizeMetric.Set(float64(atomic.LoadInt64(&c.cacheLen)))
	sizeMetric.Collect(ch)
	accessMetric.Collect(ch)
}

func (c *LRUCache) Close() error {
	c.cache = nil
	c.ll = nil
	return nil
}
