// Package history — cmd/apps/skychat/history/group_since_test.go:
// pins the BoltStore.ListGroupSince contract added so the gRPC
// StreamGroupMessages handler can backfill a reconnecting subscriber
// whose disconnect gap exceeds the in-memory inbox ring. Without
// this query, every operator who actually opted into GroupHistoryDB
// still saw the inbox-bounded behavior — the persistence existed
// but was unreachable from the streaming path.
package history

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestBoltStore_ListGroupSince_Empty(t *testing.T) {
	// Unknown group → nil, nil (matches ListByGroup's unknown-group
	// shape).
	s := newTestStore(t, DefaultLimits())
	msgs, err := s.ListGroupSince("unknown-group", time.Time{})
	if err != nil {
		t.Fatalf("ListGroupSince unknown: %v", err)
	}
	if msgs != nil {
		t.Errorf("ListGroupSince unknown: want nil, got %v", msgs)
	}
}

func TestBoltStore_ListGroupSince_EmptyGroupID(t *testing.T) {
	// Empty groupID arg → nil (defensive, matches ListByGroup).
	s := newTestStore(t, DefaultLimits())
	msgs, err := s.ListGroupSince("", time.Time{})
	if err != nil {
		t.Fatalf("ListGroupSince empty-id: %v", err)
	}
	if msgs != nil {
		t.Errorf("ListGroupSince empty-id: want nil, got %v", msgs)
	}
}

func TestBoltStore_ListGroupSince_ZeroTimeReturnsAll(t *testing.T) {
	// since == zero-time → every message in the group qualifies
	// because every Timestamp.After(zero) is true. Acts as a
	// pure "every message in the group, chronological" mode.
	s := newTestStore(t, DefaultLimits())
	groupID := "11111111-2222-3333-4444-555555555555"
	sender := "03ab" + strings.Repeat("0", 62)
	base := time.Now().UTC().Truncate(time.Millisecond)
	for i := 0; i < 5; i++ {
		if err := s.AppendGroup(GroupMessage{
			GroupID:   groupID,
			SenderPK:  sender,
			Text:      fmt.Sprintf("msg-%d", i),
			Timestamp: base.Add(time.Duration(i) * time.Millisecond),
		}); err != nil {
			t.Fatalf("AppendGroup %d: %v", i, err)
		}
	}
	msgs, err := s.ListGroupSince(groupID, time.Time{})
	if err != nil {
		t.Fatalf("ListGroupSince zero: %v", err)
	}
	if len(msgs) != 5 {
		t.Fatalf("want 5 messages, got %d", len(msgs))
	}
	for i, m := range msgs {
		want := fmt.Sprintf("msg-%d", i)
		if m.Text != want {
			t.Errorf("msg %d: text = %q, want %q", i, m.Text, want)
		}
	}
}

func TestBoltStore_ListGroupSince_ReturnsOnlyAfterCursor(t *testing.T) {
	// Core contract: the returned slice contains every message with
	// Timestamp strictly greater than `since`, and nothing at or
	// before it. Pre-fix, the streaming handler had no way to ask
	// "messages since this cursor" against persisted state; the
	// only persistent reader was ListByGroup which is limit-shaped.
	s := newTestStore(t, DefaultLimits())
	groupID := "abcdef00-1111-2222-3333-444444444444"
	sender := "03cd" + strings.Repeat("e", 62)
	base := time.Now().UTC().Truncate(time.Millisecond)

	// Plant 10 messages 100ms apart.
	for i := 0; i < 10; i++ {
		if err := s.AppendGroup(GroupMessage{
			GroupID:   groupID,
			SenderPK:  sender,
			Text:      fmt.Sprintf("msg-%02d", i),
			Timestamp: base.Add(time.Duration(i) * 100 * time.Millisecond),
		}); err != nil {
			t.Fatalf("AppendGroup %d: %v", i, err)
		}
	}

	// Cursor at message 4's timestamp — expect msgs 5..9 back.
	cursor := base.Add(4 * 100 * time.Millisecond)
	msgs, err := s.ListGroupSince(groupID, cursor)
	if err != nil {
		t.Fatalf("ListGroupSince: %v", err)
	}
	if len(msgs) != 5 {
		t.Fatalf("want 5 messages since msg-04's TS, got %d (texts: %v)", len(msgs), texts(msgs))
	}
	for i, m := range msgs {
		want := fmt.Sprintf("msg-%02d", i+5)
		if m.Text != want {
			t.Errorf("msg %d post-cursor: text = %q, want %q", i, m.Text, want)
		}
	}
}

func TestBoltStore_ListGroupSince_CursorAtNewest(t *testing.T) {
	// Cursor at-or-past the newest message → empty result. Confirms
	// the streaming handler's "no backlog to replay" signal works
	// correctly when the subscriber's cursor is fully caught up.
	s := newTestStore(t, DefaultLimits())
	groupID := "deadbeef-cafe-babe-feed-decadeaffaced"
	sender := "0312" + strings.Repeat("3", 62)
	base := time.Now().UTC().Truncate(time.Millisecond)
	for i := 0; i < 3; i++ {
		if err := s.AppendGroup(GroupMessage{
			GroupID:   groupID,
			SenderPK:  sender,
			Text:      fmt.Sprintf("msg-%d", i),
			Timestamp: base.Add(time.Duration(i) * time.Millisecond),
		}); err != nil {
			t.Fatalf("AppendGroup %d: %v", i, err)
		}
	}
	// Cursor past the newest message.
	cursor := base.Add(10 * time.Second)
	msgs, err := s.ListGroupSince(groupID, cursor)
	if err != nil {
		t.Fatalf("ListGroupSince: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("cursor past newest: want 0 messages, got %d (texts: %v)", len(msgs), texts(msgs))
	}
}

func TestBoltStore_ListGroupSince_GroupIsolation(t *testing.T) {
	// A cursor against group A must not return group B's messages.
	// The bbolt schema buckets per-group; this test guards against
	// a future refactor that accidentally crosses the boundary
	// (e.g. a global tsKey index that doesn't scope to group).
	s := newTestStore(t, DefaultLimits())
	groupA := "00000000-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	groupB := "00000000-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	sender := "0345" + strings.Repeat("6", 62)
	base := time.Now().UTC().Truncate(time.Millisecond)

	for i := 0; i < 3; i++ {
		ts := base.Add(time.Duration(i) * time.Millisecond)
		for _, gid := range []string{groupA, groupB} {
			if err := s.AppendGroup(GroupMessage{
				GroupID:   gid,
				SenderPK:  sender,
				Text:      fmt.Sprintf("%s/msg-%d", gid[:8], i),
				Timestamp: ts,
			}); err != nil {
				t.Fatalf("AppendGroup %s/%d: %v", gid, i, err)
			}
		}
	}

	msgs, err := s.ListGroupSince(groupA, time.Time{})
	if err != nil {
		t.Fatalf("ListGroupSince groupA: %v", err)
	}
	for _, m := range msgs {
		if m.GroupID != groupA {
			t.Errorf("groupA query returned message with GroupID=%q (cross-group leak)", m.GroupID)
		}
	}
	if len(msgs) != 3 {
		t.Errorf("groupA: want 3 messages, got %d", len(msgs))
	}
}

func texts(msgs []GroupMessage) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Text)
	}
	return out
}
