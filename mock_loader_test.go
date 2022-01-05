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

// Mock implementation of Loader.

import (
	"github.com/stretchr/testify/mock"
)

type MockLoader2 struct {
	mock.Mock
}

var _ Loader = &MockLoader2{}

func (m *MockLoader2) Load() (chan CacheItem, error) {
	args := m.Called()
	var retval chan CacheItem
	if retval2, ok := args.Get(0).(chan CacheItem); ok {
		retval = retval2
	}
	return retval, args.Error(1)
}

func (m *MockLoader2) Save(ch chan CacheItem) error {
	args := m.Called(ch)
	return args.Error(0)
}
