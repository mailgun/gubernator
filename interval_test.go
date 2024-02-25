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

import (
	"testing"

	"github.com/mailgun/gubernator/v2"
	"github.com/mailgun/holster/v4/clock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInterval(t *testing.T) {
	t.Run("Happy path", func(t *testing.T) {
		interval := gubernator.NewInterval(10 * clock.Millisecond)
		defer interval.Stop()
		interval.Next()

		assert.Empty(t, interval.C)

		clock.Sleep(10 * clock.Millisecond)

		// Wait for tick.
		select {
		case <-interval.C:
		case <-clock.After(100 * clock.Millisecond):
			require.Fail(t, "timeout")
		}
	})
}

func TestGregorianExpirationMinute(t *testing.T) {
	// Validate calculation assumption
	now := clock.Date(2019, clock.November, 11, 00, 00, 00, 00, clock.UTC)
	expire, err := gubernator.GregorianExpiration(now, gubernator.GregorianMinutes)
	assert.Nil(t, err)
	assert.Equal(t, clock.Date(2019, clock.November, 11, 00, 00, 59, 999000000, clock.UTC),
		clock.Unix(0, expire*1000000).UTC())

	// Expect the same expire time regardless of the current second or nsec
	now = clock.Date(2019, clock.November, 11, 00, 00, 30, 100, clock.UTC)
	expire, err = gubernator.GregorianExpiration(now, gubernator.GregorianMinutes)
	assert.Nil(t, err)
	assert.Equal(t, int64(1573430459999), expire)
}

func TestGregorianExpirationHour(t *testing.T) {
	// Validate calculation assumption
	now := clock.Date(2019, clock.November, 11, 00, 00, 00, 00, clock.UTC)
	expire, err := gubernator.GregorianExpiration(now, gubernator.GregorianHours)
	assert.Nil(t, err)
	assert.Equal(t, clock.Date(2019, clock.November, 11, 00, 59, 59, 999000000, clock.UTC),
		clock.Unix(0, expire*1000000).UTC())

	// Expect the same expire time regardless of the current minute, second or nsec
	now = clock.Date(2019, clock.November, 11, 00, 20, 1, 2134, clock.UTC)
	expire, err = gubernator.GregorianExpiration(now, gubernator.GregorianHours)
	assert.Nil(t, err)
	assert.Equal(t, int64(1573433999999), expire)
}

func TestGregorianExpirationDay(t *testing.T) {
	// Validate calculation assumption
	now := clock.Date(2019, clock.November, 11, 00, 00, 00, 00, clock.UTC)
	expire, err := gubernator.GregorianExpiration(now, gubernator.GregorianDays)
	assert.Nil(t, err)
	assert.Equal(t, clock.Date(2019, clock.November, 11, 23, 59, 59, 999000000, clock.UTC),
		clock.Unix(0, expire*1000000).UTC())

	// Expect the same expire time regardless of the current hour, minute, second or nsec
	now = clock.Date(2019, clock.November, 11, 12, 10, 9, 2345, clock.UTC)
	expire, err = gubernator.GregorianExpiration(now, gubernator.GregorianDays)
	assert.Nil(t, err)
	assert.Equal(t, int64(1573516799999), expire)
}

func TestGregorianExpirationMonth(t *testing.T) {
	// Validate calculation assumption
	now := clock.Date(2019, clock.November, 1, 00, 00, 00, 00, clock.UTC)
	expire, err := gubernator.GregorianExpiration(now, gubernator.GregorianMonths)
	assert.Nil(t, err)
	assert.Equal(t, clock.Date(2019, clock.November, 30, 23, 59, 59, 999000000, clock.UTC),
		clock.Unix(0, expire*1000000).UTC())

	// Expect the same expire time regardless of the current day, minute, second or nsec
	now = clock.Date(2019, clock.November, 11, 22, 2, 23, 0, clock.UTC)
	expire, err = gubernator.GregorianExpiration(now, gubernator.GregorianMonths)
	assert.Nil(t, err)
	assert.Equal(t, int64(1575158399999), expire)

	// January has 31 days
	now = clock.Date(2019, clock.January, 1, 00, 00, 00, 00, clock.UTC)
	expire, err = gubernator.GregorianExpiration(now, gubernator.GregorianMonths)
	assert.Nil(t, err)

	eom := clock.Date(2019, clock.January, 31, 23, 59, 59, 999999999, clock.UTC)
	assert.Equal(t, eom.UnixNano()/1000000, expire)
}

func TestGregorianExpirationYear(t *testing.T) {
	// Validate calculation assumption
	now := clock.Date(2019, clock.January, 1, 00, 00, 00, 00, clock.UTC)
	expire, err := gubernator.GregorianExpiration(now, gubernator.GregorianYears)
	assert.Nil(t, err)
	assert.Equal(t, clock.Date(2019, clock.December, 31, 23, 59, 59, 999000000, clock.UTC),
		clock.Unix(0, expire*1000000).UTC())

	// Expect the same expire time regardless of the current month, day, minute, second or nsec
	now = clock.Date(2019, clock.March, 1, 20, 30, 1231, 0, clock.UTC)
	expire, err = gubernator.GregorianExpiration(now, gubernator.GregorianYears)
	assert.Nil(t, err)
	assert.Equal(t, int64(1577836799999), expire)
}

func TestGregorianExpirationInvalid(t *testing.T) {
	now := clock.Date(2019, clock.January, 1, 00, 00, 00, 00, clock.UTC)
	expire, err := gubernator.GregorianExpiration(now, 99)
	assert.NotNil(t, err)
	assert.Equal(t, int64(0), expire)
	assert.Equal(t, "behavior DURATION_IS_GREGORIAN is set; but `Duration` is not a valid gregorian interval", err.Error())
}
