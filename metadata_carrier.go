/*
Copyright 2018-2022 Mailgun Technologies Inc

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

type MetadataCarrier struct {
	Map map[string]string
}

// Get returns the value associated with the passed key.
func (c MetadataCarrier) Get(key string) string {
	return c.Map[key]
}

// Set stores the key-value pair.
func (c MetadataCarrier) Set(key, value string) {
	c.Map[key] = value
}

// Keys lists the keys stored in this carrier.
func (c MetadataCarrier) Keys() []string {
	keys := make([]string, 0, len(c.Map))
	for k := range c.Map {
		keys = append(keys, k)
	}
	return keys
}
