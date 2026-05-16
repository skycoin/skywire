// Package group — cmd/apps/skychat/group/roster_evict_clear_test.go:
// targeted coverage for the per-peer reconnect state cleanup on
// roster eviction added as a follow-up to PR #2627. Pre-fix,
// Session.SetAllowlist tore down evicted peerSubs but the parallel
// Manager.peerReconnectState entry for that (id, peer) lingered
// until session close — a subsequent re-add picked up a stale
// backoff window that suppressed reconnects to the freshly-created
// peerSub. The new SetAllowlist signature returns evicted PKs so
// the Manager can clear those entries in lockstep.
//
// Full CXO/Session integration tests live in the manual E2E plan
// (same convention as the rest of this package's tests); these
// exercise the Manager-side cleanup contract in isolation.
package group

import (
	"errors"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestPeerReconnectClearOnRosterEvictionLetsReAddReconnect(t *testing.T) {
	// Force per-peer backoff state for a peer, then clear via the
	// eviction hook. The next shouldAttempt must return true — a
	// freshly re-added peerSub should not inherit the old backoff
	// window. This is the regression that pre-PR could happen if a
	// member was removed-then-re-added: the old per-peer state would
	// keep suppressing reconnect attempts to the fresh peerSub for
	// up to reconnectBackoffInterval2 (30min).
	m := newPeerBackoffTestManager(t)
	pk, _ := cipher.GenerateKeyPair()
	id := "group-evict-test"
	for i := uint32(0); i < reconnectBackoffFailures1; i++ {
		m.peerReconnectRecordFailure(id, pk, errors.New("force-state"))
	}
	now := time.Now().UTC()
	if m.peerReconnectShouldAttempt(id, pk, now) {
		t.Fatalf("test setup: expected backoff engaged for evict-test peer")
	}
	// Eviction path — what Manager.AddMember (and any future
	// RemoveMember) does after SetAllowlist returns a non-empty
	// evicted slice.
	m.peerReconnectClear(id, pk)
	if !m.peerReconnectShouldAttempt(id, pk, now) {
		t.Errorf("after eviction clear, expected shouldAttempt=true (fresh backoff state)")
	}
}

func TestPeerReconnectClearOneOfManyKeepsOthers(t *testing.T) {
	// Eviction of one peer must not collateral-clear another peer's
	// backoff state in the same group. The outer-map cleanup only
	// fires when the inner map is empty (covered by
	// TestPeerReconnectClearDropsState in per_peer_backoff_test.go).
	m := newPeerBackoffTestManager(t)
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	id := "group-evict-test"
	for i := uint32(0); i < reconnectBackoffFailures1; i++ {
		m.peerReconnectRecordFailure(id, a, errors.New("force-state"))
		m.peerReconnectRecordFailure(id, b, errors.New("force-state"))
	}
	m.peerReconnectClear(id, a)
	now := time.Now().UTC()
	if !m.peerReconnectShouldAttempt(id, a, now) {
		t.Errorf("after evicting a, expected a-state cleared (shouldAttempt=true)")
	}
	if m.peerReconnectShouldAttempt(id, b, now) {
		t.Errorf("after evicting a, expected b-state preserved (shouldAttempt=false in backoff)")
	}
	if _, ok := m.peerReconnectState[id]; !ok {
		t.Errorf("outer-map should still contain id while other peers remain")
	}
}
