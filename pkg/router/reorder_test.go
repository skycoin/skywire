package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReorderBuffer_InOrder(t *testing.T) {
	rb := newReorderBuffer(64)
	d := rb.Insert(0, []byte("a"))
	assert.Equal(t, [][]byte{[]byte("a")}, d)
	d = rb.Insert(1, []byte("b"))
	assert.Equal(t, [][]byte{[]byte("b")}, d)
	d = rb.Insert(2, []byte("c"))
	assert.Equal(t, [][]byte{[]byte("c")}, d)
	assert.Equal(t, 0, rb.Pending())
}

func TestReorderBuffer_OutOfOrder(t *testing.T) {
	rb := newReorderBuffer(64)
	// Packet 1 arrives before packet 0
	d := rb.Insert(1, []byte("b"))
	assert.Nil(t, d)
	assert.Equal(t, 1, rb.Pending())

	// Packet 0 arrives — should deliver both in order
	d = rb.Insert(0, []byte("a"))
	assert.Equal(t, [][]byte{[]byte("a"), []byte("b")}, d)
	assert.Equal(t, 0, rb.Pending())
}

func TestReorderBuffer_GapThenFill(t *testing.T) {
	rb := newReorderBuffer(64)
	// Packets arrive: 0, 2, 3, 1
	d := rb.Insert(0, []byte("a"))
	assert.Equal(t, [][]byte{[]byte("a")}, d)

	d = rb.Insert(2, []byte("c"))
	assert.Nil(t, d)

	d = rb.Insert(3, []byte("d"))
	assert.Nil(t, d)

	// Filling the gap delivers 1, 2, 3
	d = rb.Insert(1, []byte("b"))
	assert.Equal(t, [][]byte{[]byte("b"), []byte("c"), []byte("d")}, d)
}

func TestReorderBuffer_Duplicate(t *testing.T) {
	rb := newReorderBuffer(64)
	rb.Insert(0, []byte("a"))
	// Duplicate of seq 0
	d := rb.Insert(0, []byte("a_dup"))
	assert.Nil(t, d)
}

func TestReorderBuffer_ForceFlush(t *testing.T) {
	rb := newReorderBuffer(3)
	// Skip seq 0, send 1, 2, 3 — triggers flush at maxGap=3
	rb.Insert(1, []byte("b"))
	rb.Insert(2, []byte("c"))
	d := rb.Insert(3, []byte("d"))
	// Should flush all buffered in order
	assert.Equal(t, 3, len(d))
	assert.Equal(t, []byte("b"), d[0])
	assert.Equal(t, []byte("c"), d[1])
	assert.Equal(t, []byte("d"), d[2])
	assert.Equal(t, 0, rb.Pending())
}
