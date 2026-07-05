//go:build !no_ci
// +build !no_ci

// Package integration_test — pkg/../internal/integration/wt_test.go: end-to-end
// coverage for the WebTransport (swtr) skywire transport across real visor
// containers.
//
// Like QUIC, WT previously had unit-level coverage only (pkg/transport/network/
// wt_native_test.go and pkg/dmsg/dmsg/wt_test.go). This closes the "WT: no e2e"
// gap from the coverage report by driving the production dial path — CLI
// `tp add --type swtr` → address-resolver lookup (ResolveWT: endpoint URL + pinned
// cert hash) → real HTTP/3-over-QUIC WebTransport handshake → transport
// establishment — between two live visors on the Docker network.
//
// WT binds its own UDP port and registers BOTH the endpoint and the self-signed
// cert's SHA-256 hash with the address resolver (BindWT); a dialing peer resolves
// that record and pins the cert exactly as a browser would (see wt.go Dial /
// wt_native.go). It dials the AR-published address directly with no STUN gate, so
// on the directly-reachable Docker network it behaves like STCPR/QUIC and is
// expected to succeed (unlike SUDPH, which needs hole-punching).
package integration_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	types "github.com/skycoin/skywire/pkg/transport/types"
)

// TestEnv_WTTransport creates a WebTransport (swtr) transport from visor-b to
// visor-a and visor-c, asserts the transport is established with the correct type
// and remote PK, confirms it is visible in the transport list, then tears it down.
func TestEnv_WTTransport(t *testing.T) {
	start := time.Now()
	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC})

	// Wait for DMSG discovery entries so every visor is registered and its
	// address-resolver client is live — WT dial-by-PK resolves the remote's
	// endpoint + cert hash through the AR (BindWT), so the peers must be
	// discoverable before a transport can be added. Mirrors TestEnv_Tp.
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

	// WT registers its bound UDP port + cert hash with the address resolver
	// asynchronously (initWTClient → BindWT, best-effort with backoff), so the
	// endpoint may not be resolvable the instant the visor is up. The retry
	// envelope below (5 attempts with increasing delay) absorbs that startup lag.
	const maxRetries = 5

	for _, remote := range []string{visorA, visorC} {
		pk := env.visorPKs[remote]

		addTpSum, err := env.VisorTpAddWithRetry(visorB, pk, types.WT, maxRetries)
		if err != nil {
			// WT is expected to work on the directly-reachable Docker network
			// (no hole-punch needed), so a failure is a real regression — dump
			// both ends' logs to make it diagnosable rather than swallowing it.
			t.Logf("Failed to create WT transport from %s to %s: %v", visorB, remote, err)
			for _, v := range []string{visorB, remote} {
				if logs, logErr := env.ReadLog(v); logErr == nil {
					t.Logf("Logs for %s:\n%s", v, logs)
				}
			}
		}
		require.NoError(t, err, "WT transport %s->%s must be creatable", visorB, remote)
		require.Equal(t, types.WT, addTpSum.Type, "transport type must be WT (swtr)")
		require.Contains(t, addTpSum.Remote.Hex(), pk, "remote PK mismatch on WT transport")

		// The transport must be individually resolvable by its ID and report the
		// same remote — proves it was actually registered, not just returned by add.
		tpSum, err := env.VisorTpID(visorB, addTpSum.ID)
		require.NoError(t, err)
		require.Equal(t, types.WT, tpSum.Type)
		require.Contains(t, tpSum.Remote.Hex(), pk)

		// And it must appear in the transport listing with the WT type.
		tps, err := env.VisorTpLs(visorB)
		require.NoError(t, err)
		found := false
		for _, tp := range tps {
			if tp.ID == addTpSum.ID {
				require.Equal(t, types.WT, tp.Type)
				found = true
				break
			}
		}
		require.True(t, found, "WT transport %s not found in visor-b transport list", addTpSum.ID)

		rmTpSum, err := env.VisorTpRm(visorB, addTpSum.ID)
		require.NoError(t, err)
		require.Equal(t, "OK", rmTpSum)
		t.Logf("WT transport %s->%s created, verified, and removed", visorB, remote)
	}

	t.Logf("TestEnv_WTTransport completed in %v", time.Since(start).Round(time.Second))
}
