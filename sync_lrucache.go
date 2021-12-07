/*
Copyright 2018-2021 Mailgun Technologies Inc

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package gubernator

import "github.com/prometheus/client_golang/prometheus"

type SyncLRUCache struct {
	cache                    *LRUCache
	done                     chan struct{}
	addRequest               chan syncLRUCacheAddRequest
	addResponse              chan syncLRUCacheAddResponse
	updateExpirationRequest  chan syncLRUCacheUpdateExpirationRequest
	updateExpirationResponse chan syncLRUCacheUpdateExpirationResponse
	getItemRequest           chan syncLRUCacheGetItemRequest
	getItemResponse          chan syncLRUCacheGetItemResponse
	eachRequest              chan syncLRUCacheEachRequest
	eachResponse             chan syncLRUCacheEachResponse
	removeRequest            chan syncLRUCacheRemoveRequest
	removeResponse           chan syncLRUCacheRemoveResponse
	sizeRequest              chan syncLRUCacheSizeRequest
	sizeResponse             chan syncLRUCacheSizeResponse
}

type syncLRUCacheAddRequest struct {
	Item CacheItem
}

type syncLRUCacheAddResponse struct {
	Exists bool
}

type syncLRUCacheUpdateExpirationRequest struct {
	Key      string
	ExpireAt int64
}

type syncLRUCacheUpdateExpirationResponse struct {
	Ok bool
}

type syncLRUCacheGetItemRequest struct {
	Key string
}

type syncLRUCacheGetItemResponse struct {
	Item CacheItem
	Ok   bool
}

type syncLRUCacheEachRequest struct{}

type syncLRUCacheEachResponse struct {
	Each chan CacheItem
}

type syncLRUCacheRemoveRequest struct {
	Key string
}

type syncLRUCacheRemoveResponse struct{}

type syncLRUCacheSizeRequest struct{}

type syncLRUCacheSizeResponse struct {
	Size int
}

// Thread-safe implementation of `LRUCache`.
func NewSyncLRUCache(maxSize int) *SyncLRUCache {
	c := &SyncLRUCache{
		cache:                    NewLRUCache(maxSize),
		done:                     make(chan struct{}),
		addRequest:               make(chan syncLRUCacheAddRequest),
		addResponse:              make(chan syncLRUCacheAddResponse),
		updateExpirationRequest:  make(chan syncLRUCacheUpdateExpirationRequest),
		updateExpirationResponse: make(chan syncLRUCacheUpdateExpirationResponse),
		getItemRequest:           make(chan syncLRUCacheGetItemRequest),
		getItemResponse:          make(chan syncLRUCacheGetItemResponse),
		eachRequest:              make(chan syncLRUCacheEachRequest),
		eachResponse:             make(chan syncLRUCacheEachResponse),
		removeRequest:            make(chan syncLRUCacheRemoveRequest),
		removeResponse:           make(chan syncLRUCacheRemoveResponse),
		sizeRequest:              make(chan syncLRUCacheSizeRequest),
		sizeResponse:             make(chan syncLRUCacheSizeResponse),
	}

	go c.listen()

	return c
}

func (c *SyncLRUCache) listen() {
	// Listen for requests and dispatch to underlying cache object.
	for {
		select {
		case request := <-c.addRequest:
			exists := c.cache.Add(request.Item)
			c.addResponse <- syncLRUCacheAddResponse{Exists: exists}
		case request := <-c.updateExpirationRequest:
			ok := c.cache.UpdateExpiration(request.Key, request.ExpireAt)
			c.updateExpirationResponse <- syncLRUCacheUpdateExpirationResponse{Ok: ok}
		case request := <-c.getItemRequest:
			item, ok := c.cache.GetItem(request.Key)
			c.getItemResponse <- syncLRUCacheGetItemResponse{Item: item, Ok: ok}
		case <-c.eachRequest:
			each := c.cache.Each()
			c.eachResponse <- syncLRUCacheEachResponse{Each: each}
		case request := <-c.removeRequest:
			c.cache.Remove(request.Key)
			c.removeResponse <- syncLRUCacheRemoveResponse{}
		case <-c.sizeRequest:
			size := c.cache.Size()
			c.sizeResponse <- syncLRUCacheSizeResponse{Size: size}
		case <-c.done:
			return
		}
	}
}

func (c *SyncLRUCache) Add(item CacheItem) bool {
	c.addRequest <- syncLRUCacheAddRequest{Item: item}
	response := <-c.addResponse
	return response.Exists
}

func (c *SyncLRUCache) UpdateExpiration(key string, expireAt int64) bool {
	c.updateExpirationRequest <- syncLRUCacheUpdateExpirationRequest{Key: key, ExpireAt: expireAt}
	response := <-c.updateExpirationResponse
	return response.Ok
}

func (c *SyncLRUCache) GetItem(key string) (CacheItem, bool) {
	c.getItemRequest <- syncLRUCacheGetItemRequest{Key: key}
	response := <-c.getItemResponse
	return response.Item, response.Ok
}

func (c *SyncLRUCache) Each() chan CacheItem {
	c.eachRequest <- syncLRUCacheEachRequest{}
	response := <-c.eachResponse
	return response.Each
}

func (c *SyncLRUCache) Remove(key string) {
	c.removeRequest <- syncLRUCacheRemoveRequest{Key: key}
	<-c.removeResponse
}

func (c *SyncLRUCache) Unlock() {
	// No operation.
}

func (c *SyncLRUCache) Lock() {
	// No operation.
}

func (c *SyncLRUCache) Close() error {
	close(c.done)
	return nil
}

func (c *SyncLRUCache) Size() int {
	c.sizeRequest <- syncLRUCacheSizeRequest{}
	response := <-c.sizeResponse
	return response.Size
}

func (c *SyncLRUCache) Describe(ch chan<- *prometheus.Desc) {
	c.cache.Describe(ch)
}

func (c *SyncLRUCache) Collect(ch chan<- prometheus.Metric) {
	c.cache.Collect(ch)
}

func (c *SyncLRUCache) New() Cache {
	return NewSyncLRUCache(c.cache.cacheSize)
}
