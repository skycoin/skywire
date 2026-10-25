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
	c.Kick()

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
// by ReadFrame.
func TestConnectionRead(t *testing.T) {
	a, b := net.Pipe()
	c := newConnection(a, false)
	defer c.Close()
	defer b.Close() //nolint:errcheck

	go func() { _, _ = b.Write(frame([]byte("ping"))) }() //nolint

	got, err := c.ReadFrame(2 * time.Second)
	require.NoError(t, err)
	assert.Equal(t, []byte("ping"), got)
}

// TestConnectionReadInvalidLength verifies a zero-length frame tears the
// connection down.
func TestConnectionReadInvalidLength(t *testing.T) {
	a, b := net.Pipe()
	c := newConnection(a, false)
	defer c.Close()
	defer b.Close() //nolint:errcheck

	go func() { _, _ = b.Write([]byte{0, 0, 0, 0}) }() //nolint // length 0 → reject

	_, err := c.ReadFrame(2 * time.Second)
	require.Error(t, err)
	require.True(t, c.IsClosed(), "an invalid length must close the connection")
}

// TestConnectionWriterExitsWhenIdle verifies the writer goroutine only lives
// while frames are queued.
func TestConnectionWriterExitsWhenIdle(t *testing.T) {
	a, b := net.Pipe()
	c := newConnection(a, false)
	defer c.Close()
	defer b.Close()                               //nolint:errcheck
	go func() { _, _ = io.Copy(io.Discard, b) }() //nolint

	require.False(t, c.writing.Load(), "a new connection starts no writer")
	for i := 0; i < 100; i++ {
		c.GetChanOut() <- Frame{Body: []byte("x")}
		c.Kick()
	}
	require.Eventually(t, func() bool { return !c.writing.Load() && len(c.out) == 0 },
		2*time.Second, time.Millisecond, "the writer must drain the queue and exit")
}

// TestConnectionInterruptKeepsWrites verifies Interrupt stops reads but lets
// queued frames reach the peer.
func TestConnectionInterruptKeepsWrites(t *testing.T) {
	a, b := net.Pipe()
	c := newConnection(a, false)
	defer c.Close()
	defer b.Close() //nolint:errcheck

	errc := make(chan error, 1)
	go func() {
		_, err := c.ReadFrame(0)
		errc <- err
	}()
	time.Sleep(50 * time.Millisecond)
	c.Interrupt()
	select {
	case err := <-errc:
		require.ErrorIs(t, err, ErrInterrupted)
	case <-time.After(2 * time.Second):
		t.Fatal("Interrupt did not unblock ReadFrame")
	}
	require.False(t, c.IsClosed(), "Interrupt must not close the connection")

	c.GetChanOut() <- Frame{Body: []byte("after")}
	c.Kick()
	header := make([]byte, 4)
	require.NoError(t, b.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, err := io.ReadFull(b, header)
	require.NoError(t, err)
	payload := make([]byte, binary.BigEndian.Uint32(header))
	_, err = io.ReadFull(b, payload)
	require.NoError(t, err)
	assert.Equal(t, []byte("after"), payload)
}
