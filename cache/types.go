/*
Copyright 2018-2019 Mailgun Technologies Inc

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

package cache

// Interface accepts any cache which returns cache stats
type Stater interface {
	Stats(bool) Stats
}

// So algorithms can interface with different cache implementations
type Cache interface {
	// Access methods
	Add(key Key, value interface{}, expireAt int64) bool
	UpdateExpiration(key Key, expireAt int64) bool
	Get(key Key) (value interface{}, ok bool)
	Remove(key Key)

	// If the cache is exclusive, this will control access to the cache
	Unlock()
	Lock()
}

// A Key may be any value that is comparable. See http://golang.org/ref/spec#Comparison_operators
type Key interface{}

// Holds stats collected about the cache
type Stats struct {
	Size int64
	Miss int64
	Hit  int64
}
