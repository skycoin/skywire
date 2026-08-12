// Package commands cmd/apps/skychat/commands/pairing_control_test.go
//
// Unit coverage for the pair-control envelope handling on the legacy
// direct path: the pending-invite bookkeeping helpers, the PK/whitespace
// utilities, and handlePairControlFrame's fast-reject + invite/ack/decline
// dispatch (with a fake visor.API in place of the real RPC).
package commands

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor"
)

// clearPending resets the package-level pending-invite set so each test
// starts clean (the map is a process global).
func clearPending() {
	pendingInvitesMu.Lock()
	pendingInvites = map[cipher.PubKey]pendingInvite{}
	pendingInvitesMu.Unlock()
}

func TestPendingInviteHelpers(t *testing.T) {
	clearPending()
	pk, _ := cipher.GenerateKeyPair()
	if pendingHas(pk) {
		t.Fatal("fresh set should not contain the invite")
	}
	pendingPut(pk)
	if !pendingHas(pk) {
		t.Error("after pendingPut, pendingHas should be true")
	}
	list := pendingList()
	if len(list) != 1 || list[0].PeerPK != pk {
		t.Errorf("pendingList = %+v, want one entry for %s", list, pk.Hex())
	}
	pendingDelete(pk)
	if pendingHas(pk) {
		t.Error("after pendingDelete, pendingHas should be false")
	}
}

func TestParsePK(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	got, err := parsePK(pk.Hex())
	if err != nil || got != pk {
		t.Fatalf("valid pk: got=%s err=%v", got.Hex(), err)
	}
	if _, err := parsePK("not-a-pk"); err == nil {
		t.Error("invalid pk should error")
	}
}

func TestBytesTrimSpace(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  {x", "{x"},
		{"\t\n\r {x", "{x"},
		{"{x", "{x"},
		{"   ", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := string(bytesTrimSpace([]byte(c.in))); got != c.want {
			t.Errorf("bytesTrimSpace(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// pairControlAPI is a fake visor.API that records the pair mutations
// handlePairControlFrame drives through the pair RPC. Only the two
// methods the ack/decline branches call are implemented; any other
// call panics via the nil-embedded interface, which is fine — the
// tested branches don't reach them.
type pairControlAPI struct {
	visorAPIShim
	markedActive []cipher.PubKey
	removed      []cipher.PubKey
}

func (a *pairControlAPI) PairMarkActive(pk cipher.PubKey) error {
	a.markedActive = append(a.markedActive, pk)
	return nil
}

func (a *pairControlAPI) PairRemove(pk cipher.PubKey) error {
	a.removed = append(a.removed, pk)
	return nil
}

// withPairControlEnv turns pairing on, installs an SSE hub (notifyInviteSSE
// needs one) and the fake RPC, and restores all globals on cleanup.
func withPairControlEnv(t *testing.T, fake visor.API) {
	t.Helper()
	origEnable, origHub := pairEnable, hub
	pairEnable = true
	hub = newSSEHub()
	withFakePairRPC(t, fake) // sets appLog, swaps pairRPC, restores on cleanup
	t.Cleanup(func() {
		pairEnable = origEnable
		hub = origHub
	})
}

func TestHandlePairControlFrame_DisabledReturnsFalse(t *testing.T) {
	clearPending()
	pk, _ := cipher.GenerateKeyPair()
	invite, _ := json.Marshal(pairMsg{Type: pairTypeInvite}) //nolint:errcheck

	origEnable := pairEnable
	pairEnable = false
	t.Cleanup(func() { pairEnable = origEnable })

	if handlePairControlFrame(context.Background(), pk, invite) {
		t.Error("disabled pairing should return false (falls through to text path)")
	}
}

func TestHandlePairControlFrame_Types(t *testing.T) {
	fake := &pairControlAPI{}
	withPairControlEnv(t, fake)
	peer, _ := cipher.GenerateKeyPair()

	// Non-envelope / malformed / unknown-type -> not consumed.
	for _, raw := range [][]byte{
		[]byte("plain hello"),
		[]byte("{malformed"),
		[]byte(`{"type":"not-a-pair-type"}`),
		[]byte(`{}`),
	} {
		clearPending()
		if handlePairControlFrame(context.Background(), peer, raw) {
			t.Errorf("payload %q should not be consumed", raw)
		}
	}

	// pair-invite -> consumed, stored pending, no RPC mutation.
	clearPending()
	invite, _ := json.Marshal(pairMsg{Type: pairTypeInvite}) //nolint:errcheck
	if !handlePairControlFrame(context.Background(), peer, invite) {
		t.Fatal("pair-invite should be consumed")
	}
	if !pendingHas(peer) {
		t.Error("pair-invite should store a pending invite")
	}
	if len(fake.markedActive) != 0 || len(fake.removed) != 0 {
		t.Error("pair-invite must not mutate pair state via RPC")
	}

	// pair-ack -> PairMarkActive(peer).
	ack, _ := json.Marshal(pairMsg{Type: pairTypeAck}) //nolint:errcheck
	if !handlePairControlFrame(context.Background(), peer, ack) {
		t.Fatal("pair-ack should be consumed")
	}
	if len(fake.markedActive) != 1 || fake.markedActive[0] != peer {
		t.Errorf("pair-ack should PairMarkActive(peer), got %v", fake.markedActive)
	}

	// pair-decline -> PairRemove(peer).
	decline, _ := json.Marshal(pairMsg{Type: pairTypeDecline}) //nolint:errcheck
	if !handlePairControlFrame(context.Background(), peer, decline) {
		t.Fatal("pair-decline should be consumed")
	}
	if len(fake.removed) != 1 || fake.removed[0] != peer {
		t.Errorf("pair-decline should PairRemove(peer), got %v", fake.removed)
	}
}

// An invite is announced once, however many times it arrives and however
// often the page reconnects. Reported as "every time I open this
// interface, it pops up so many Peer invites from PK…", with a second
// phone showing one — the count tracked invite events still in the SSE
// replay ring, not invites outstanding.
func TestHandlePairControlFrame_InviteAnnouncedOnce(t *testing.T) {
	fake := &pairControlAPI{}
	withPairControlEnv(t, fake)
	clearPending()
	peer, _ := cipher.GenerateKeyPair()
	invite, _ := json.Marshal(pairMsg{Type: pairTypeInvite}) //nolint:errcheck

	open, unsub := hub.subscribe()
	defer unsub()

	if !handlePairControlFrame(context.Background(), peer, invite) {
		t.Fatal("pair-invite should be consumed")
	}
	if got := drainStrings(open); len(got) != 1 {
		t.Fatalf("first invite announced %d times, want exactly 1: %v", len(got), got)
	}

	// The peer retries — a reconnect, a resend. The invite is already in
	// the sidebar, so there is nothing new to say about it.
	if !handlePairControlFrame(context.Background(), peer, invite) {
		t.Fatal("a repeat pair-invite should still be consumed")
	}
	if got := drainStrings(open); len(got) != 0 {
		t.Errorf("repeat invite announced again: %v", got)
	}
	if !pendingHas(peer) {
		t.Error("a repeat invite must still keep the invite pending — the resend " +
			"is what carries it across a dropped connection")
	}

	// The page is reopened. This is the reported bug: it used to be handed
	// every past invite envelope out of the replay ring and toast for each.
	reopened, reopenedUnsub := hub.subscribe()
	defer reopenedUnsub()
	if got := drainStrings(reopened); len(got) != 0 {
		t.Errorf("opening the page replayed %d invite envelope(s): %v", len(got), got)
	}
}
