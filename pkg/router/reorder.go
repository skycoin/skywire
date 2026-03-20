// Package router pkg/router/reorder.go
package router

import (
	"sort"
	"sync"
)

// reorderBuffer holds out-of-order packets and delivers them in sequence order.
// When mux mode distributes packets across multiple transports, they may arrive
// out of order. This buffer re-sequences them before passing to the noise layer.
type reorderBuffer struct {
	mu      sync.Mutex
	nextSeq uint32            // next expected sequence number
	buf     map[uint32][]byte // out-of-order packets: seq -> payload
	maxGap  int               // max buffered packets before force-flush
}

func newReorderBuffer(maxGap int) *reorderBuffer {
	return &reorderBuffer{
		buf:    make(map[uint32][]byte),
		maxGap: maxGap,
	}
}

// Insert adds a packet with the given sequence number. Returns payloads ready
// for in-order delivery. Common case (in-order arrival) requires no allocation.
func (rb *reorderBuffer) Insert(seq uint32, data []byte) [][]byte {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	// Duplicate or late packet — discard
	if seq < rb.nextSeq {
		return nil
	}

	// Buffer out-of-order packet
	if seq != rb.nextSeq {
		// Make a copy since the underlying packet buffer may be reused
		cp := make([]byte, len(data))
		copy(cp, data)
		rb.buf[seq] = cp

		// Force flush if buffer is too large (prevents unbounded memory)
		if len(rb.buf) >= rb.maxGap {
			return rb.flushAll()
		}
		return nil
	}

	// In-order: deliver this packet and any consecutive buffered packets
	var delivered [][]byte
	delivered = append(delivered, data)
	rb.nextSeq = seq + 1

	for {
		next, ok := rb.buf[rb.nextSeq]
		if !ok {
			break
		}
		delivered = append(delivered, next)
		delete(rb.buf, rb.nextSeq)
		rb.nextSeq++
	}

	return delivered
}

// flushAll delivers all buffered packets in sequence order and resets.
// Called when the gap exceeds maxGap to prevent unbounded memory growth.
// The caller must hold rb.mu.
func (rb *reorderBuffer) flushAll() [][]byte {
	if len(rb.buf) == 0 {
		return nil
	}

	seqs := make([]uint32, 0, len(rb.buf))
	for seq := range rb.buf {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })

	delivered := make([][]byte, 0, len(seqs))
	for _, seq := range seqs {
		delivered = append(delivered, rb.buf[seq])
		delete(rb.buf, seq)
	}

	// Advance nextSeq past all delivered
	if len(seqs) > 0 {
		rb.nextSeq = seqs[len(seqs)-1] + 1
	}

	return delivered
}

// Pending returns the number of packets currently buffered out-of-order.
func (rb *reorderBuffer) Pending() int {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return len(rb.buf)
}

// NextSeq returns the next expected sequence number (first not yet delivered in order).
func (rb *reorderBuffer) NextSeq() uint32 {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.nextSeq
}
