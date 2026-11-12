// Package transport pkg/transport/managed_transport_close_test.go c2-net-transport
package transport

import (
	"sync"
	"testing"
)

// Concurrent closers used to pass the done check together and close it twice.
func TestManagedTransportConcurrentClose(t *testing.T) {
	for range 200 {
		mt := NewManagedTransportForTest(nil)
		var wg sync.WaitGroup
		for i := range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if i%2 == 0 {
					mt.closeWith("test")
				} else {
					mt.closeWithoutDeregister()
				}
			}()
		}
		wg.Wait()
	}
}
