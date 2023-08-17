/*
Modifications Copyright 2023 Mailgun Technologies Inc

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

type Cache interface {
	Add(item *CacheItem) bool
	UpdateExpiration(key string, expireAt int64) bool
	GetItem(key string) (value *CacheItem, ok bool)
	Each() chan *CacheItem
	Remove(key string)
	Size() int64
	Close() error
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
