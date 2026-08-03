//go:build !js
// +build !js

// Package history pkg/skychat/history/list_group_before_test.go
// the backward page cursor, across both backends.
//
// Paging is the kind of thing that looks right and is off by one: the
// boundary is exclusive, the page order is newest-last while the walk runs
// newest-first, and a zero cursor has to mean "the newest page" rather than
// "the beginning of time". Each of those gets its own case, and the whole
// table runs against bolt and mem through the same Store interface so the
// two cannot drift.
//
// Carries the !js tag because BoltStore does.
package history

import (
	"fmt"
	"testing"
	"time"
)

// seedGroup writes n messages one second apart and returns their
// timestamps, oldest first.
func seedGroup(t *testing.T, st Store, groupID string, n int) []time.Time {
	t.Helper()
	base := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	out := make([]time.Time, 0, n)
	for i := 0; i < n; i++ {
		ts := base.Add(time.Duration(i) * time.Second)
		err := st.AppendGroup(GroupMessage{
			GroupID:   groupID,
			SenderPK:  "0248c948affc71f4dd6b0b6b47e5d5f1e0e13bc39d3f5d5f4e1f3d3a6e6c0b2b7f",
			Text:      fmt.Sprintf("msg-%02d", i),
			Timestamp: ts,
		})
		if err != nil {
			t.Fatalf("AppendGroup %d: %v", i, err)
		}
		out = append(out, ts)
	}
	return out
}

func pageTexts(msgs []GroupMessage) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Text)
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func forEachBackend(t *testing.T, fn func(t *testing.T, st Store)) {
	t.Helper()
	t.Run("bolt", func(t *testing.T) { fn(t, newTestStore(t, DefaultLimits())) })
	t.Run("mem", func(t *testing.T) { fn(t, newTestMemStore(t, DefaultLimits())) })
}

// A zero cursor must mean "the newest page", so a caller's first request
// needs no special case. If it meant "from the beginning" instead, a joiner
// to a large channel would be handed the oldest messages first — the exact
// thing chunking exists to avoid.
func TestListGroupBefore_ZeroCursorIsNewestPage(t *testing.T) {
	forEachBackend(t, func(t *testing.T, st Store) {
		seedGroup(t, st, "g1", 10)
		got, err := st.ListGroupBefore("g1", time.Time{}, 3)
		if err != nil {
			t.Fatalf("ListGroupBefore: %v", err)
		}
		want := []string{"msg-07", "msg-08", "msg-09"}
		if !eq(pageTexts(got), want) {
			t.Errorf("got %v, want %v", pageTexts(got), want)
		}
		// And it agrees with ListByGroup, which is the same question.
		byGroup, err := st.ListByGroup("g1", 3)
		if err != nil {
			t.Fatalf("ListByGroup: %v", err)
		}
		if !eq(pageTexts(got), pageTexts(byGroup)) {
			t.Errorf("zero cursor %v != ListByGroup %v", pageTexts(got), pageTexts(byGroup))
		}
	})
}

// Walking backwards page by page must reconstruct the whole group exactly
// once, in order, with no duplicate at any boundary and nothing skipped.
// This is the property the UI depends on.
func TestListGroupBefore_PagesCoverEverythingOnce(t *testing.T) {
	forEachBackend(t, func(t *testing.T, st Store) {
		const total, page = 10, 3
		seedGroup(t, st, "g1", total)

		var assembled []string
		cursor := time.Time{}
		for i := 0; i < total; i++ { // bounded: cannot loop forever
			got, err := st.ListGroupBefore("g1", cursor, page)
			if err != nil {
				t.Fatalf("page %d: %v", i, err)
			}
			if len(got) == 0 {
				break
			}
			// Pages arrive newest-last, so each one prepends.
			assembled = append(pageTexts(got), assembled...)
			cursor = got[0].Timestamp
		}
		want := make([]string, 0, total)
		for i := 0; i < total; i++ {
			want = append(want, fmt.Sprintf("msg-%02d", i))
		}
		if !eq(assembled, want) {
			t.Errorf("assembled %v, want %v", assembled, want)
		}
	})
}

