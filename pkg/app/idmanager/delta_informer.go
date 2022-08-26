// Package idmanager delta_informer.go
package idmanager

// DeltaInformer informs when there has been a change to the id-manager.
// .Trigger() and .Stop() can not be called concurrently to each other.
type DeltaInformer struct {
	closed bool
	ch     chan int
}

// NewDeltaInformer creates a new DeltaInformer.
func NewDeltaInformer() *DeltaInformer {
	return &DeltaInformer{ch: make(chan int)}
}

// Chan returns the internal chan that gets triggered when a change occurs.
func (di *DeltaInformer) Chan() <-chan int {
	if di == nil {
		return nil
	}
	return di.ch
}

// Trigger should be called whenever the internal chan should be triggered.
func (di *DeltaInformer) Trigger(n int) {
	if di == nil || di.closed {
		return
	}
	select {
	case di.ch <- n:
	default:
	}
}

// Stop closes the internal chan.
func (di *DeltaInformer) Stop() {
	if di == nil || di.closed {
		return
	}
	di.closed = true
	close(di.ch)
}
