// Package routing pkg/routing/packet_known_test.go c2-net-routing
package routing

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPacketTypeKnownTracksString pins PacketType.Known to String: the
// transport read loop used to compare the type byte against DirectionPacket
// directly, so LegRehomePacket — added after it — was counted as a malformed
// frame and logged as "peer framing off" on every hop that carried a leg
// re-home. Adding a type without moving the bound fails here instead.
func TestPacketTypeKnownTracksString(t *testing.T) {
	for i := 0; i < int(LegRehomePacket)+8; i++ {
		pt := PacketType(i) //nolint:gosec
		named := !strings.HasPrefix(pt.String(), "Unknown(")
		require.Equalf(t, named, pt.Known(),
			"PacketType(%d) is %q: Known() and String() disagree", i, pt.String())
	}
}
