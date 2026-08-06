// Package visorcore pkg/visor/visorcore/dmsg_test.go
package visorcore

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestDmsgServicePKs verifies the shared direct-client dmsg service-PK
// derivation both the native visor and the wasm edge use: the seven service
// URLs are parsed to their PKs, empty/unparseable entries are skipped, and
// RouteSetupNodes is NOT folded in (the callers add it, or not, themselves).
func TestDmsgServicePKs(t *testing.T) {
	// Two distinct valid PKs (real curve points) reused across the URL slots.
	pubA, _ := cipher.GenerateKeyPair()
	pubB, _ := cipher.GenerateKeyPair()
	pkA := pubA.Hex()
	pkB := pubB.Hex()

	tests := []struct {
		name string
		svc  Services
		want int // number of PKs expected
	}{
		{
			name: "all seven present → 7 pks",
			svc: Services{
				DmsgDiscoveryDmsg:      "dmsg://" + pkA + ":80",
				TransportDiscoveryDmsg: "dmsg://" + pkA + ":80",
				AddressResolverDmsg:    "dmsg://" + pkA + ":80",
				RouteFinderDmsg:        "dmsg://" + pkA + ":80",
				ServiceDiscoveryDmsg:   "dmsg://" + pkA + ":80",
				ConfDmsg:               "dmsg://" + pkA + ":80",
				UptimeTrackerDmsg:      "dmsg://" + pkA + ":80",
			},
			want: 7,
		},
		{
			name: "empty + unparseable slots skipped",
			svc: Services{
				DmsgDiscoveryDmsg:      "dmsg://" + pkA + ":80",
				TransportDiscoveryDmsg: "",               // skipped
				AddressResolverDmsg:    "not-a-dmsg-url", // skipped
				RouteFinderDmsg:        "dmsg://" + pkB + ":80",
			},
			want: 2,
		},
		{
			name: "route-setup nodes are NOT included",
			svc: Services{
				DmsgDiscoveryDmsg: "dmsg://" + pkA + ":80",
				RouteSetupNodes:   []cipher.PubKey{pubA, pubA},
			},
			want: 1, // only the one service URL; RouteSetupNodes excluded
		},
		{
			name: "no dmsg services → nil",
			svc:  Services{},
			want: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DmsgServicePKs(tc.svc)
			require.Len(t, got, tc.want)
		})
	}
}
