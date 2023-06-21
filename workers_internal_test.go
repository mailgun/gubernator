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

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type MockHasher struct {
	mock.Mock
}

func (m *MockHasher) ComputeHash63(input string) uint64 {
	args := m.Called(input)
	retval, _ := args.Get(0).(uint64)
	return retval
}

func TestWorkersInternal(t *testing.T) {
	t.Run("getWorker()", func(t *testing.T) {
		const concurrency = 32
		conf := &Config{
			Workers:   concurrency,
			CacheSize: 1000,
		}
		require.NoError(t, conf.SetDefaults())

		// Test that getWorker() interpolates the hash to find the expected worker.
		testCases := []struct {
			Name        string
			Hash        uint64
			ExpectedIdx int
		}{
			{"Hash 0%", 0, 0},
			{"Hash 50%", 0x3fff_ffff_ffff_ffff, (concurrency / 2) - 1},
			{"Hash 50% + 1", 0x4000_0000_0000_0000, concurrency / 2},
			{"Hash 100%", 0x7fff_ffff_ffff_ffff, concurrency - 1},
		}

		for _, testCase := range testCases {
			t.Run(testCase.Name, func(t *testing.T) {
				pool := NewWorkerPool(conf)
				defer pool.Close()
				mockHasher := &MockHasher{}
				pool.hasher = mockHasher

				// Setup mocks.
				mockHasher.On("ComputeHash63", mock.Anything).Once().Return(testCase.Hash)

				// Call code.
				worker := pool.getWorker("Foobar")

				// Verify
				require.NotNil(t, worker)

				var actualIdx int
				for ; actualIdx < len(pool.workers); actualIdx++ {
					if pool.workers[actualIdx] == worker {
						break
					}
				}
				assert.Equal(t, testCase.ExpectedIdx, actualIdx)
				mockHasher.AssertExpectations(t)
			})
		}
	})
}
