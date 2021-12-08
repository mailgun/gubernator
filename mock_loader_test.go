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
