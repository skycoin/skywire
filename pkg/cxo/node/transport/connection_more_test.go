package transport

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// frame builds a length-prefixed message matching Connection's wire format.
func frame(payload []byte) []byte {
	h := make([]byte, 4)
	binary.BigEndian.PutUint32(h, uint32(len(payload))) //nolint
	return append(h, payload...)
}

// TestConnectionAccessors covers the simple getters and idempotent Close.
func TestConnectionAccessors(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close() //nolint:errcheck

	c := newConnection(a, true)
	assert.True(t, c.IsTCP())
	assert.False(t, c.IsClosed())
	assert.NotNil(t, c.GetRemoteAddr())

	c.Close()
	assert.True(t, c.IsClosed())
	c.Close() // idempotent — must not panic or double-close
}

// TestConnectionWrite verifies a message sent on the out channel is framed and
// written to the underlying conn.
func TestConnectionWrite(t *testing.T) {
	a, b := net.Pipe()
	c := newConnection(a, false)
	defer c.Close()
	defer b.Close() //nolint:errcheck

	c.GetChanOut() <- Frame{Body: []byte("hello")}

	done := make(chan []byte, 1)
	go func() {
		header := make([]byte, 4)
		if _, err := io.ReadFull(b, header); err != nil {
			done <- nil
			return
		}
		payload := make([]byte, binary.BigEndian.Uint32(header))
		if _, err := io.ReadFull(b, payload); err != nil {
			done <- nil
			return
		}
		done <- payload
	}()

	select {
	case got := <-done:
		assert.Equal(t, []byte("hello"), got)
	case <-time.After(2 * time.Second):
		t.Fatal("writeLoop did not frame and write the message")
	}
}

// TestConnectionRead verifies a framed message arriving on the conn is delivered
// on the in channel.
func TestConnectionRead(t *testing.T) {
	a, b := net.Pipe()
	c := newConnection(a, false)
	defer c.Close()
	defer b.Close() //nolint:errcheck

	go func() { _, _ = b.Write(frame([]byte("ping"))) }() //nolint

	select {
	case got := <-c.GetChanIn():
		assert.Equal(t, []byte("ping"), got)
	case <-time.After(2 * time.Second):
		t.Fatal("readLoop did not deliver the message")
	}
}

// TestConnectionReadInvalidLength verifies a zero-length frame tears the
// connection down (readLoop exits, closing the in channel).
func TestConnectionReadInvalidLength(t *testing.T) {
	a, b := net.Pipe()
	c := newConnection(a, false)
	defer c.Close()
	defer b.Close() //nolint:errcheck

	go func() { _, _ = b.Write([]byte{0, 0, 0, 0}) }() //nolint // length 0 → reject

	select {
	case _, ok := <-c.GetChanIn():
		assert.False(t, ok, "in channel must close after an invalid length")
	case <-time.After(2 * time.Second):
		t.Fatal("readLoop did not tear down on an invalid length")
	}
	require.Eventually(t, c.IsClosed, time.Second, 10*time.Millisecond,
		"connection should be closed after readLoop exits")
}
