// Package dmsg pkg/dmsg/datagram_test.go: tests the session datagram API
// (#2607 dmsg-over-QUIC). The QUIC round-trip itself needs two live QUIC
// sessions (covered by integration/live tests); here we assert the
// non-QUIC contract.
package dmsg

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionDatagramsUnsupportedOnNonQUIC(t *testing.T) {
	// A session with no QUIC backend (yamux/smux or unset) must report no
	// datagram support and reject datagram I/O.
	sc := &SessionCommon{}

	assert.False(t, sc.SupportsDatagrams())

	err := sc.WriteDatagram([]byte("x"))
	require.ErrorIs(t, err, ErrDatagramsUnsupported)

	_, err = sc.ReadDatagram(context.Background())
	require.ErrorIs(t, err, ErrDatagramsUnsupported)
}
