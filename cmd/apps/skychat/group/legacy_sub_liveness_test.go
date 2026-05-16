// Package group — cmd/apps/skychat/group/legacy_sub_liveness_test.go:
// targeted coverage for the legacy s.sub liveness accessor +
// reconnect wrapper added to extend the per-peerSub liveness machinery
// (#2606) to cover the D1 owner-feed subscriber. The full reconnect
// wiring rides Session.Open / treestore CXO infra and is exercised by
// the manual E2E plan; this file pins the accessor contracts the
// stale-check branch in detectStaleAndReconnect now depends on.
package group

import (
	"context"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cxo/treestore"
)

// nonNilSubSentinel returns a *treestore.Subscriber whose only
// purpose is to satisfy s.sub != nil for HasLegacySub /
// LegacySubLastInbound. Never call Connect on the returned value —
// the zero-value Subscriber has no node attached and will panic.
func nonNilSubSentinel() *treestore.Subscriber {
	return &treestore.Subscriber{}
}

func TestLegacySubLastInboundZeroOnOwnerSession(t *testing.T) {
	// Owner-role sessions never carry a legacy owner-feed subscriber
	// (s.sub is nil by construction in openOwner). Accessor must
	// short-circuit to zero time so the detectStaleAndReconnect
	// branch never enrolls owner sessions in the legacy-sub stale
	// loop. HasLegacySub mirrors this so callers can fast-skip
	// without dereferencing the time.
	s := &Session{}
	if !s.LegacySubLastInbound().IsZero() {
		t.Errorf("owner session: LegacySubLastInbound should be zero, got %v", s.LegacySubLastInbound())
	}
	if s.HasLegacySub() {
		t.Errorf("owner session: HasLegacySub should be false, got true")
	}
}

func TestLegacySubLastInboundReturnsStoredTime(t *testing.T) {
	// On member sessions where s.sub is non-nil and subLastInboundNs
	// has been bumped (via the makeLegacySubOnUpdate wrapper firing
	// or via a successful Connect / ReconnectLegacySub), the
	// accessor must reflect the stored timestamp so the stale-check
	// branch computes lag correctly. Uses a zero-value Subscriber
	// sentinel — neither accessor dereferences it, so the lack of
	// a real CXO node attached is fine.
	s := &Session{}
	s.sub = nonNilSubSentinel()
	now := time.Now().UTC().Truncate(time.Microsecond)
	s.subLastInboundNs.Store(now.UnixNano())
	got := s.LegacySubLastInbound()
	if got.IsZero() {
		t.Fatalf("LegacySubLastInbound returned zero with subLastInboundNs=%d", now.UnixNano())
	}
	if !got.Equal(now) {
		t.Errorf("LegacySubLastInbound got %v want %v", got, now)
	}
	if !s.HasLegacySub() {
		t.Errorf("HasLegacySub: expected true with s.sub set")
	}
}

func TestLegacySubLastInboundZeroWhenSubNilEvenIfNsSet(t *testing.T) {
	// Defensive: if a future refactor leaves subLastInboundNs bumped
	// after tearing s.sub down (e.g. SetAllowlist drops the legacy
	// subscriber), the accessor must still report zero — operators
	// reading IsSubscriberAlive should see "no legacy sub" rather
	// than a stale-but-non-zero timestamp that maps to "alive".
	s := &Session{}
	s.subLastInboundNs.Store(time.Now().UnixNano())
	if !s.LegacySubLastInbound().IsZero() {
		t.Errorf("nil-sub session: LegacySubLastInbound should be zero, got %v", s.LegacySubLastInbound())
	}
}

func TestReconnectLegacySubNoopOnOwnerSession(t *testing.T) {
	// Manager.tryReconnectLegacySub is gated by HasLegacySub, but
	// Session.ReconnectLegacySub also has to be safe for direct
	// callers that haven't checked. Confirms the no-op + nil-error
	// contract for the s.sub == nil branch.
	s := &Session{}
	if err := s.ReconnectLegacySub(context.Background()); err != nil {
		t.Errorf("ReconnectLegacySub on owner session: want nil err, got %v", err)
	}
	if !s.LegacySubLastInbound().IsZero() {
		t.Errorf("ReconnectLegacySub on owner session: must not bump subLastInboundNs")
	}
}

func TestReconnectLegacySubHonorsCanceledContext(t *testing.T) {
	// A canceled context must return ctx.Err() without dialing —
	// mirrors ReconnectPeer's pre-check. We can't exercise the
	// actual Connect path without CXO infra, but the early-return
	// branch is deterministic and worth pinning so a refactor
	// doesn't accidentally drop the ctx.Err() check.
	s := &Session{}
	s.sub = nonNilSubSentinel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := s.ReconnectLegacySub(ctx)
	if err == nil {
		t.Fatalf("ReconnectLegacySub: want ctx.Err() on canceled context, got nil")
	}
	if err != context.Canceled {
		t.Errorf("ReconnectLegacySub: want context.Canceled, got %v", err)
	}
}
