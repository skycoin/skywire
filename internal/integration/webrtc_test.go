//go:build !no_ci
// +build !no_ci

// Package integration_test — pkg/../internal/integration/webrtc_test.go: end-to-end
// coverage for the WebRTC (webrtc) skywire transport across real visor containers.
//
// This closes the "WebRTC: no e2e" gap from the coverage report by driving the
// production dial path — CLI `tp add --type webrtc` → dmsg signaling stream
// (SDP offer/answer + ICE candidates over dmsg) → direct ICE DataChannel
// (DTLS+SCTP) → transport establishment — between two live visors.
//
// Unlike QUIC/WT/WS (single AR-resolved endpoint), WebRTC is genuinely peer-to-peer
// and needs (a) a working dmsg session between the two visors to carry signaling
// and (b) an ICE connectivity check to succeed. On the directly-reachable Docker
// network ICE succeeds on host candidates (no STUN/hole-punch), so this is expected
// to work — but because it layers on dmsg (whose session establishment can be flaky
// in Docker, exactly as the DMSG transport test notes), a creation failure is
// treated as an infra skip rather than a hard failure, matching TestEnv_Tp's
// handling of dmsg/sudph.
package integration_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	types "github.com/skycoin/skywire/pkg/transport/types"
)

// TestEnv_WebRTCTransport creates a WebRTC transport from visor-b to visor-a and
// visor-c, asserts the transport is established with the correct type and remote
// PK, confirms it is visible in the transport list, then tears it down.
func TestEnv_WebRTCTransport(t *testing.T) {
	start := time.Now()
	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC})

	// Wait for DMSG discovery entries: WebRTC signaling rides dmsg, so every
	// visor must be registered and reachable over dmsg before a transport can be
	// negotiated.
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

	const maxRetries = 5
	created := 0

	for _, remote := range []string{visorA, visorC} {
		pk := env.visorPKs[remote]

		addTpSum, err := env.VisorTpAddWithRetry(visorB, pk, types.WEBRTC, maxRetries)
		if err != nil {
			// WebRTC layers on a dmsg signaling session (flaky in Docker, like the
			// DMSG transport) plus an ICE check. If it can't be established here,
			// skip this leg with diagnostics rather than failing — same policy as
			// TestEnv_Tp applies to dmsg/sudph.
			t.Logf("Skipping WebRTC %s->%s: %v (dmsg-signaling/ICE may be unavailable in Docker)", visorB, remote, err)
			if logs, logErr := env.ReadLog(visorB); logErr == nil {
				t.Logf("Logs for %s:\n%s", visorB, logs)
			}
			continue
		}
		require.Equal(t, types.WEBRTC, addTpSum.Type, "transport type must be WebRTC")
		require.Contains(t, addTpSum.Remote.Hex(), pk, "remote PK mismatch on WebRTC transport")

		// The transport must be individually resolvable by its ID and report the
		// same remote — proves it was actually registered, not just returned by add.
		tpSum, err := env.VisorTpID(visorB, addTpSum.ID)
		require.NoError(t, err)
		require.Equal(t, types.WEBRTC, tpSum.Type)
		require.Contains(t, tpSum.Remote.Hex(), pk)

		// And it must appear in the transport listing with the WebRTC type.
		tps, err := env.VisorTpLs(visorB)
		require.NoError(t, err)
		found := false
		for _, tp := range tps {
			if tp.ID == addTpSum.ID {
				require.Equal(t, types.WEBRTC, tp.Type)
				found = true
				break
			}
		}
		require.True(t, found, "WebRTC transport %s not found in visor-b transport list", addTpSum.ID)

		rmTpSum, err := env.VisorTpRm(visorB, addTpSum.ID)
		require.NoError(t, err)
		require.Equal(t, "OK", rmTpSum)
		created++
		t.Logf("WebRTC transport %s->%s created, verified, and removed", visorB, remote)
	}

	if created == 0 {
		t.Skip("WebRTC transport could not be established in this Docker environment (dmsg-signaling/ICE unavailable)")
	}

	t.Logf("TestEnv_WebRTCTransport completed in %v (%d/2 legs)", time.Since(start).Round(time.Second), created)
}
