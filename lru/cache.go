package lru

import (
	"container/list"
	"time"
)

/*
Modifications Copyright 2017 Mailgun Technologies Inc

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

// Holds stats collected about the cache
type LRUCacheStats struct {
	Size int64
	Miss int64
	Hit  int64
}

// Cache is an thread unsafe LRU cache that supports TTL expiration
type Cache struct {
	// MaxEntries is the maximum number of cache entries before
	// an item is evicted. Zero means no limit.
	MaxEntries int

	stats LRUCacheStats
	ll    *list.List
	cache map[interface{}]*list.Element
}

// A Key may be any value that is comparable. See http://golang.org/ref/spec#Comparison_operators
type Key interface{}

type cacheRecord struct {
	key      Key
	value    interface{}
	expireAt int64
}

// New creates a new Cache.
// If maxEntries is zero, the cache has no limit and it's assumed
// that eviction is done by the caller.
func NewLRUCache(maxEntries int) *Cache {
	return &Cache{
		MaxEntries: maxEntries,
		ll:         list.New(),
		cache:      make(map[interface{}]*list.Element),
	}
}

// Adds a value to the cache with an expiration
func (c *Cache) Add(key Key, value interface{}, expireAt int64) bool {
	return c.addRecord(&cacheRecord{
		key:      key,
		value:    value,
		expireAt: expireAt,
	})
}

// Adds a value to the cache.
func (c *Cache) addRecord(record *cacheRecord) bool {
	// If the key already exist, set the new value
	if ee, ok := c.cache[record.key]; ok {
		c.ll.MoveToFront(ee)
		temp := ee.Value.(*cacheRecord)
		*temp = *record
		return true
	}

	ele := c.ll.PushFront(record)
	c.cache[record.key] = ele
	if c.MaxEntries != 0 && c.ll.Len() > c.MaxEntries {
		c.removeOldest()
	}
	return false
}

// Return unix epoch in milliseconds
func MillisecondNow() int64 {
	return time.Now().UnixNano() / 1000000
}

// Get looks up a key's value from the cache.
func (c *Cache) Get(key Key) (value interface{}, ok bool) {

	if ele, hit := c.cache[key]; hit {
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
func (c *Cache) Remove(key Key) {
	if ele, hit := c.cache[key]; hit {
		c.removeElement(ele)
	}
}

// RemoveOldest removes the oldest item from the cache.
func (c *Cache) removeOldest() {
	ele := c.ll.Back()
	if ele != nil {
		c.removeElement(ele)
	}
}

func (c *Cache) removeElement(e *list.Element) {
	c.ll.Remove(e)
	kv := e.Value.(*cacheRecord)
	delete(c.cache, kv.key)
}

// Len returns the number of items in the cache.
func (c *Cache) Size() int {
	return c.ll.Len()
}

// Returns stats about the current state of the cache
func (c *Cache) Stats() LRUCacheStats {
	defer func() {
		c.stats = LRUCacheStats{}
	}()
	c.stats.Size = int64(len(c.cache))
	return c.stats
}

// Get a list of keys at this point in time
func (c *Cache) Keys() (keys []interface{}) {
	for key := range c.cache {
		keys = append(keys, key)
	}
	return
}

// Update the expiration time for the key
func (c *Cache) UpdateExpiration(key Key, expireAt int64) bool {
	if ele, hit := c.cache[key]; hit {
		entry := ele.Value.(*cacheRecord)
		entry.expireAt = expireAt
		return true
	}
	return false
}
