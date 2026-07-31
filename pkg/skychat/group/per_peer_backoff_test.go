// Package group — cmd/apps/skychat/group/per_peer_backoff_test.go:
// targeted coverage for the per-(group, peer) reconnect backoff
// added in T2b. The full reconnect loop wiring rides the manual E2E
// plan (same convention as the rest of the manager.go tests in this
// package); these tests pin the manager's per-peer state helpers in
// isolation so a refactor doesn't regress the contract
// detectStaleAndReconnect + tryReconnectPeers depend on.
package group

import (
	"errors"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// newPeerBackoffTestManager returns a *Manager seeded with the
// minimum state the per-peer backoff helpers need — the maps —
// without any session / store / dmsg infra. The new helpers don't
// touch any other fields, so a partially-initialized Manager is
// safe for these tests.
func newPeerBackoffTestManager(t *testing.T) *Manager {
	t.Helper()
	return &Manager{
		log:                logging.MustGetLogger("skychat-group-manager-test"),
		reconnectState:     make(map[string]*reconnectState),
		peerReconnectState: make(map[string]map[cipher.PubKey]*reconnectState),
	}
}

func TestPeerReconnectShouldAttemptDefaultsTrue(t *testing.T) {
	// No prior failure recorded → should attempt. Mirrors
	// reconnectShouldAttempt's contract for the session-level path.
	m := newPeerBackoffTestManager(t)
	pk, _ := cipher.GenerateKeyPair()
	if !m.peerReconnectShouldAttempt("group-1", pk, time.Now().UTC()) {
		t.Errorf("expected true when no peer state exists, got false")
	}
}

func TestPeerReconnectRecordFailureExtendsBackoff(t *testing.T) {
	// First failure: bumps counter, no transition until
	// reconnectBackoffFailures1 threshold. At that exact failure
	// count the per-peer nextAttempt extends by
	// reconnectBackoffInterval1 (so shouldAttempt returns false
	// until that window elapses). Mirrors reconnectRecordFailure's
	// schedule — they share applyBackoffTransition.
	m := newPeerBackoffTestManager(t)
	pk, _ := cipher.GenerateKeyPair()
	id := "group-1"
	now := time.Now().UTC()
	err := errors.New("synthetic peer reconnect failure")
	for i := uint32(1); i < reconnectBackoffFailures1; i++ {
		m.peerReconnectRecordFailure(id, pk, err)
		if !m.peerReconnectShouldAttempt(id, pk, now) {
			t.Errorf("at failure=%d (below threshold), expected shouldAttempt=true", i)
		}
	}
	// Threshold cross — extends by reconnectBackoffInterval1.
	m.peerReconnectRecordFailure(id, pk, err)
	if m.peerReconnectShouldAttempt(id, pk, now) {
		t.Errorf("at failure=%d (threshold), expected shouldAttempt=false within backoff window", reconnectBackoffFailures1)
	}
	// And shouldAttempt flips back to true once the backoff window
	// passes — the underlying nextAttempt is a wall-clock time.
	future := now.Add(reconnectBackoffInterval1 + time.Second)
	if !m.peerReconnectShouldAttempt(id, pk, future) {
		t.Errorf("after backoff window elapsed, expected shouldAttempt=true")
	}
}

func TestPeerReconnectClearDropsState(t *testing.T) {
	// On per-peer reconnect SUCCESS, tryReconnectPeers calls
	// peerReconnectClear to drop the (id, peer) entry so the next
	// failure starts the schedule from zero. Confirms the entry
	// goes away AND the outer (group-keyed) map cleans up when
	// empty.
	m := newPeerBackoffTestManager(t)
	pk, _ := cipher.GenerateKeyPair()
	id := "group-1"
	// Force a known backoff state.
	for i := uint32(0); i < reconnectBackoffFailures1; i++ {
		m.peerReconnectRecordFailure(id, pk, errors.New("force-state"))
	}
	if m.peerReconnectShouldAttempt(id, pk, time.Now().UTC()) {
		t.Fatalf("test setup: expected backoff engaged after %d failures", reconnectBackoffFailures1)
	}
	m.peerReconnectClear(id, pk)
	if !m.peerReconnectShouldAttempt(id, pk, time.Now().UTC()) {
		t.Errorf("after clear, expected shouldAttempt=true (fresh state)")
	}
	if _, ok := m.peerReconnectState[id]; ok {
		t.Errorf("after clearing the only peer for group, expected outer map[id] to be deleted")
	}
}

func TestPeerReconnectIndependentBetweenPeers(t *testing.T) {
	// One wedged peer's backoff must not gate a healthy peer's
	// next-tick attempt. This is the core T2b property: pre-fix
	// the session-level backoff schedule engaged on any one peer's
	// repeated failures and locked out reconnect attempts for the
	// rest of the session.
	m := newPeerBackoffTestManager(t)
	wedged, _ := cipher.GenerateKeyPair()
	healthy, _ := cipher.GenerateKeyPair()
	id := "group-1"
	for i := uint32(0); i < reconnectBackoffFailures1; i++ {
		m.peerReconnectRecordFailure(id, wedged, errors.New("force-state"))
	}
	now := time.Now().UTC()
	if m.peerReconnectShouldAttempt(id, wedged, now) {
		t.Errorf("wedged peer: expected shouldAttempt=false in backoff window")
	}
	if !m.peerReconnectShouldAttempt(id, healthy, now) {
		t.Errorf("healthy peer: expected shouldAttempt=true (independent of wedged peer's backoff)")
	}
}

func TestPeerReconnectIndependentBetweenGroups(t *testing.T) {
	// Same peer PK in two different groups gets independent
	// backoff state. Otherwise a peer wedged in group A would
	// silently lock out reconnects in group B sharing that peer.
	m := newPeerBackoffTestManager(t)
	pk, _ := cipher.GenerateKeyPair()
	for i := uint32(0); i < reconnectBackoffFailures1; i++ {
		m.peerReconnectRecordFailure("group-a", pk, errors.New("force-state"))
	}
	now := time.Now().UTC()
	if m.peerReconnectShouldAttempt("group-a", pk, now) {
		t.Errorf("group-a state should be in backoff after %d failures", reconnectBackoffFailures1)
	}
	if !m.peerReconnectShouldAttempt("group-b", pk, now) {
		t.Errorf("group-b state should be fresh: per-(group, peer) keying")
	}
}

func TestPeerReconnectSecondThresholdExtendsLonger(t *testing.T) {
	// At reconnectBackoffFailures2 the nextAttempt extends by
	// reconnectBackoffInterval2 (the longer 30min cadence in
	// production). Pins the second threshold transition so the
	// per-peer schedule matches the session-level one.
	m := newPeerBackoffTestManager(t)
	pk, _ := cipher.GenerateKeyPair()
	for i := uint32(0); i < reconnectBackoffFailures2; i++ {
		m.peerReconnectRecordFailure("group-1", pk, errors.New("force-state"))
	}
	now := time.Now().UTC()
	if m.peerReconnectShouldAttempt("group-1", pk, now) {
		t.Errorf("at failures=%d, expected shouldAttempt=false in long-backoff window", reconnectBackoffFailures2)
	}
	// Just past the first threshold's window — should still be in
	// backoff because the second threshold extended further.
	pastShort := now.Add(reconnectBackoffInterval1 + time.Second)
	if m.peerReconnectShouldAttempt("group-1", pk, pastShort) {
		t.Errorf("after short-interval elapsed, second-threshold backoff should still be engaged")
	}
	// Past the long window — should be true again.
	pastLong := now.Add(reconnectBackoffInterval2 + time.Second)
	if !m.peerReconnectShouldAttempt("group-1", pk, pastLong) {
		t.Errorf("after long-interval elapsed, expected shouldAttempt=true")
	}
}
