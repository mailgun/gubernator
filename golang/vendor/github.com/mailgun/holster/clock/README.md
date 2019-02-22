# Clock

A drop in (almost) replacement for the system `time` package. It provides a way
to make scheduled calls, timers and tickers deterministic in tests. By default
it forwards all calls to the system `time` package. In test, however, it is
possible to enable the frozen clock mode, and advance time manually to make
scheduled even trigger at certain moments.

# Usage

```go
package foo

import (
    "time"

    "github.com/mailgun/holster/clock"
    . "gopkg.in/check.v1"
)

type FooSuite struct{}

var _ = Suite(&FooSuite{})

func (s *FooSuite) SetUpTest(c *C) {
    // Freeze switches the clock package to the frozen clock mode. You need to
    // advance time manually from now on. Note that all scheduled events, timers
    // and ticker created before this call keep operating in real time.
    //
    // The initial time is set to 0 here, but you can set any datetime.
    clock.Freeze(time.Time(0))
}

func (s *FooSuite) TearDownTest(c *C) {
    // Reverts the effect of Freeze in test setup.
    clock.Unfreeze()
}

func (s *FooSuite) TestSleep(c *C) {
    var fired bool

    clock.AfterFunc(100*time.Millisecond, func() {
        fired = true
    })
    clock.Advance(93*time.Millisecond)
    
    // Advance will make all fire all events, timers, tickers that are
    // scheduled for the passed period of time. Note that scheduled functions
    // are called from within Advanced unlike system time package that calls
    // them in their own goroutine.
    c.Assert(clock.Advance(6*time.Millisecond), Equals, 97*time.Millisecond)
    c.Assert(fired, Equals, false)
    c.Assert(clock.Advance(1*time.Millisecond), Equals, 100*time.Millisecond)
    c.Assert(fired, Equals, true)
}
```
