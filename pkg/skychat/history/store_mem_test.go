// Package history store_mem_test.go — mirrors the BoltStore coverage for the
// in-memory backend (which is the only Store under GOOS=js) plus a few
// mem-specific cases (total-bytes cap, group-since, idempotent Close). No
// build tag: MemStore compiles and these run on every platform.
package history

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func newTestMemStore(t *testing.T, limits Limits) *MemStore {
	t.Helper()
	s, err := NewMemStore(limits)
	if err != nil {
		t.Fatalf("NewMemStore: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Logf("store close: %v", err)
		}
	})
	return s
}

func TestMemStore_AppendAndListByPeer(t *testing.T) {
	s := newTestMemStore(t, DefaultLimits())
	peer := "03abc" + strings.Repeat("0", 61)
	for i := 0; i < 5; i++ {
		if err := s.Append(Message{
			Peer:      peer,
			Text:      fmt.Sprintf("msg-%d", i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Millisecond),
		}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	msgs, err := s.ListByPeer(peer, 0)
	if err != nil {
		t.Fatalf("ListByPeer: %v", err)
	}
	if len(msgs) != 5 {
		t.Fatalf("want 5 messages, got %d", len(msgs))
	}
	for i, m := range msgs {
		if want := fmt.Sprintf("msg-%d", i); m.Text != want {
			t.Errorf("msg %d: text = %q, want %q", i, m.Text, want)
		}
	}
}

func TestMemStore_MaxMessageSize(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxMessageSize = 10
	s := newTestMemStore(t, lim)
	if err := s.Append(Message{Peer: "abc", Text: strings.Repeat("x", 11)}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
	if err := s.Append(Message{Peer: "abc", Text: "short"}); err != nil {
		t.Fatalf("short message should succeed: %v", err)
	}
}

func TestMemStore_RateLimit(t *testing.T) {
	lim := DefaultLimits()
	lim.PerPeerRatePerMin = 3
	s := newTestMemStore(t, lim)
	peer := "rate-peer"
	for i := 0; i < 3; i++ {
		if err := s.Append(Message{Peer: peer, Text: "ok"}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if err := s.Append(Message{Peer: peer, Text: "overflow"}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("want ErrRateLimited, got %v", err)
	}
	if err := s.Append(Message{Peer: "other-peer", Text: "ok"}); err != nil {
		t.Fatalf("other peer should not be rate-limited: %v", err)
	}
}

func TestMemStore_PerPeerCap(t *testing.T) {
	lim := DefaultLimits()
	lim.PerPeerCap = 3
	lim.PerPeerRatePerMin = 0
	s := newTestMemStore(t, lim)
	peer := "cap-peer"
	for i := 0; i < 5; i++ {
		if err := s.Append(Message{
			Peer:      peer,
			Text:      fmt.Sprintf("m%d", i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	msgs, err := s.ListByPeer(peer, 0)
	if err != nil {
		t.Fatalf("ListByPeer: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages after cap eviction, got %d", len(msgs))
	}
	want := []string{"m2", "m3", "m4"}
	for i, m := range msgs {
		if m.Text != want[i] {
			t.Errorf("msg %d: want %q got %q", i, want[i], m.Text)
		}
	}
}

func TestMemStore_Whitelist(t *testing.T) {
	lim := DefaultLimits()
	lim.WhitelistOnly = true
	lim.Whitelist = map[string]bool{"allowed": true}
	s := newTestMemStore(t, lim)
	if err := s.Append(Message{Peer: "allowed", Text: "ok"}); err != nil {
		t.Fatalf("allowed peer should succeed: %v", err)
	}
	if err := s.Append(Message{Peer: "blocked", Text: "ok"}); !errors.Is(err, ErrNotWhitelisted) {
		t.Fatalf("want ErrNotWhitelisted, got %v", err)
	}
}

func TestMemStore_ListRecent(t *testing.T) {
	s := newTestMemStore(t, Limits{})
	now := time.Now().UTC()
	writes := []Message{
		{Peer: "a", Text: "a1", Timestamp: now.Add(1 * time.Second)},
		{Peer: "b", Text: "b1", Timestamp: now.Add(2 * time.Second)},
		{Peer: "a", Text: "a2", Timestamp: now.Add(3 * time.Second)},
		{Peer: "b", Text: "b2", Timestamp: now.Add(4 * time.Second)},
	}
	for _, m := range writes {
		if err := s.Append(m); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.ListRecent(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("want 4 messages, got %d", len(all))
	}
	want := []string{"a1", "b1", "a2", "b2"}
	for i, m := range all {
		if m.Text != want[i] {
			t.Errorf("msg %d: want %q got %q", i, want[i], m.Text)
		}
	}
	recent, err := s.ListRecent(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 || recent[0].Text != "a2" || recent[1].Text != "b2" {
		t.Errorf("unexpected recent list: %+v", recent)
	}
}

func TestMemStore_Peers(t *testing.T) {
	s := newTestMemStore(t, Limits{})
	for _, p := range []string{"a", "b", "c"} {
		if err := s.Append(Message{Peer: p, Text: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	peers, err := s.Peers()
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 3 {
		t.Fatalf("want 3 peers, got %d: %v", len(peers), peers)
	}
}

func TestMemStore_EmptyPeerRejected(t *testing.T) {
	s := newTestMemStore(t, Limits{})
	if err := s.Append(Message{Peer: "", Text: "x"}); !errors.Is(err, ErrEmptyPeer) {
		t.Fatalf("want ErrEmptyPeer, got %v", err)
	}
}

func TestMemStore_TTLSweep(t *testing.T) {
	s := newTestMemStore(t, Limits{TTL: 10 * time.Second})
	now := time.Now().UTC()
	if err := s.Append(Message{Peer: "p", Text: "old", Timestamp: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(Message{Peer: "p", Text: "new", Timestamp: now}); err != nil {
		t.Fatal(err)
	}
	s.sweep() // trigger directly rather than waiting on the ticker
	msgs, err := s.ListByPeer("p", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 message after sweep, got %d", len(msgs))
	}
	if msgs[0].Text != "new" {
		t.Errorf("sweep removed wrong message: %+v", msgs[0])
	}
}

func TestMemStore_TotalCapBytes(t *testing.T) {
	// A tiny cap: the first message writes, and once totalBytes >= cap the
	// next write is rejected with ErrStorageFull.
	s := newTestMemStore(t, Limits{TotalCapBytes: 60})
	if err := s.Append(Message{Peer: "p", Text: strings.Repeat("x", 40)}); err != nil {
		t.Fatalf("first append should succeed: %v", err)
	}
	if err := s.Append(Message{Peer: "p", Text: "y"}); !errors.Is(err, ErrStorageFull) {
		t.Fatalf("want ErrStorageFull once over cap, got %v", err)
	}
}

func TestMemStore_GroupAppendAndSince(t *testing.T) {
	s := newTestMemStore(t, Limits{})
	base := time.Now().UTC()
	for i := 0; i < 4; i++ {
		if err := s.AppendGroup(GroupMessage{
			GroupID:   "g1",
			SenderPK:  "s",
			Text:      fmt.Sprintf("g%d", i),
			Timestamp: base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("AppendGroup %d: %v", i, err)
		}
	}
	// ListByGroup newest-last.
	all, err := s.ListByGroup("g1", 0)
	if err != nil || len(all) != 4 {
		t.Fatalf("ListByGroup: len=%d err=%v", len(all), err)
	}
	if all[0].Text != "g0" || all[3].Text != "g3" {
		t.Errorf("group order wrong: %+v", groupTexts(all))
	}
	// ListGroupSince strictly-after cursor: cursor at g1's ts → g2,g3.
	since := base.Add(1 * time.Second)
	after, err := s.ListGroupSince("g1", since)
	if err != nil {
		t.Fatal(err)
	}
	if got := groupTexts(after); len(got) != 2 || got[0] != "g2" || got[1] != "g3" {
		t.Errorf("ListGroupSince returned %v, want [g2 g3]", got)
	}
	// Zero time returns all.
	zeroAll, err := s.ListGroupSince("g1", time.Time{})
	if err != nil || len(zeroAll) != 4 {
		t.Fatalf("zero-time ListGroupSince: len=%d err=%v", len(zeroAll), err)
	}
	// Unknown group → nil.
	if got, _ := s.ListGroupSince("nope", since); got != nil { //nolint
		t.Errorf("unknown group should be nil, got %v", got)
	}
	// Groups() lists the one group.
	groups, _ := s.Groups() //nolint
	if len(groups) != 1 || groups[0] != "g1" {
		t.Errorf("Groups() = %v, want [g1]", groups)
	}
}

func TestMemStore_GroupIsolation(t *testing.T) {
	// A 1:1 message and a group message with the same key must not leak
	// across ListByPeer / ListByGroup.
	s := newTestMemStore(t, Limits{})
	if err := s.Append(Message{Peer: "x", Text: "dm"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendGroup(GroupMessage{GroupID: "x", SenderPK: "x", Text: "grp"}); err != nil {
		t.Fatal(err)
	}
	dm, _ := s.ListByPeer("x", 0) //nolint
	if len(dm) != 1 || dm[0].Text != "dm" {
		t.Errorf("ListByPeer leaked group traffic: %+v", dm)
	}
	grp, _ := s.ListByGroup("x", 0) //nolint
	if len(grp) != 1 || grp[0].Text != "grp" {
		t.Errorf("ListByGroup leaked dm traffic: %+v", grp)
	}
}

func TestMemStore_CloseIdempotent(t *testing.T) {
	s, err := NewMemStore(Limits{TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second close should be a no-op: %v", err)
	}
}

// texts extracts the Text field from group messages for concise assertions.
func groupTexts(msgs []GroupMessage) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Text
	}
	return out
}
