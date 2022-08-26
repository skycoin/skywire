/*Package deadline Copied from https://golang.org/src/net/pipe.go with some changes.*/
package deadline

import (
	"sync"
	"time"
)

// PipeDeadline is an abstraction for handling timeouts.
type PipeDeadline struct {
	mu       sync.Mutex // Guards timer and cancel
	timer    *time.Timer
	cancelMu sync.RWMutex
	cancel   chan struct{} // Must be non-nil
}

// MakePipeDeadline creates a new PipeDeadline
func MakePipeDeadline() PipeDeadline {
	return PipeDeadline{cancel: make(chan struct{})}
}

// Set sets the point in time when the deadline will time out.
// A timeout event is signaled by closing the channel returned by waiter.
// Once a timeout has occurred, the deadline can be refreshed by specifying a
// t value in the future.
//
// A zero value for t prevents timeout.
func (d *PipeDeadline) Set(t time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil && !d.timer.Stop() {
		<-d.cancel // Wait for the timer callback to finish and close cancel
	}
	d.timer = nil

	// Time is zero, then there is no deadline.
	closed := d.Closed()
	if t.IsZero() {
		if closed {
			d.cancel = make(chan struct{})
		}
		return
	}

	// Time in the future, setup a timer to cancel in the future.
	if dur := time.Until(t); dur > 0 {
		if closed {
			d.cancelMu.Lock()
			d.cancel = make(chan struct{})
			d.cancelMu.Unlock()
		}
		d.timer = time.AfterFunc(dur, func() {
			close(d.cancel)
		})
		return
	}

	// Time in the past, so close immediately.
	if !closed {
		close(d.cancel)
	}
}

// Wait returns a channel that is closed when the deadline is exceeded.
func (d *PipeDeadline) Wait() chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cancel
}

// Closed checks if deadline happened.
func (d *PipeDeadline) Closed() bool {
	d.cancelMu.RLock()
	defer d.cancelMu.RUnlock()

	select {
	case <-d.cancel:
		return true
	default:
		return false
	}
}
