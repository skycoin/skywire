package dmsg

import (
	"fmt"
	"io"
	"math"
	"net"
	"sync"
)

// LocalWindow represents the read window of a given dmsg.Transport
type LocalWindow struct {
	r   int           // remaining window (in bytes)
	max int           // max possible window (in bytes)
	buf net.Buffers   // buffer for unread bytes
	ch  chan struct{} // indicator for new data in 'buf'
	mx  sync.Mutex    // race protection
}

// NewLocalWindow creates a new local window with a given size in bytes.
func NewLocalWindow(size int) *LocalWindow {
	return &LocalWindow{
		r:   size,
		max: size,
		buf: make(net.Buffers, 0, 255),
		ch:  make(chan struct{}, 1),
	}
}

// Max returns the maximum size of the local window.
func (lw *LocalWindow) Max() int {
	return lw.max
}

// Remaining returns the remaining window.
func (lw *LocalWindow) Remaining() int {
	lw.mx.Lock()
	defer lw.mx.Unlock()
	return lw.r
}

// Enqueue adds the given payload 'p' to the internal buffer of the window.
// 'tpDone' indicates whether the associated dmsg.Transport has been closed.
func (lw *LocalWindow) Enqueue(p []byte, tpDone chan struct{}) error {
	lw.mx.Lock()
	defer lw.mx.Unlock()

	// Offset local window.
	// If the length of the FWD payload exceeds local window, then the remote client is not respecting our advertised
	// window size.
	if lw.r -= len(p); lw.r < 0 || lw.r > lw.max {
		return fmt.Errorf("failed to enqueue local window: remote is not respecting advertised window size: remaining(%d) min(%d) max(%d)",
			lw.r, 0, lw.max)
	}
	fmt.Println("LocalWindow.Enqueue() remaining:", lw.r)

	lw.buf = append(lw.buf, p)
	if !isDone(tpDone) {
		select {
		case lw.ch <- struct{}{}:
		default:
		}
	}

	return nil
}

// Read reads from the internal buffer of the local window.
// ACK frames is delivered on every read (to clear the remote record of the local window).
// The ACK frame should contain the number of freed bytes.
func (lw *LocalWindow) Read(p []byte, tpDone <-chan struct{}, sendAck func(uint16)) (n int, err error) {
	// return if 'p' has 0 len
	if len(p) == 0 {
		lw.mx.Lock()
		if isDone(tpDone) && lw.r == lw.max {
			err = io.EOF
		}
		lw.mx.Unlock()
		return
	}

	lastN := 0
	for {
		lw.mx.Lock()

		// We limit the reader so that ACK frames has an 'window_offset' field that is in scope.
		lastN, err = io.LimitReader(&lw.buf, math.MaxUint16).Read(p)
		p = p[lastN:]
		n += lastN

		if lastN > 0 {
			// increase local window and send ACK.
			if lw.r += lastN; lw.r < 0 || lw.r > lw.max {
				lw.mx.Unlock()
				panic(fmt.Errorf("bug: local window size became invalid after read: remaining(%d) min(%d) max(%d)",
					lw.r, 0, lw.max))
			}
			fmt.Println("LocalWindow.Read() remaining:", lw.r)

			if !isDone(tpDone) {
				go sendAck(uint16(lastN))
				err = nil
			}
			lw.mx.Unlock()
			return n, err
		}
		lw.mx.Unlock()

		if _, ok := <-lw.ch; !ok {
			return n, err
		}
	}
}

// Close closes the local window.
func (lw *LocalWindow) Close() error {
	if lw == nil {
		return nil
	}
	lw.mx.Lock()
	close(lw.ch)
	lw.mx.Unlock()
	return nil
}

// RemoteWindow represents the local record of the remote window.
type RemoteWindow struct {
	r   int           // remaining window (in bytes)
	max int           // max possible window (in bytes)
	ch  chan struct{} // blocks writes until remote window clears up
	wMx sync.Mutex    // ensures only one write can happen at one time
	mx  sync.Mutex    // race protection
}

// NewRemoteWindow creates a new local representation of the remote window.
// 'size' is in bytes and is the total size of the remote window.
func NewRemoteWindow(size int) *RemoteWindow {
	return &RemoteWindow{
		r:   size,
		max: size,
		ch:  make(chan struct{}, 1),
	}
}

// Grow should be triggered when we receive a remote ACK to grow our record of the remote window.
// 'tpDone' signals when the associated dmsg.Transport is closed.
func (rw *RemoteWindow) Grow(n int, tpDone <-chan struct{}) error {
	rw.mx.Lock()
	defer rw.mx.Unlock()

	// grow remaining window
	if rw.r += n; rw.r < 0 || rw.r > rw.max {
		return fmt.Errorf("local record of remote window has become invalid: remaning(%d) min(%d) max(%d)", rw.r, 0, rw.max)
	}

	if !isDone(tpDone) {
		select {
		case rw.ch <- struct{}{}:
		default:
		}
	}

	return nil
}

// Write blocks until all of 'p' is written, an error occurs, or the associated dmsg.Transport is closed.
// 'sendFwd' contains the logic to write a FWD frame.
func (rw *RemoteWindow) Write(p []byte, sendFwd func([]byte) error) (n int, err error) {
	rw.wMx.Lock()
	defer rw.wMx.Unlock()

	lastN := 0
	for {
		r := rw.remaining()

		// if remaining window has len 0, wait until it opens up
		if r <= 0 {
			if _, ok := <-rw.ch; !ok {
				return 0, io.ErrClosedPipe
			}
			continue
		}

		// write FWD frame and update 'p' and 'n'
		lastN, err = rw.write(p, r, sendFwd)
		p, n = p[lastN:], n+lastN

		if err != nil || len(p) <= 0 {
			return
		}
	}
}

func (rw *RemoteWindow) write(p []byte, r int, sendFwd func([]byte) error) (n int, err error) {
	n = len(p)

	// ensure written payload does not surpass remaining remote window size or maximum allowed FWD payload size
	if n > r {
		n = r
	}
	if n > maxFwdPayLen {
		n = maxFwdPayLen
	}

	// write FWD and remove written portion of 'p'
	if err := sendFwd(p[:n]); err != nil {
		return 0, err
	}

	// shrink remaining remote window
	return n, rw.shrink(n)
}

func (rw *RemoteWindow) shrink(dec int) error {
	rw.mx.Lock()
	defer rw.mx.Unlock()
	if rw.r -= dec; rw.r < 0 || rw.r > rw.max {
		return fmt.Errorf("local record of remote window has become invalid: remaning(%d) min(%d) max(%d)", rw.r, 0, rw.max)
	}
	return nil
}

func (rw *RemoteWindow) remaining() int {
	rw.mx.Lock()
	defer rw.mx.Unlock()
	return rw.r
}

// Close closes the local record of the remote window.
func (rw *RemoteWindow) Close() error {
	if rw == nil {
		return nil
	}
	rw.mx.Lock()
	close(rw.ch)
	rw.mx.Unlock()
	return nil
}

/*
	Helper functions.
*/

func isDone(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}
