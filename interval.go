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

package gubernator

import (
	"errors"
	"github.com/mailgun/holster"
	"time"
)

type Interval struct {
	C  chan struct{}
	in chan struct{}
	wg holster.WaitGroup
}

// NewInterval creates a new ticker like object, however
// the `C` channel does not return the current time and
// `C` channel will only get a tick after `Next()` has
// been called.
func NewInterval(d time.Duration) *Interval {
	i := Interval{
		C:  make(chan struct{}, 1),
		in: make(chan struct{}),
	}
	go i.run(d)
	return &i
}

func (i *Interval) run(d time.Duration) {
	i.wg.Until(func(done chan struct{}) bool {
		select {
		case <-i.in:
			time.Sleep(d)
			i.C <- struct{}{}
			return true
		case <-done:
			return false
		}
	})
}

func (i *Interval) Stop() {
	i.wg.Stop()
}

// Next queues the next interval to run, If multiple calls to Next() are
// made before previous intervals have completed they are ignored.
func (i *Interval) Next() {
	select {
	case i.in <- struct{}{}:
	default:
	}
}

const (
	GregorianMinutes int64 = iota
	GregorianHours
	GregorianDays
	GregorianWeeks
	GregorianMonths
	GregorianYears
)

// GregorianDuration returns the entire duration of the Gregorian interval
func GregorianDuration(now time.Time, d int64) (int64, error) {
	switch d {
	case GregorianMinutes:
		return 60000, nil
	case GregorianHours:
		return 3.6e+6, nil
	case GregorianDays:
		return 8.64e+7, nil
	case GregorianWeeks:
		return 0, errors.New("`Duration = GregorianWeeks` not yet supported; consider making a PR!`")
	case GregorianMonths:
		y, m, _ := now.Date()
		// Given the beginning of the month, subtract the end of the current month to get the duration
		begin := time.Date(y, m, 1, 0, 0, 0, 0, now.Location())
		end := begin.AddDate(0, 1, 0).Add(-time.Nanosecond)
		return end.UnixNano() - begin.UnixNano()/1000000, nil
	case GregorianYears:
		y, _, _ := now.Date()
		// Given the beginning of the year, subtract the end of the current year to get the duration
		begin := time.Date(y, time.January, 1, 0, 0, 0, 0, now.Location())
		end := begin.AddDate(1, 0, 0).Add(-time.Nanosecond)
		return end.UnixNano() - begin.UnixNano()/1000000, nil
	}
	return 0, errors.New("behavior DURATION_IS_GREGORIAN is set; but `Duration` is not a valid gregorian interval")

}

// GregorianExpiration returns an gregorian interval as defined by the
// 'DURATION_IS_GREGORIAN` Behavior. it returns the expiration time as the
// end of GREGORIAN interval in milliseconds from `now`.
//
// Example: If `now` is 2019-01-01 11:20:10 and `d` = GregorianMinutes then the return
// expire time would be 2019-01-01 11:20:59 in milliseconds since epoch
func GregorianExpiration(now time.Time, d int64) (int64, error) {
	switch d {
	case GregorianMinutes:
		return now.Truncate(time.Minute).
			Add(time.Minute-time.Nanosecond).
			UnixNano() / 1000000, nil
	case GregorianHours:
		y, m, d := now.Date()
		// See time.Truncate() documentation on why we can' reliably use time.Truncate(Hour) here.
		return time.Date(y, m, d, now.Hour(), 0, 0, 0, now.Location()).
			Add(time.Hour-time.Nanosecond).
			UnixNano() / 1000000, nil
	case GregorianDays:
		y, m, d := now.Date()
		return time.Date(y, m, d, 23, 59, 59, int(time.Second-time.Nanosecond), now.Location()).
			UnixNano() / 1000000, nil
	case GregorianWeeks:
		return 0, errors.New("`Duration = GregorianWeeks` not yet supported; consider making a PR!`")
	case GregorianMonths:
		y, m, _ := now.Date()
		return time.Date(y, m, 1, 0, 0, 0, 0, now.Location()).
			AddDate(0, 1, 0).Add(-time.Nanosecond).
			UnixNano() / 1000000, nil
	case GregorianYears:
		y, _, _ := now.Date()
		return time.Date(y, time.January, 1, 0, 0, 0, 0, now.Location()).
			AddDate(1, 0, 0).
			Add(-time.Nanosecond).
			UnixNano() / 1000000, nil
	}
	return 0, errors.New("behavior DURATION_IS_GREGORIAN is set; but `Duration` is not a valid gregorian interval")
}
