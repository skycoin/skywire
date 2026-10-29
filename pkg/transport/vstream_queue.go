// Package transport pkg/transport/vstream_queue.go c2-net-transport
package transport

import "sync"

// frameKeep is the most slots a drained frameQueue keeps for reuse; a larger
// backing array is dropped so an idle stream holds a few hundred bytes.
const frameKeep = 16

// frameQueue is a VStream's inbound frame queue, bounded at vstreamReadBuf
// frames like the channel it replaces but grown only as frames wait. It has
// one producer (the transport read loop) and one reader.
type frameQueue struct {
	mu    sync.Mutex
	q     [][]byte
	head  int
	ready chan struct{} // a frame was queued
	space chan struct{} // a frame was taken
}

func newFrameQueue() frameQueue {
	return frameQueue{ready: make(chan struct{}, 1), space: make(chan struct{}, 1)}
}

// push queues b unless the queue is full.
func (fq *frameQueue) push(b []byte) bool {
	fq.mu.Lock()
	if len(fq.q)-fq.head >= vstreamReadBuf {
		fq.mu.Unlock()
		return false
	}
	if fq.head > 0 && len(fq.q) == cap(fq.q) {
		n := copy(fq.q, fq.q[fq.head:])
		clear(fq.q[n:])
		fq.q, fq.head = fq.q[:n], 0
	}
	fq.q = append(fq.q, b)
	fq.mu.Unlock()
	signal(fq.ready)
	return true
}

// pop takes the oldest frame.
func (fq *frameQueue) pop() ([]byte, bool) {
	fq.mu.Lock()
	if fq.head == len(fq.q) {
		fq.mu.Unlock()
		return nil, false
	}
	b := fq.q[fq.head]
	fq.q[fq.head] = nil
	fq.head++
	if fq.head == len(fq.q) {
		if cap(fq.q) > frameKeep {
			fq.q = nil
		} else {
			fq.q = fq.q[:0]
		}
		fq.head = 0
	}
	fq.mu.Unlock()
	signal(fq.space)
	return b, true
}

func (fq *frameQueue) len() int {
	fq.mu.Lock()
	defer fq.mu.Unlock()
	return len(fq.q) - fq.head
}

func signal(c chan struct{}) {
	select {
	case c <- struct{}{}:
	default:
	}
}
