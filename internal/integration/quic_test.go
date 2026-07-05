//go:build !no_ci
// +build !no_ci

// Package integration_test — pkg/../internal/integration/quic_test.go: end-to-end
// coverage for the QUIC (squicr) skywire transport across real visor containers.
//
// Until now QUIC had unit-level coverage only (pkg/transport/network/quic_conn_test.go
// and pkg/dmsg/dmsg/quic_test.go). This closes the "QUIC: no e2e" gap called out in
// the coverage report by driving the production dial path — CLI `tp add --type squicr`
// → address-resolver lookup → real quic-go handshake with the PK-bound TLS identity
// → transport establishment — between two live visors on the Docker network.
//
// Unlike SUDPH (which needs UDP hole-punching / STUN and effectively always skips in
// Docker), QUIC dials the AR-published UDP address directly with an ephemeral socket
// and needs no STUN gate (see pkg/visor/init_transport.go initQuicClient), so on the
// directly-reachable Docker network it behaves like STCPR and is expected to succeed.
package integration_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	types "github.com/skycoin/skywire/pkg/transport/types"
)

// TestEnv_QUICTransport creates a QUIC (squicr) transport from visor-b to visor-a
// and visor-c, asserts the transport is established with the correct type and remote
// PK, confirms it is visible in the transport list, then tears it down.
func TestEnv_QUICTransport(t *testing.T) {
	start := time.Now()
	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC})

	// Wait for DMSG discovery entries so every visor is registered and its
	// address-resolver client is live — QUIC dial-by-PK resolves the remote's
	// UDP endpoint through the AR (BindQUIC), so the peers must be discoverable
	// before a transport can be added. Mirrors TestEnv_Tp.
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

	// QUIC registers its bound UDP port with the address resolver asynchronously
	// (initQuicClient → bindAndReRegister, best-effort with backoff), so the
	// endpoint may not be resolvable the instant the visor is up. The retry
	// envelope below (5 attempts with increasing delay) absorbs that startup lag.
	const maxRetries = 5

	for _, remote := range []string{visorA, visorC} {
		pk := env.visorPKs[remote]

		addTpSum, err := env.VisorTpAddWithRetry(visorB, pk, types.QUIC, maxRetries)
		if err != nil {
			// QUIC is expected to work on the directly-reachable Docker network
			// (no hole-punch needed), so a failure is a real regression — dump
			// both ends' logs to make it diagnosable rather than swallowing it.
			t.Logf("Failed to create QUIC transport from %s to %s: %v", visorB, remote, err)
			for _, v := range []string{visorB, remote} {
				if logs, logErr := env.ReadLog(v); logErr == nil {
					t.Logf("Logs for %s:\n%s", v, logs)
				}
			}
		}
		require.NoError(t, err, "QUIC transport %s->%s must be creatable", visorB, remote)
		require.Equal(t, types.QUIC, addTpSum.Type, "transport type must be QUIC (squicr)")
		require.Contains(t, addTpSum.Remote.Hex(), pk, "remote PK mismatch on QUIC transport")

		// The transport must be individually resolvable by its ID and report the
		// same remote — proves it was actually registered, not just returned by add.
		tpSum, err := env.VisorTpID(visorB, addTpSum.ID)
		require.NoError(t, err)
		require.Equal(t, types.QUIC, tpSum.Type)
		require.Contains(t, tpSum.Remote.Hex(), pk)

		// And it must appear in the transport listing with the QUIC type.
		tps, err := env.VisorTpLs(visorB)
		require.NoError(t, err)
		found := false
		for _, tp := range tps {
			if tp.ID == addTpSum.ID {
				require.Equal(t, types.QUIC, tp.Type)
				found = true
				break
			}
		}
		require.True(t, found, "QUIC transport %s not found in visor-b transport list", addTpSum.ID)

		rmTpSum, err := env.VisorTpRm(visorB, addTpSum.ID)
		require.NoError(t, err)
		require.Equal(t, "OK", rmTpSum)
		t.Logf("QUIC transport %s->%s created, verified, and removed", visorB, remote)
	}

	t.Logf("TestEnv_QUICTransport completed in %v", time.Since(start).Round(time.Second))
}
