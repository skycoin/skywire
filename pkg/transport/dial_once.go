// Package transport pkg/transport/dial_once.go c2-net-transport
package transport

import (
	"sync"

	"github.com/google/uuid"
)

// dialFlights lets one dial run per transport ID. Concurrent requests for the
// same peer and type wait for it, instead of each opening a connection that
// the two ends then disagree on and tear down.
type dialFlights struct {
	mu sync.Mutex
	m  map[uuid.UUID]*dialFlight
}

type dialFlight struct {
	done chan struct{}
	mTp  *ManagedTransport
	err  error
}

// join returns the flight for id and whether the caller leads it.
func (d *dialFlights) join(id uuid.UUID) (*dialFlight, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if f, ok := d.m[id]; ok {
		return f, false
	}
	if d.m == nil {
		d.m = make(map[uuid.UUID]*dialFlight)
	}
	f := &dialFlight{done: make(chan struct{})}
	d.m[id] = f
	return f, true
}

// finish records the leader's result and releases the waiters.
func (d *dialFlights) finish(id uuid.UUID, f *dialFlight, mTp *ManagedTransport, err error) {
	d.mu.Lock()
	delete(d.m, id)
	d.mu.Unlock()
	f.mTp, f.err = mTp, err
	close(f.done)
}