// The bound is exclusive: the cursor message is the oldest the caller
// already holds, so returning it again would duplicate a row on every page
// boundary.
func TestListGroupBefore_CursorIsExclusive(t *testing.T) {
	forEachBackend(t, func(t *testing.T, st Store) {
		ts := seedGroup(t, st, "g1", 5)
		got, err := st.ListGroupBefore("g1", ts[2], 10)
		if err != nil {
			t.Fatalf("ListGroupBefore: %v", err)
		}
		want := []string{"msg-00", "msg-01"}
		if !eq(pageTexts(got), want) {
			t.Errorf("got %v, want %v (msg-02 is the cursor and must be excluded)", pageTexts(got), want)
		}
	})
}

// Reaching the start of history returns empty rather than repeating the
// first page — that is what stops the UI's scroll-back loop.
func TestListGroupBefore_ExhaustsCleanly(t *testing.T) {
	forEachBackend(t, func(t *testing.T, st Store) {
		ts := seedGroup(t, st, "g1", 3)
		got, err := st.ListGroupBefore("g1", ts[0], 10)
		if err != nil {
			t.Fatalf("ListGroupBefore: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected nothing older than the first message, got %v", pageTexts(got))
		}
	})
}

// A cursor newer than everything stored is the "before is in the future"
// case — it must return the newest page, not nothing. Reachable whenever a
// client's clock runs ahead of the writer's.
func TestListGroupBefore_FutureCursorReturnsNewest(t *testing.T) {
	forEachBackend(t, func(t *testing.T, st Store) {
		seedGroup(t, st, "g1", 5)
		got, err := st.ListGroupBefore("g1", time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), 2)
		if err != nil {
			t.Fatalf("ListGroupBefore: %v", err)
		}
		want := []string{"msg-03", "msg-04"}
		if !eq(pageTexts(got), want) {
			t.Errorf("got %v, want %v", pageTexts(got), want)
		}
	})
}

// An unknown group and a zero group id are empty answers, not errors: the
// UI asks before it knows whether anything was ever persisted.
func TestListGroupBefore_UnknownGroup(t *testing.T) {
	forEachBackend(t, func(t *testing.T, st Store) {
		for _, id := range []string{"", "no-such-group"} {
			got, err := st.ListGroupBefore(id, time.Time{}, 10)
			if err != nil {
				t.Errorf("ListGroupBefore(%q): unexpected error %v", id, err)
			}
			if len(got) != 0 {
				t.Errorf("ListGroupBefore(%q): got %v, want empty", id, pageTexts(got))
			}
		}
	})
}

// A non-positive limit means "no cap" — same convention as ListByGroup, so
// a caller can ask for a whole small group in one request.
func TestListGroupBefore_ZeroLimitReturnsAll(t *testing.T) {
	forEachBackend(t, func(t *testing.T, st Store) {
		seedGroup(t, st, "g1", 4)
		got, err := st.ListGroupBefore("g1", time.Time{}, 0)
		if err != nil {
			t.Fatalf("ListGroupBefore: %v", err)
		}
		want := []string{"msg-00", "msg-01", "msg-02", "msg-03"}
		if !eq(pageTexts(got), want) {
			t.Errorf("got %v, want %v", pageTexts(got), want)
		}
	})
}

// Pages must not leak across groups.
func TestListGroupBefore_ScopedToGroup(t *testing.T) {
	forEachBackend(t, func(t *testing.T, st Store) {
		seedGroup(t, st, "g1", 3)
		seedGroup(t, st, "g2", 3)
		got, err := st.ListGroupBefore("g1", time.Time{}, 10)
		if err != nil {
			t.Fatalf("ListGroupBefore: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("got %d messages, want 3", len(got))
		}
		for _, m := range got {
			if m.GroupID != "g1" {
				t.Errorf("page leaked a %q message into g1", m.GroupID)
			}
		}
	})
}
