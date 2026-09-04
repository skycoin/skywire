package dmsg

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLostSessionBudget covers the bookkeeping behind preferring a re-dial of
// the server whose session just dropped over moving to a different one. A
// client that reconnects to the SAME server keeps its delegated-server set
// unchanged, so no discovery entry update is published; every rotation to a
// different server forces one, which at fleet scale is the bulk of the
// discovery's entry-update load (#4086).
//
// The budget is what keeps that preference from pinning a client to a server
// that is genuinely gone.
func TestLostSessionBudget(t *testing.T) {
	ce := &Client{}
	srv := mkPK(t)

	// Nothing dropped yet.
	_, ok := ce.takeLostSession()
	require.False(t, ok)

	// A drop is retried up to lostSessionMaxRetries times.
	ce.noteLostSession(srv)
	for i := 0; i < lostSessionMaxRetries; i++ {
		pk, ok := ce.takeLostSession()
		require.True(t, ok, "attempt %d should still have budget", i)
		require.Equal(t, srv, pk)
	}

	// Budget spent: the entry is dropped rather than retried forever, so the
	// Serve loop falls through to the next candidate.
	_, ok = ce.takeLostSession()
	require.False(t, ok)
	require.Empty(t, ce.lostSession)
}

// TestLostSessionRenoteKeepsBudget ensures a flapping server cannot refresh its
// own retry budget. noteLostSession is called on every drop; if it reset the
// attempt count, a server that accepts a session and immediately drops it would
// be re-dialed indefinitely.
func TestLostSessionRenoteKeepsBudget(t *testing.T) {
	ce := &Client{}
	srv := mkPK(t)

	ce.noteLostSession(srv)
	_, ok := ce.takeLostSession()
	require.True(t, ok)

	// Re-noting the same server must not clear the attempt already charged.
	ce.noteLostSession(srv)
	require.Equal(t, 1, ce.lostSession[srv])
}

// TestClearLostSession covers the success path: once a session is restored the
// server is no longer a re-dial candidate.
func TestClearLostSession(t *testing.T) {
	ce := &Client{}
	srv := mkPK(t)

	ce.noteLostSession(srv)
	ce.clearLostSession(srv)

	_, ok := ce.takeLostSession()
	require.False(t, ok)
}
