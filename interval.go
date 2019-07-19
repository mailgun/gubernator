package gubernator

import (
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
