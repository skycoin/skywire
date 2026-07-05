//go:build !no_ci
// +build !no_ci

// Package integration_test — pkg/../internal/integration/sudph_test.go: end-to-end
// coverage for the SUDPH (sudph) skywire transport, i.e. STUN-assisted UDP hole
// punching, across real visor containers.
//
// SUDPH is the one transport that genuinely exercises NAT traversal: each visor
// discovers its public UDP address via STUN, registers it with the address
// resolver, and A↔B connect by simultaneously punching a UDP hole coordinated
// through the AR's UDP rendezvous. This was previously impossible in the Docker
// e2e because the deployment's address-resolver did not advertise a udp_address
// in /health (dmsg-only AR) — so every visor logged "SUDPH unavailable" and the
// SUDPH leg of TestEnv_Tp always skipped.
//
// The deployment AR now advertises public_udp_addr (docker/integration/services.json),
// so /health carries udp_address, visors bind SUDPH and register their
// STUN-discovered address, and the hole punch completes over the directly-reachable
// Docker network. This test asserts that path works end to end.
package integration_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	types "github.com/skycoin/skywire/pkg/transport/types"
)

// TestEnv_SUDPHTransport creates a SUDPH transport from visor-b to visor-a and
// visor-c — driving STUN discovery + AR registration + UDP hole punching —
// asserts the transport is established with the correct type and remote PK,
// confirms it is visible in the transport list, then tears it down.
func TestEnv_SUDPHTransport(t *testing.T) {
	start := time.Now()
	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC})

	// Wait for DMSG discovery entries: SUDPH registration and hole-punch
	// coordination ride the address resolver, reached over dmsg, so every visor
	// must be registered and reachable before a transport can be added.
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

	// SUDPH needs the STUN-discovered address bound and registered with the AR
	// (BindSUDPH), which happens asynchronously after the AR handshake; the retry
	// envelope (5 attempts, growing delay) absorbs that startup lag.
	const maxRetries = 5

	for _, remote := range []string{visorA, visorC} {
		pk := env.visorPKs[remote]

		addTpSum, err := env.VisorTpAddWithRetry(visorB, pk, types.SUDPH, maxRetries)
		if err != nil {
			// With the AR advertising udp_address, SUDPH is expected to work on the
			// directly-reachable Docker network — a failure now is a real regression,
			// so dump both ends' logs (incl. their SUDPH availability lines).
			t.Logf("Failed to create SUDPH transport from %s to %s: %v", visorB, remote, err)
			for _, v := range []string{visorB, remote} {
				if logs, logErr := env.ReadLog(v); logErr == nil {
					t.Logf("Logs for %s:\n%s", v, logs)
				}
			}
		}
		require.NoError(t, err, "SUDPH transport %s->%s must be creatable (STUN/hole-punch)", visorB, remote)
		require.Equal(t, types.SUDPH, addTpSum.Type, "transport type must be SUDPH")
		require.Contains(t, addTpSum.Remote.Hex(), pk, "remote PK mismatch on SUDPH transport")

		// The transport must be individually resolvable by its ID and report the
		// same remote — proves it was actually registered, not just returned by add.
		tpSum, err := env.VisorTpID(visorB, addTpSum.ID)
		require.NoError(t, err)
		require.Equal(t, types.SUDPH, tpSum.Type)
		require.Contains(t, tpSum.Remote.Hex(), pk)

		// And it must appear in the transport listing with the SUDPH type.
		tps, err := env.VisorTpLs(visorB)
		require.NoError(t, err)
		found := false
		for _, tp := range tps {
			if tp.ID == addTpSum.ID {
				require.Equal(t, types.SUDPH, tp.Type)
				found = true
				break
			}
		}
		require.True(t, found, "SUDPH transport %s not found in visor-b transport list", addTpSum.ID)

		rmTpSum, err := env.VisorTpRm(visorB, addTpSum.ID)
		require.NoError(t, err)
		require.Equal(t, "OK", rmTpSum)
		t.Logf("SUDPH transport %s->%s created, verified, and removed", visorB, remote)
	}

	t.Logf("TestEnv_SUDPHTransport completed in %v", time.Since(start).Round(time.Second))
}
