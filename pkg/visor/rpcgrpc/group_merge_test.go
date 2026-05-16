// Package rpcgrpc — pkg/visor/rpcgrpc/group_merge_test.go: pins the
// mergeGroupBacklog contract added so StreamGroupMessages can backfill
// a reconnecting subscriber whose disconnect gap is longer than the
// in-memory inbox ring (groupInboxCap = 256) can cover. The merge
// interleaves the durable bbolt history snapshot with the inbox
// snapshot, dropping the duplicates that result from both layers
// observing the same delivery.
//
// Behavior under test:
//   - chronological output ordering (caller's lastSentNs gate requires it)
//   - exact-match dedup on (GroupID, TimestampNs, SenderPK)
//   - empty-input fast paths return the other slice verbatim (no copy)
//   - history-side TimestampNs equality preserved (handler's `<= lastSentNs`
//     skip handles the first-after-cursor case correctly)
package rpcgrpc

import (
	"reflect"
	"testing"
)

func msg(groupID, sender string, ts int64, body string) GroupMessageData {
	return GroupMessageData{GroupID: groupID, SenderPK: sender, TimestampNs: ts, Body: body}
}

func TestMergeGroupBacklog_BothEmpty(t *testing.T) {
	got := mergeGroupBacklog(nil, nil)
	if got != nil {
		t.Errorf("both-empty: want nil, got %v", got)
	}
}

func TestMergeGroupBacklog_HistoryOnlyReturnsHistory(t *testing.T) {
	// History without inbox → return history verbatim (no copy
	// required). Common case when the visor has GroupHistoryDB
	// enabled but the in-memory inbox is empty for this group.
	hist := []GroupMessageData{
		msg("g1", "p1", 100, "a"),
		msg("g1", "p1", 200, "b"),
	}
	got := mergeGroupBacklog(hist, nil)
	if !reflect.DeepEqual(got, hist) {
		t.Errorf("history-only: got %v want %v", got, hist)
	}
}

func TestMergeGroupBacklog_InboxOnlyReturnsInbox(t *testing.T) {
	// Symmetric to history-only. This is the pre-PR shape — every
	// group with persistence off goes through this branch.
	in := []GroupMessageData{
		msg("g1", "p1", 100, "a"),
		msg("g1", "p1", 200, "b"),
	}
	got := mergeGroupBacklog(nil, in)
	if !reflect.DeepEqual(got, in) {
		t.Errorf("inbox-only: got %v want %v", got, in)
	}
}

func TestMergeGroupBacklog_OverlapDedupsByKey(t *testing.T) {
	// Realistic case: same group, same publisher, same TS — inbox
	// and history both observed the message. Dedup key is the
	// (group, ts, sender) triple; message should appear once in
	// the output. lastSentNs in the handler relies on this so
	// "the message arrived at deliver()" + "the message is in
	// history" doesn't double-fire on the wire.
	hist := []GroupMessageData{
		msg("g1", "p1", 100, "a"),
		msg("g1", "p1", 200, "b"),
		msg("g1", "p1", 300, "c"),
	}
	in := []GroupMessageData{
		msg("g1", "p1", 200, "b"), // dup
		msg("g1", "p1", 300, "c"), // dup
		msg("g1", "p1", 400, "d"), // new
	}
	got := mergeGroupBacklog(hist, in)
	want := []GroupMessageData{
		msg("g1", "p1", 100, "a"),
		msg("g1", "p1", 200, "b"),
		msg("g1", "p1", 300, "c"),
		msg("g1", "p1", 400, "d"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("overlap-dedup: got %v want %v", got, want)
	}
}

func TestMergeGroupBacklog_OutputIsChronological(t *testing.T) {
	// Caller's lastSentNs gate requires chronological order (each
	// emit advances lastSentNs and subsequent in-order messages
	// pass the >-test). Interleaved input must produce a single
	// merged-ascending stream.
	hist := []GroupMessageData{
		msg("g1", "p1", 100, "h1"),
		msg("g1", "p1", 300, "h3"),
		msg("g1", "p1", 500, "h5"),
	}
	in := []GroupMessageData{
		msg("g1", "p1", 200, "i2"),
		msg("g1", "p1", 400, "i4"),
		msg("g1", "p1", 600, "i6"),
	}
	got := mergeGroupBacklog(hist, in)
	last := int64(-1)
	for i, m := range got {
		if m.TimestampNs <= last {
			t.Errorf("merge[%d] TS=%d not strictly greater than prev %d", i, m.TimestampNs, last)
		}
		last = m.TimestampNs
	}
	if n := len(got); n != 6 {
		t.Errorf("expected 6 merged messages (no dups), got %d: %v", n, got)
	}
}

func TestMergeGroupBacklog_DifferentSendersAtSameTSBothEmit(t *testing.T) {
	// Two senders publishing within the same nanosecond is rare but
	// possible (members on different visors hitting the same clock
	// tick). They must both emit — the dedup key includes SenderPK
	// specifically to keep them distinct.
	hist := []GroupMessageData{
		msg("g1", "p1", 100, "from-p1"),
	}
	in := []GroupMessageData{
		msg("g1", "p2", 100, "from-p2"),
	}
	got := mergeGroupBacklog(hist, in)
	if len(got) != 2 {
		t.Errorf("same-TS-different-senders: want 2 emits, got %d: %v", len(got), got)
	}
}

func TestMergeGroupBacklog_DifferentGroupsAtSameTSBothEmit(t *testing.T) {
	// Cross-group dedup must NOT collapse — same TS in two
	// different groups is a coincidence, not a dup. The handler
	// already filters by req.GroupId after the merge; this test
	// guards against an over-eager dedup that would silently drop
	// the "wrong group" half of the merge before the filter fires.
	hist := []GroupMessageData{
		msg("g1", "p1", 100, "g1-body"),
	}
	in := []GroupMessageData{
		msg("g2", "p1", 100, "g2-body"),
	}
	got := mergeGroupBacklog(hist, in)
	if len(got) != 2 {
		t.Errorf("cross-group same-TS: want 2 emits, got %d: %v", len(got), got)
	}
}
