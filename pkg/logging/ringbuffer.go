// Package logging pkg/logging/ringbuffer.go c0-com-log
package logging

import (
	"bytes"
	"sync"
)

// DefaultRingBufferBytes is the default capacity of a RingBuffer.
const DefaultRingBufferBytes = 256 * 1024

// RingBuffer is a thread-safe, fixed-capacity io.Writer that retains the most
// recent bytes written (oldest dropped, trimmed to a line boundary). It exists
// so a service can expose its recent log output over a debug endpoint without
// writing to disk: attach it with AddHook(NewWriteHook(rb)) and serve rb.Bytes().
//
// The backing store is a single pre-allocated ring of `max` bytes: Write copies
// into it in place and never allocates or grows. (The previous append/re-slice
// implementation reallocated the whole buffer roughly every `max` bytes of log
// output; fired on EVERY log line of every service, that was a top allocation
// source and a needless GC driver on busy services like dmsg-discovery.)
type RingBuffer struct {
	mu    sync.Mutex
	buf   []byte // fixed ring, len == max
	start int    // index of the oldest retained byte
	size  int    // number of valid bytes held (0..max)
	max   int
}

// NewRingBuffer returns a RingBuffer holding up to maxBytes (DefaultRingBufferBytes
// when maxBytes <= 0).
func NewRingBuffer(maxBytes int) *RingBuffer {
	if maxBytes <= 0 {
		maxBytes = DefaultRingBufferBytes
	}
	return &RingBuffer{buf: make([]byte, maxBytes), max: maxBytes}
}

// Write copies p into the ring in place, dropping the oldest bytes when capacity
// is exceeded. Allocation-free in steady state. Never errors.
func (rb *RingBuffer) Write(p []byte) (int, error) {
	n := len(p)
	rb.mu.Lock()
	defer rb.mu.Unlock()
	// A single write at least as large as the ring: only its last max bytes survive.
	if len(p) >= rb.max {
		copy(rb.buf, p[len(p)-rb.max:])
		rb.start, rb.size = 0, rb.max
		return n, nil
	}
	// Copy into the ring just past the current contents, wrapping at the end.
	pos := (rb.start + rb.size) % rb.max
	c := copy(rb.buf[pos:], p)
	if c < len(p) {
		copy(rb.buf, p[c:])
	}
	if rb.size += len(p); rb.size > rb.max {
		// The write overwrote the oldest bytes; advance start past them.
		rb.start = (rb.start + (rb.size - rb.max)) % rb.max
		rb.size = rb.max
	}
	return n, nil
}

// Bytes returns a copy of the current buffer contents, oldest first. Once the
// ring has wrapped (is full) the result is trimmed to the next line boundary so
// it never starts mid-line — matching the original append-based behavior.
func (rb *RingBuffer) Bytes() []byte {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	out := make([]byte, rb.size)
	c := copy(out, rb.buf[rb.start:min(rb.start+rb.size, rb.max)])
	if c < rb.size {
		copy(out[c:], rb.buf[:rb.size-c])
	}
	if rb.size == rb.max {
		if i := bytes.IndexByte(out, '\n'); i >= 0 && i+1 <= len(out) {
			out = out[i+1:]
		}
	}
	return out
}
