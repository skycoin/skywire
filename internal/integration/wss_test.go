//go:build !no_ci
// +build !no_ci

// Package integration_test — pkg/../internal/integration/wss_test.go: end-to-end
// coverage for the WebSocket (swsr) skywire transport across real visor
// containers.
//
// This closes the "WS: no e2e" gap from the coverage report by driving the
// production dial path — CLI `tp add --type swsr` → address-resolver lookup →
// direct WebSocket handshake → transport establishment — between two live visors
// on the Docker network.
//
// WS has no dedicated listener: it rides the visor's stcpr TCP port via a cmux
// (see init_transport.go initWSClient), so the peer's stcpr address-resolver
// record IS its ws:// endpoint (resolveWSURLViaAR forms ws://host:port/). Because
// it reuses the directly-reachable stcpr endpoint with no STUN/hole-punch, on the
// Docker network it behaves like STCPR/QUIC and is expected to succeed (unlike
// SUDPH, which needs hole-punching).
package integration_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	types "github.com/skycoin/skywire/pkg/transport/types"
)

// TestEnv_WSTransport creates a WebSocket (swsr) transport from visor-b to
// visor-a and visor-c, asserts the transport is established with the correct type
// and remote PK, confirms it is visible in the transport list, then tears it down.
func TestEnv_WSTransport(t *testing.T) {
	start := time.Now()
	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC})

	// Wait for DMSG discovery entries so every visor is registered and its
	// address-resolver client is live — a WS dial-by-PK resolves the remote's
	// stcpr record through the AR, so the peers must be discoverable (and have
	// registered their stcpr endpoint) before a transport can be added.
	for _, visor := range []string{visorA, visorB, visorC} {
		err := env.WaitForDmsgDiscoveryEntry(visor, 120*time.Second)
		if err != nil {
			t.Logf("Failed to find DMSG discovery entry for %s: %v", visor, err)
			if logs, logErr := env.ReadLog(visor); logErr == nil {
				t.Logf("Logs for %s:\n%s", visor, logs)
			}
		}
		require.NoError(t, err, "Visor %s not found in DMSG discovery", visor)
	}

	// WS resolves via the peer's stcpr AR record, which is registered
	// asynchronously after startup; the retry envelope (5 attempts with
	// increasing delay) absorbs that lag.
	const maxRetries = 5

	for _, remote := range []string{visorA, visorC} {
		pk := env.visorPKs[remote]

		addTpSum, err := env.VisorTpAddWithRetry(visorB, pk, types.WS, maxRetries)
		if err != nil {
			// WS is expected to work on the directly-reachable Docker network
			// (it reuses the stcpr endpoint, no hole-punch), so a failure is a
			// real regression — dump both ends' logs to make it diagnosable.
			t.Logf("Failed to create WS transport from %s to %s: %v", visorB, remote, err)
			for _, v := range []string{visorB, remote} {
				if logs, logErr := env.ReadLog(v); logErr == nil {
					t.Logf("Logs for %s:\n%s", v, logs)
				}
			}
		}
		require.NoError(t, err, "WS transport %s->%s must be creatable", visorB, remote)
		require.Equal(t, types.WS, addTpSum.Type, "transport type must be WS (swsr)")
		require.Contains(t, addTpSum.Remote.Hex(), pk, "remote PK mismatch on WS transport")

		// The transport must be individually resolvable by its ID and report the
		// same remote — proves it was actually registered, not just returned by add.
		tpSum, err := env.VisorTpID(visorB, addTpSum.ID)
		require.NoError(t, err)
		require.Equal(t, types.WS, tpSum.Type)
		require.Contains(t, tpSum.Remote.Hex(), pk)

		// And it must appear in the transport listing with the WS type.
		tps, err := env.VisorTpLs(visorB)
		require.NoError(t, err)
		found := false
		for _, tp := range tps {
			if tp.ID == addTpSum.ID {
				require.Equal(t, types.WS, tp.Type)
				found = true
				break
			}
		}
		require.True(t, found, "WS transport %s not found in visor-b transport list", addTpSum.ID)

		rmTpSum, err := env.VisorTpRm(visorB, addTpSum.ID)
		require.NoError(t, err)
		require.Equal(t, "OK", rmTpSum)
		t.Logf("WS transport %s->%s created, verified, and removed", visorB, remote)
	}

	t.Logf("TestEnv_WSTransport completed in %v", time.Since(start).Round(time.Second))
}
