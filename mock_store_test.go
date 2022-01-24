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

package gubernator_test

// Mock implementation of Store.

import (
	"context"

	guber "github.com/mailgun/gubernator/v2"
	"github.com/stretchr/testify/mock"
)

type MockStore2 struct {
	mock.Mock
}

var _ guber.Store = &MockStore2{}

func (m *MockStore2) OnChange(ctx context.Context, r *guber.RateLimitReq, item *guber.CacheItem) {
	m.Called(ctx, r, item)
}

func (m *MockStore2) Get(ctx context.Context, r *guber.RateLimitReq) (*guber.CacheItem, bool) {
	args := m.Called(ctx, r)
	retval, _ := args.Get(0).(*guber.CacheItem)
	return retval, args.Bool(1)
}

func (m *MockStore2) Remove(ctx context.Context, key string) {
	m.Called(ctx, key)
}
