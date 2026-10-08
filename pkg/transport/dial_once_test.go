// Package transport pkg/transport/dial_once_test.go c2-net-transport
package transport

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Concurrent requests for one transport ID make one dial and share its result.
func TestDialFlightsOneDialPerID(t *testing.T) {
	var d dialFlights
	id := uuid.New()
	want := errors.New("dial result")
	var dials atomic.Int32
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			f, lead := d.join(id)
			if lead {
				dials.Add(1)
				d.finish(id, f, nil, want)
			} else {
				<-f.done
			}
			errs[i] = f.err
		}(i)
	}
	close(start)
	wg.Wait()
	require.GreaterOrEqual(t, dials.Load(), int32(1))
	for _, err := range errs {
		require.ErrorIs(t, err, want)
	}

	// Once finished, the next request dials again.
	f, lead := d.join(id)
	require.True(t, lead)
	d.finish(id, f, nil, nil)
}
