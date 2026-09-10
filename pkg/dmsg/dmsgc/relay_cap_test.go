// Package dmsgc pkg/dmsg/dmsgc/relay_cap_test.go c1-net-dmsg
package dmsgc

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgc/spec"
)

// The relay-slot cap is the default unless the operator names a number.
func TestRelayMaxStreams(t *testing.T) {
	require.Equal(t, dmsg.DefaultClientMaxRelayedStreams, relayMaxStreams(nil))
	require.Equal(t, dmsg.DefaultClientMaxRelayedStreams, relayMaxStreams(&spec.DmsgConfig{}))

	// A relaying visor allows what the dmsg server beside it would allow: it
	// is doing the same job for the peers attached to it.
	require.Equal(t, dmsg.DefaultMaxRelayedStreams, dmsg.DefaultClientMaxRelayedStreams)
	require.Greater(t, dmsg.DefaultClientMaxRelayedStreams, 256, "raised from the old desk-sized cap")

	// An explicit value always wins.
	require.Equal(t, 42, relayMaxStreams(&spec.DmsgConfig{RelayMaxStreams: 42}))

	// Negative refuses to relay at all, and must not be turned into a default.
	require.Equal(t, -1, relayMaxStreams(&spec.DmsgConfig{RelayMaxStreams: -1}))
}
