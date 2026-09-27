// Package commands pair_poll_revoked_test.go
package commands

import (
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skychat/pairing"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// pairPollFake stands in for the visor RPC: a fixed inbox window and a fixed
// pair listing. Built on visorAPIShim so it satisfies visorapi.API without
// implementing the rest; only the two methods the poller calls are live.
type pairPollFake struct {
	visorAPIShim
	msgs  []visorapi.PairMessage
	pairs []visorapi.PairInfo
}

func (f *pairPollFake) PairPoll(time.Time) ([]visorapi.PairMessage, error) { return f.msgs, nil }
func (f *pairPollFake) PairList() ([]visorapi.PairInfo, error)             { return f.pairs, nil }

// withPairRPC swaps the package-level pair RPC client for the test and
// restores it on cleanup.
func withPairRPC(t *testing.T, api visorapi.API) {
	t.Helper()
	pairRPCMu.Lock()
	prev := pairRPC
	pairRPC = api
	pairRPCMu.Unlock()
	t.Cleanup(func() {
		pairRPCMu.Lock()
		pairRPC = prev
		pairRPCMu.Unlock()
	})
}

// TestPairPollSkipsRevokedPeers pins the restart-replay half of "delete a
// paired chat": the startup tick re-broadcasts the visor's whole inbox window
// from a zero cursor (pair messages have no history-store copy, so that
// replay is their delivery), and a revoked pair's surviving entries are the
// conversation the user deleted. They must not go back out — on a phone the
// page re-adds a sender the moment one of their messages renders.
func TestPairPollSkipsRevokedPeers(t *testing.T) {
	withChatLog(t)
	prevHub := hub
	hub = newSSEHub()
	t.Cleanup(func() { hub = prevHub })

	gone, _ := cipher.GenerateKeyPair()
	live, _ := cipher.GenerateKeyPair()
	base := time.Date(2026, 5, 18, 9, 0, 0, 0, time.UTC)

	fake := &pairPollFake{
		msgs: []visorapi.PairMessage{
			{PeerPK: gone, Text: "old message of the deleted chat", TS: base.Add(1 * time.Second), ID: "g1"},
			{PeerPK: live, Text: "hello", TS: base.Add(2 * time.Second), ID: "l1"},
		},
		pairs: []visorapi.PairInfo{
			{PeerPK: gone, Status: pairing.StatusRevoked},
			{PeerPK: live, Status: pairing.StatusActive},
		},
	}
	withPairRPC(t, fake)

	// Zero cursor: exactly what a restarted chat-app polls first.
	since := pollPairInboxOnce(time.Time{})

	// The cursor advanced past BOTH messages — a skipped message must not be
	// reconsidered on a later tick either.
	if want := base.Add(2 * time.Second); !since.Equal(want) {
		t.Fatalf("cursor = %v, want %v", since, want)
	}

	ch, unsubscribe := hub.subscribe()
	defer unsubscribe()
	sawLive := false
	for _, m := range drain(ch) {
		if strings.Contains(m, gone.Hex()) {
			t.Errorf("a revoked pair's message was re-broadcast: %s", m)
		}
		if strings.Contains(m, live.Hex()) {
			sawLive = true
		}
	}
	if !sawLive {
		t.Error("a live pair's message was dropped by the revoked filter")
	}

	// And the structured /events ring carries the same split.
	hub.mu.Lock()
	for i := 0; i < hub.eventsLen; i++ {
		ev := hub.events[i]
		if ev.Channel == channelPair && ev.From == gone.Hex() {
			t.Errorf("a revoked pair's event was recorded: %+v", ev)
		}
	}
	n := hub.eventsLen
	hub.mu.Unlock()
	if n != 1 {
		t.Errorf("/events ring holds %d events, want 1 (the live peer's)", n)
	}
}

// TestRevokedPairSetOnlyListsRevoked: the filter key is the record's status,
// not presence in the listing — revoked records are kept for audit and DO
// appear in PairList, which is precisely how they used to pass for live.
func TestRevokedPairSetOnlyListsRevoked(t *testing.T) {
	revoked, _ := cipher.GenerateKeyPair()
	active, _ := cipher.GenerateKeyPair()
	pending, _ := cipher.GenerateKeyPair()

	withPairRPC(t, &pairPollFake{pairs: []visorapi.PairInfo{
		{PeerPK: revoked, Status: pairing.StatusRevoked},
		{PeerPK: active, Status: pairing.StatusActive},
		{PeerPK: pending, Status: pairing.StatusPending},
	}})

	set := revokedPairSet()
	if len(set) != 1 || !set[revoked.Hex()] {
		t.Fatalf("revokedPairSet = %v, want only %s", set, revoked.Hex())
	}
}

// TestRevokedPairSetEmptyWithoutRPC: a dead RPC listing must degrade to "no
// revoked peers", never to "drop everything".
func TestRevokedPairSetEmptyWithoutRPC(t *testing.T) {
	withPairRPC(t, nil)
	if set := revokedPairSet(); len(set) != 0 {
		t.Fatalf("revokedPairSet with no RPC = %v, want empty", set)
	}
}
