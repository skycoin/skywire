// Package transport pkg/transport/vstream_fin_test.go c2-net-transport
package transport

import (
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestVStreamReadDrainsBeforeEOF: whatever the peer wrote before closing is
// read before the EOF.
//
// A FIN closes the stream as it arrives, with the frames written ahead of it
// already queued. Read used to wait on both at once, and select picks at
// random between ready cases — so a peer that answered a request and hung up
// lost its answer about half the time. That is how a ringback tone request
// over a direct skynet conn came back as a bare EOF. Repeated because one
// lucky pick would pass the broken version.
func TestVStreamReadDrainsBeforeEOF(t *testing.T) {
	for i := 0; i < 200; i++ {
		s := &VStream{readBuf: make(chan []byte, 4), closed: make(chan struct{})}
		s.readBuf <- []byte("hello, ")
		s.readBuf <- []byte("world")
		close(s.closed) // the FIN, behind the data

		got, err := io.ReadAll(s)
		require.NoError(t, err, "round %d", i)
		require.Equal(t, "hello, world", string(got), "round %d: data written before the FIN was lost", i)
	}
}
