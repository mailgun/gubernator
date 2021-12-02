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
	"context"
	"sync/atomic"
	"time"

	"github.com/mailgun/holster/v4/clock"
	"github.com/mailgun/holster/v4/setter"
	"github.com/prometheus/client_golang/prometheus"
)

// So algorithms can interface with different cache implementations
type Cache interface {
	// Access methods
	Add(item CacheItem) bool
	UpdateExpiration(key string, expireAt int64) bool
	GetItem(key string) (value CacheItem, ok bool)
	Each() chan CacheItem
	Remove(key string)

	// If the cache is exclusive, this will control access to the cache
	Unlock()
	Lock(ctx context.Context) error
}

// Cache is an thread unsafe LRU cache that supports expiration
type LRUCache struct {
	cache     map[string]*list.Element
	ll        *list.List
	cacheSize int

	LockCounter   int64
	UnlockCounter int64
	mutexCh       chan struct{}
}

type CacheItem struct {
	Algorithm Algorithm
	Key       string
	Value     interface{}

	// Timestamp when rate limit expires in epoch milliseconds.
	ExpireAt int64
	// Timestamp when the cache should invalidate this rate limit. This is useful when used in conjunction with
	// a persistent store to ensure our node has the most up to date info from the store. Ignored if set to `0`
	// It is set by the persistent store implementation to indicate when the node should query the persistent store
	// for the latest rate limit data.
	InvalidAt int64
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

// New creates a new Cache with a maximum size
func NewLRUCache(maxSize int) *LRUCache {
	setter.SetDefault(&maxSize, 50_000)
	mutexCh := make(chan struct{}, 1)
	mutexCh <- struct{}{}

	return &LRUCache{
		cache:     make(map[string]*list.Element),
		ll:        list.New(),
		cacheSize: maxSize,
		mutexCh:   mutexCh,
	}
}

func (c *LRUCache) Lock(ctx context.Context) error {
	atomic.AddInt64(&c.LockCounter, 1)
	select {
	case <-c.mutexCh:
		return nil
	case <-ctx.Done():
		atomic.AddInt64(&c.LockCounter, -1)
		return ctx.Err();
	}
}

func (c *LRUCache) Unlock() {
	atomic.AddInt64(&c.UnlockCounter, 1)
	c.mutexCh <- struct{}{}
}

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
	ctx, cancel := context.WithTimeout(context.Background(), 500 * time.Millisecond)
	defer cancel()
	err := c.Lock(ctx)
	if err != nil {
		return
	}
	defer c.Unlock()

	sizeMetric.Set(float64(len(c.cache)))
	sizeMetric.Collect(ch)
	accessMetric.Collect(ch)
}
