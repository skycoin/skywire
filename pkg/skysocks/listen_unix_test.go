//go:build unix

// Package skysocks pkg/skysocks/listen_unix_test.go c4-app-proxy
package skysocks

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReuseListen_ConcurrentSameAddr proves the reconnect-rebind fix: with
// SO_REUSEADDR + SO_REUSEPORT two listeners can hold the SAME address at once —
// the brief within-cycle overlap where the disconnected interstitial listener
// and the live Client listener both bind the SOCKS port during handoff. A plain
// net.Listen fails the second bind with "address already in use", which is what
// dropped the app on reconnect.
func TestReuseListen_ConcurrentSameAddr(t *testing.T) {
	l1, err := ReuseListen("127.0.0.1:0")
	require.NoError(t, err)
	defer l1.Close() //nolint:errcheck
	addr := l1.Addr().String()

	l2, err := ReuseListen(addr)
	require.NoError(t, err, "second listener on the same addr must succeed (SO_REUSEADDR/PORT)")
	defer l2.Close() //nolint:errcheck

	// Sanity: a plain net.Listen on that same addr must NOT be allowed — proving
	// the success above came from the socket options, not a lax OS.
	if l3, perr := net.Listen("tcp", addr); perr == nil {
		l3.Close() //nolint:errcheck,gosec
		t.Fatalf("plain net.Listen unexpectedly bound an in-use addr; test can't distinguish the fix")
	}
}

// TestReuseListen_RebindAfterClose covers the sequential reconnect case: close a
// listener and immediately rebind the same address (what every --reconnect cycle
// does). Must succeed rather than fail on a lingering socket.
func TestReuseListen_RebindAfterClose(t *testing.T) {
	l1, err := ReuseListen("127.0.0.1:0")
	require.NoError(t, err)
	addr := l1.Addr().String()

	// Give the socket real connection state, then tear everything down.
	c, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	sc, err := l1.Accept()
	require.NoError(t, err)
	c.Close()  //nolint:errcheck,gosec
	sc.Close() //nolint:errcheck,gosec
	require.NoError(t, l1.Close())

	l2, err := ReuseListen(addr)
	require.NoError(t, err, "rebind of the same addr right after close must succeed")
	require.NoError(t, l2.Close())
}
