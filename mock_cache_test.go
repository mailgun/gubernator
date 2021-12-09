package gubernator

// Mock implementation of Cache.

import "github.com/stretchr/testify/mock"

type MockCache struct {
	mock.Mock
}

var _ Cache = &MockCache{}

func (m *MockCache) Add(item CacheItem) bool {
	args := m.Called(item)
	return args.Bool(0)
}

func (m *MockCache) UpdateExpiration(key string, expireAt int64) bool {
	args := m.Called(key, expireAt)
	return args.Bool(0)
}

func (m *MockCache) GetItem(key string) (value CacheItem, ok bool) {
	args := m.Called(key)
	var retval CacheItem
	if retval2, ok := args.Get(0).(CacheItem); ok {
		retval = retval2
	}
	return retval, args.Bool(1)
}

func (m *MockCache) Each() chan CacheItem {
	args := m.Called()
	var retval chan CacheItem
	if retval2, ok := args.Get(0).(chan CacheItem); ok {
		retval = retval2
	}
	return retval
}

func (m *MockCache) Remove(key string) {
	m.Called(key)
}

func (m *MockCache) Size() int64 {
	args := m.Called()
	return int64(args.Int(0))
}

func (m *MockCache) Close() error {
	args := m.Called()
	return args.Error(0)
}
