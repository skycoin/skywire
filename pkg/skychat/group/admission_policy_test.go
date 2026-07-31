// Package group pkg/skychat/group/admission_policy_test.go c4-app-chat
// tests for the Record-level admission + moderation predicates: group
// kind, derived policies, the posting-permission precedence, and the
// legacy-record migration.
package group

import (
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestKindDerivedPolicies(t *testing.T) {
	tests := []struct {
		kind      Kind
		wantMode  Mode
		wantJoin  JoinPolicy
		encrypted bool
	}{
		{KindPublic, ModePublic, JoinOpen, false},
		{KindPrivate, ModePrivate, JoinApproval, true},
	}
	for _, tt := range tests {
		r := Record{Kind: tt.kind, Mode: modeForKind(tt.kind)}
		if got := modeForKind(tt.kind); got != tt.wantMode {
			t.Errorf("%s: modeForKind = %q, want %q", tt.kind, got, tt.wantMode)
		}
		if got := r.JoinPolicy(); got != tt.wantJoin {
			t.Errorf("%s: JoinPolicy = %q, want %q", tt.kind, got, tt.wantJoin)
		}
		if got := r.Encrypted(); got != tt.encrypted {
			t.Errorf("%s: Encrypted = %v, want %v", tt.kind, got, tt.encrypted)
		}
	}
}

// A record written before Kind existed carries only Mode. Its admission
// policy must become whatever an operator would have assumed that Mode
// meant, so upgrading a visor doesn't silently open a private group or
// gate a public one.
func TestEnsureKindMigratesLegacyRecords(t *testing.T) {
	tests := []struct {
		mode Mode
		want Kind
	}{
		{ModePublic, KindPublic},
		{ModePrivate, KindPrivate},
	}
	for _, tt := range tests {
		r := Record{Mode: tt.mode}
		r.EnsureKind()
		if r.Kind != tt.want {
			t.Errorf("mode %q: EnsureKind → %q, want %q", tt.mode, r.Kind, tt.want)
		}
		// Idempotent.
		r.EnsureKind()
		if r.Kind != tt.want {
			t.Errorf("mode %q: second EnsureKind changed kind to %q", tt.mode, r.Kind)
		}
	}
	// An explicit Kind is never overwritten by the Mode fallback.
	r := Record{Mode: ModePublic, Kind: KindPrivate}
	r.EnsureKind()
	if r.Kind != KindPrivate {
		t.Errorf("EnsureKind overwrote an explicit Kind: got %q", r.Kind)
	}
}

// JoinPolicy must answer correctly even on a record that skipped
// EnsureKind — a test fixture, or any future read path that forgets it.
// Falling through to the open default there would silently un-gate a
// private group.
func TestJoinPolicyWithoutNormalization(t *testing.T) {
	r := Record{Mode: ModePrivate} // Kind deliberately empty
	if got := r.JoinPolicy(); got != JoinApproval {
		t.Errorf("unnormalized private record: JoinPolicy = %q, want %q", got, JoinApproval)
	}
}

func TestPostPolicyTracksReadOnly(t *testing.T) {
	r := Record{Kind: KindPublic}
	if got := r.PostPolicy(); got != PostAll {
		t.Errorf("PostPolicy = %q, want %q", got, PostAll)
	}
	r.ReadOnly = true
	if got := r.PostPolicy(); got != PostAdminsOnly {
		t.Errorf("read-only PostPolicy = %q, want %q", got, PostAdminsOnly)
	}
}

// CanPost's precedence is load-bearing: the reason string is shown to
// the operator, and the session-side reader gate mirrors this order, so
// the two must agree on which restriction wins.
func TestCanPostPrecedence(t *testing.T) {
	founder, _ := cipher.GenerateKeyPair()
	admin, _ := cipher.GenerateKeyPair()
	member, _ := cipher.GenerateKeyPair()

	base := Record{
		OwnerPK: founder,
		Admins:  []cipher.PubKey{founder, admin},
		Members: []cipher.PubKey{founder, admin, member},
		Kind:    KindPublic,
	}

	t.Run("plain member may post", func(t *testing.T) {
		if ok, why := base.CanPost(member); !ok {
			t.Errorf("member blocked: %s", why)
		}
	})

	t.Run("read-only stops members but not admins", func(t *testing.T) {
		r := base
		r.ReadOnly = true
		if ok, _ := r.CanPost(member); ok {
			t.Error("member could post in a read-only group")
		}
		if ok, why := r.CanPost(admin); !ok {
			t.Errorf("admin blocked by read-only they control: %s", why)
		}
		if ok, why := r.CanPost(founder); !ok {
			t.Errorf("founder blocked by read-only: %s", why)
		}
	})

	t.Run("mute applies even to an admin", func(t *testing.T) {
		r := base
		r.Muted = []cipher.PubKey{admin}
		if ok, _ := r.CanPost(admin); ok {
			t.Error("explicitly muted admin could still post")
		}
	})

	t.Run("ban outranks mute", func(t *testing.T) {
		r := base
		r.Banned = []cipher.PubKey{member}
		r.Muted = []cipher.PubKey{member}
		ok, why := r.CanPost(member)
		if ok {
			t.Fatal("banned member could post")
		}
		if want := "you are banned from this group"; why != want {
			t.Errorf("reason = %q, want the ban reason %q", why, want)
		}
	})
}

func TestMuteEffectiveFrom(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	at := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	r := Record{Muted: []cipher.PubKey{pk}, MutedSince: map[string]time.Time{pk.Hex(): at}}
	if got := r.MuteEffectiveFrom(pk); !got.Equal(at) {
		t.Errorf("MuteEffectiveFrom = %v, want %v", got, at)
	}
	// Unmuted PK, and a nil map, both answer zero rather than panicking.
	other, _ := cipher.GenerateKeyPair()
	if got := r.MuteEffectiveFrom(other); !got.IsZero() {
		t.Errorf("unmuted PK: MuteEffectiveFrom = %v, want zero", got)
	}
	if got := (Record{}).MuteEffectiveFrom(pk); !got.IsZero() {
		t.Errorf("nil MutedSince: MuteEffectiveFrom = %v, want zero", got)
	}
}

func TestStatusIsTerminal(t *testing.T) {
	terminal := []Status{StatusLeft, StatusRevoked, StatusDenied, StatusBanned}
	for _, s := range terminal {
		if !s.IsTerminal() {
			t.Errorf("%q should be terminal", s)
		}
	}
	// Awaiting approval is explicitly NOT terminal: it is unfinished,
	// not over, and the retry loop depends on that distinction.
	for _, s := range []Status{StatusPending, StatusActive, StatusAwaitingApproval} {
		if s.IsTerminal() {
			t.Errorf("%q should not be terminal", s)
		}
	}
}

func TestJoinStatusIsTerminal(t *testing.T) {
	for _, s := range []JoinStatus{JoinStatusAdmitted, JoinStatusDenied, JoinStatusBanned} {
		if !s.IsTerminal() {
			t.Errorf("%q should stop the requester re-asking", s)
		}
	}
	if JoinStatusPending.IsTerminal() {
		t.Error("pending must keep the requester asking")
	}
}
