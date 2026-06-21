// Package logging pkg/logging/ringbuffer.go
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
type RingBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

// NewRingBuffer returns a RingBuffer holding up to maxBytes (DefaultRingBufferBytes
// when maxBytes <= 0).
func NewRingBuffer(maxBytes int) *RingBuffer {
	if maxBytes <= 0 {
		maxBytes = DefaultRingBufferBytes
	}
	return &RingBuffer{max: maxBytes}
}

// Write appends p, dropping the oldest bytes (trimmed to the next line boundary
// so the buffer never starts mid-line) when capacity is exceeded. Never errors.
func (rb *RingBuffer) Write(p []byte) (int, error) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.buf = append(rb.buf, p...)
	if len(rb.buf) > rb.max {
		rb.buf = rb.buf[len(rb.buf)-rb.max:]
		if i := bytes.IndexByte(rb.buf, '\n'); i >= 0 && i+1 <= len(rb.buf) {
			rb.buf = rb.buf[i+1:]
		}
	}
	return len(p), nil
}

// Bytes returns a copy of the current buffer contents.
func (rb *RingBuffer) Bytes() []byte {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	out := make([]byte, len(rb.buf))
	copy(out, rb.buf)
	return out
}
