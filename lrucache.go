/*
Modifications Copyright 2018-2022 Mailgun Technologies Inc

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
	"sync/atomic"

	"github.com/mailgun/holster/v4/clock"
	"github.com/mailgun/holster/v4/setter"
	"github.com/prometheus/client_golang/prometheus"
)

// LRUCache is an LRU cache that supports expiration and is not thread-safe
// Be sure to use a mutex to prevent concurrent method calls.
type LRUCache struct {
	cache     map[string]*list.Element
	ll        *list.List
	cacheSize int
	cacheLen  int64
}

// LRUCacheCollector provides prometheus metrics collector for LRUCache.
// Register only one collector, add one or more caches to this collector.
type LRUCacheCollector struct {
	caches []Cache
}

var _ Cache = &LRUCache{}
var _ prometheus.Collector = &LRUCacheCollector{}

var metricCacheSize = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "gubernator_cache_size",
	Help: "The number of items in LRU Cache which holds the rate limits.",
})
var metricCacheAccess = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "gubernator_cache_access_count",
	Help: "Cache access counts.  Label \"type\" = hit|miss.",
}, []string{"type"})
var metricCacheUnexpiredEvictions = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "gubernator_unexpired_evictions_count",
	Help: "Count the number of cache items which were evicted while unexpired.",
})

// NewLRUCache creates a new Cache with a maximum size.
func NewLRUCache(maxSize int) *LRUCache {
	setter.SetDefault(&maxSize, 50_000)

	return &LRUCache{
		cache:     make(map[string]*list.Element),
		ll:        list.New(),
		cacheSize: maxSize,
	}
}

// Each is not thread-safe. Each() maintains a goroutine that iterates.
// Other go routines cannot safely access the Cache while iterating.
// It would be safer if this were done using an iterator or delegate pattern
// that doesn't require a goroutine. May need to reassess functional requirements.
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

// Add adds a value to the cache.
func (c *LRUCache) Add(item *CacheItem) bool {
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

// MillisecondNow returns unix epoch in milliseconds
func MillisecondNow() int64 {
	return clock.Now().UnixNano() / 1000000
}

// GetItem returns the item stored in the cache
func (c *LRUCache) GetItem(key string) (item *CacheItem, ok bool) {
	if ele, hit := c.cache[key]; hit {
		entry := ele.Value.(*CacheItem)

		now := MillisecondNow()
		// If the entry is invalidated
		if entry.InvalidAt != 0 && entry.InvalidAt < now {
			c.removeElement(ele)
			metricCacheAccess.WithLabelValues("miss").Add(1)
			return
		}

		// If the entry has expired, remove it from the cache
		if entry.ExpireAt < now {
			c.removeElement(ele)
			metricCacheAccess.WithLabelValues("miss").Add(1)
			return
		}

		metricCacheAccess.WithLabelValues("hit").Add(1)
		c.ll.MoveToFront(ele)
		return entry, true
	}

	metricCacheAccess.WithLabelValues("miss").Add(1)
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
		entry := ele.Value.(*CacheItem)

		if MillisecondNow() < entry.ExpireAt {
			metricCacheUnexpiredEvictions.Add(1)
		}

		c.removeElement(ele)
	}
}

func (c *LRUCache) removeElement(e *list.Element) {
	c.ll.Remove(e)
	kv := e.Value.(*CacheItem)
	delete(c.cache, kv.Key)
	atomic.StoreInt64(&c.cacheLen, int64(c.ll.Len()))
}

// Size returns the number of items in the cache.
func (c *LRUCache) Size() int64 {
	return atomic.LoadInt64(&c.cacheLen)
}

// UpdateExpiration updates the expiration time for the key
func (c *LRUCache) UpdateExpiration(key string, expireAt int64) bool {
	if ele, hit := c.cache[key]; hit {
		entry := ele.Value.(*CacheItem)
		entry.ExpireAt = expireAt
		return true
	}
	return false
}

func (c *LRUCache) Close() error {
	c.cache = nil
	c.ll = nil
	c.cacheLen = 0
	return nil
}

func NewLRUCacheCollector() *LRUCacheCollector {
	return &LRUCacheCollector{
		caches: []Cache{},
	}
}

// AddCache adds a Cache object to be tracked by the collector.
func (collector *LRUCacheCollector) AddCache(cache Cache) {
	collector.caches = append(collector.caches, cache)
}

// Describe fetches prometheus metrics to be registered
func (collector *LRUCacheCollector) Describe(ch chan<- *prometheus.Desc) {
	metricCacheSize.Describe(ch)
	metricCacheAccess.Describe(ch)
	metricCacheUnexpiredEvictions.Describe(ch)
}

// Collect fetches metric counts and gauges from the cache
func (collector *LRUCacheCollector) Collect(ch chan<- prometheus.Metric) {
	metricCacheSize.Set(collector.getSize())
	metricCacheSize.Collect(ch)
	metricCacheAccess.Collect(ch)
	metricCacheUnexpiredEvictions.Collect(ch)
}

func (collector *LRUCacheCollector) getSize() float64 {
	var size float64

	for _, cache := range collector.caches {
		size += float64(cache.Size())
	}

	return size
}
