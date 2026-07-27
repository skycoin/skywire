// Package history cmd/apps/skychat/history/pure_helpers_test.go
//
// Unit coverage for the pure value helpers (Limits.Validate,
// DefaultLimits, Message.String, truncate) plus the two group-store
// readers the existing suite doesn't touch (ListByGroup, Groups).
package history

import (
	"strings"
	"testing"
	"time"
)

func TestLimitsValidate(t *testing.T) {
	if err := DefaultLimits().Validate(); err != nil {
		t.Fatalf("DefaultLimits should validate: %v", err)
	}
	bad := map[string]Limits{
		"neg max size":    {MaxMessageSize: -1},
		"neg rate":        {PerPeerRatePerMin: -1},
		"neg cap":         {PerPeerCap: -1},
		"neg total":       {TotalCapBytes: -1},
		"neg ttl":         {TTL: -time.Second},
		"whitelist empty": {WhitelistOnly: true},
	}
	for name, l := range bad {
		if err := l.Validate(); err == nil {
			t.Errorf("%s: Validate should error", name)
		}
	}
	ok := Limits{WhitelistOnly: true, Whitelist: map[string]bool{"pk": true}}
	if err := ok.Validate(); err != nil {
		t.Errorf("whitelist with entries should validate: %v", err)
	}
}

func TestDefaultLimits(t *testing.T) {
	l := DefaultLimits()
	if l.MaxMessageSize != 4*1024 {
		t.Errorf("MaxMessageSize = %d, want 4096", l.MaxMessageSize)
	}
	if l.PerPeerRatePerMin != 20 {
		t.Errorf("PerPeerRatePerMin = %d, want 20", l.PerPeerRatePerMin)
	}
	if l.PerPeerCap != 500 {
		t.Errorf("PerPeerCap = %d, want 500", l.PerPeerCap)
	}
	if l.TotalCapBytes != 10*1024*1024 {
		t.Errorf("TotalCapBytes = %d, want 10MB", l.TotalCapBytes)
	}
	if l.TTL != 30*24*time.Hour {
		t.Errorf("TTL = %v, want 30d", l.TTL)
	}
	if l.WhitelistOnly {
		t.Error("WhitelistOnly should default to false")
	}
}

func TestMessageString(t *testing.T) {
	ts := time.Date(2026, 7, 23, 10, 30, 0, 0, time.UTC)
	in := Message{Peer: "peerpk", From: "peerpk", Text: "hello", Timestamp: ts}
	s := in.String()
	if !strings.Contains(s, "<-") || !strings.Contains(s, "peerpk") || !strings.Contains(s, "hello") {
		t.Errorf("incoming String() = %q, want <- arrow + peer + text", s)
	}
	if !strings.Contains(s, "2026-07-23T10:30:00Z") {
		t.Errorf("String() = %q, want an RFC3339 timestamp", s)
	}
	out := Message{Peer: "peerpk", Outgoing: true, Text: "hi", Timestamp: ts}
	if !strings.Contains(out.String(), "->") {
		t.Errorf("outgoing String() = %q, want -> arrow", out.String())
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"short", 60, "short"},
		{"exactly-five", 12, "exactly-five"}, // len == n: no truncation
		{"abcdef", 3, "abc…"},
		{"", 5, ""},
	}
	for _, c := range cases {
		if got := truncate(c.in, c.n); got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestBoltStore_ListByGroupAndGroups(t *testing.T) {
	s := newTestStore(t, DefaultLimits())
	now := time.Now().UTC()
	writes := []struct{ gid, text string }{
		{"g1", "a"}, {"g1", "b"}, {"g2", "c"},
	}
	for i, w := range writes {
		if err := s.AppendGroup(GroupMessage{
			GroupID:   w.gid,
			SenderPK:  "sender",
			Text:      w.text,
			Timestamp: now.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("AppendGroup %d: %v", i, err)
		}
	}

	g1, err := s.ListByGroup("g1", 0)
	if err != nil {
		t.Fatalf("ListByGroup: %v", err)
	}
	if len(g1) != 2 {
		t.Errorf("g1 messages = %d, want 2", len(g1))
	}
	// Empty groupID returns nothing (no cross-group scan).
	if got, _ := s.ListByGroup("", 0); len(got) != 0 { //nolint
		t.Errorf(`ListByGroup("") = %d, want 0`, len(got))
	}

	groups, err := s.Groups()
	if err != nil {
		t.Fatalf("Groups: %v", err)
	}
	if len(groups) != 2 {
		t.Errorf("Groups = %v, want 2 (g1, g2)", groups)
	}
}
