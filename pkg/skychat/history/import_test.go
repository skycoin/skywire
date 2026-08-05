//go:build !js
// +build !js

// Package history import_test.go — Store.Import across both backends.
//
// Import is what moves a chat from one device to another, so the properties
// that matter are the ones a migration depends on: it is not rate limited
// (Append is, and at 20/min an archive would arrive as a handful of
// messages), running it twice changes nothing, and it reports honestly what
// the store will not keep. Both backends run the same table so they cannot
// drift; the file carries !js only because BoltStore does.
package history

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func testPeer(prefix string) string {
	return prefix + strings.Repeat("0", 66-len(prefix))
}

// archiveOf builds n messages for peer, oldest first, one second apart.
func archiveOf(peer string, n int, base time.Time) []Message {
	out := make([]Message, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Message{
			Peer:      peer,
			Text:      "message " + string(rune('a'+i%26)),
			ID:        "id-" + time.Duration(i).String(),
			Timestamp: base.Add(time.Duration(i) * time.Second),
		})
	}
	return out
}

// The reason Import exists at all: Append's per-peer rate limit would let
// through PerPeerRatePerMin records and drop the rest of the archive.
func TestImport_IsNotRateLimited(t *testing.T) {
	peer := testPeer("03aa")
	base := time.Now().UTC().Add(-time.Hour)

	limits := DefaultLimits()
	limits.PerPeerRatePerMin = 5

	bothStoresWithLimits(t, limits, func(t *testing.T, s Store) {
		msgs := archiveOf(peer, 50, base)

		res, err := s.Import(msgs, nil)
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if res.Messages != 50 {
			t.Fatalf("stored %d of 50 messages — the rate limit reached the import path", res.Messages)
		}

		got, err := s.ListByPeer(peer, 0)
		if err != nil {
			t.Fatalf("ListByPeer: %v", err)
		}
		if len(got) != 50 {
			t.Fatalf("read back %d messages, want 50", len(got))
		}

		// The live path must still be limited — Import bypassing it is not
		// a license for a peer to flood the disk.
		accepted := 0
		for i := 0; i < 20; i++ {
			if err := s.Append(Message{
				Peer: peer, Text: "live", Timestamp: time.Now().UTC(),
			}); err == nil {
				accepted++
			}
		}
		if accepted > limits.PerPeerRatePerMin {
			t.Fatalf("Append accepted %d in a minute, cap is %d", accepted, limits.PerPeerRatePerMin)
		}
	})
}

func TestImport_SkipsDuplicates(t *testing.T) {
	peer := testPeer("03bb")
	base := time.Now().UTC().Add(-time.Hour)

	bothStores(t, func(t *testing.T, s Store) {
		msgs := archiveOf(peer, 10, base)

		first, err := s.Import(msgs, nil)
		if err != nil {
			t.Fatalf("first Import: %v", err)
		}
		if first.Messages != 10 || first.Duplicates != 0 {
			t.Fatalf("first import: %+v, want 10 stored / 0 duplicate", first)
		}

		second, err := s.Import(msgs, nil)
		if err != nil {
			t.Fatalf("second Import: %v", err)
		}
		if second.Messages != 0 || second.Duplicates != 10 {
			t.Fatalf("re-import: %+v, want 0 stored / 10 duplicate", second)
		}

		got, err := s.ListByPeer(peer, 0)
		if err != nil {
			t.Fatalf("ListByPeer: %v", err)
		}
		if len(got) != 10 {
			t.Fatalf("importing the same archive twice left %d messages, want 10", len(got))
		}
	})
}

// Older messages carry no envelope ID, so identity falls back to timestamp +
// direction + text. Same three, same message; a different direction is not.
func TestImport_DeduplicatesWithoutIDs(t *testing.T) {
	peer := testPeer("03cc")
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)

	bothStores(t, func(t *testing.T, s Store) {
		inbound := Message{Peer: peer, Text: "hello", Timestamp: at}
		outbound := Message{Peer: peer, Text: "hello", Timestamp: at, Outgoing: true}

		if _, err := s.Import([]Message{inbound, outbound}, nil); err != nil {
			t.Fatalf("Import: %v", err)
		}
		res, err := s.Import([]Message{inbound, outbound}, nil)
		if err != nil {
			t.Fatalf("re-Import: %v", err)
		}
		if res.Duplicates != 2 || res.Messages != 0 {
			t.Fatalf("re-import: %+v, want 2 duplicates", res)
		}

		got, err := s.ListByPeer(peer, 0)
		if err != nil {
			t.Fatalf("ListByPeer: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("kept %d messages, want 2 — the two directions are distinct", len(got))
		}
	})
}

func TestImport_MergesIntoExistingHistory(t *testing.T) {
	peer := testPeer("03dd")
	base := time.Now().UTC().Add(-time.Hour)

	bothStores(t, func(t *testing.T, s Store) {
		if err := s.Append(Message{
			Peer: peer, Text: "already here", ID: "local-1", Timestamp: base.Add(time.Minute),
		}); err != nil {
			t.Fatalf("Append: %v", err)
		}

		res, err := s.Import(archiveOf(peer, 3, base), nil)
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if res.Messages != 3 {
			t.Fatalf("stored %d, want 3", res.Messages)
		}

		got, err := s.ListByPeer(peer, 0)
		if err != nil {
			t.Fatalf("ListByPeer: %v", err)
		}
		if len(got) != 4 {
			t.Fatalf("conversation holds %d messages, want 4", len(got))
		}
		// Newest last, imported and local interleaved by timestamp.
		for i := 1; i < len(got); i++ {
			if got[i].Timestamp.Before(got[i-1].Timestamp) {
				t.Fatalf("history is out of order at %d", i)
			}
		}
	})
}

func TestImport_GroupMessages(t *testing.T) {
	group := "group-1"
	base := time.Now().UTC().Add(-time.Hour)

	bothStores(t, func(t *testing.T, s Store) {
		msgs := []GroupMessage{
			{GroupID: group, SenderPK: testPeer("03ee"), Text: "one", Timestamp: base},
			{GroupID: group, SenderPK: testPeer("03ff"), Text: "two", Timestamp: base.Add(time.Second)},
		}

		res, err := s.Import(nil, msgs)
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if res.GroupMessages != 2 {
			t.Fatalf("stored %d group messages, want 2", res.GroupMessages)
		}

		again, err := s.Import(nil, msgs)
		if err != nil {
			t.Fatalf("re-Import: %v", err)
		}
		if again.Duplicates != 2 {
			t.Fatalf("re-import: %+v, want 2 duplicates", again)
		}

		got, err := s.ListByGroup(group, 0)
		if err != nil {
			t.Fatalf("ListByGroup: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("group holds %d messages, want 2", len(got))
		}
	})
}

// The per-peer cap keeps the newest end of an over-large archive, and says
// how much it dropped — the number that decides whether the old device can
// be wiped.
func TestImport_ReportsWhatTheCapEvicts(t *testing.T) {
	peer := testPeer("03a1")
	base := time.Now().UTC().Add(-time.Hour)

	limits := DefaultLimits()
	limits.PerPeerCap = 10

	bothStoresWithLimits(t, limits, func(t *testing.T, s Store) {
		res, err := s.Import(archiveOf(peer, 25, base), nil)
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if res.Evicted != 15 {
			t.Fatalf("evicted %d, want 15", res.Evicted)
		}

		got, err := s.ListByPeer(peer, 0)
		if err != nil {
			t.Fatalf("ListByPeer: %v", err)
		}
		if len(got) != 10 {
			t.Fatalf("kept %d messages, want the cap of 10", len(got))
		}
		// Oldest-first eviction: what survives is the tail.
		if !got[len(got)-1].Timestamp.Equal(base.Add(24 * time.Second)) {
			t.Fatalf("newest kept message is %v, want the last of the archive", got[len(got)-1].Timestamp)
		}
	})
}

// Anything already past the retention window is stored and counted, because
// the sweep is about to take it and the operator needs to know before they
// wipe the source device.
func TestImport_CountsRecordsTheSweepWillTake(t *testing.T) {
	peer := testPeer("03b1")
	now := time.Now().UTC()

	limits := DefaultLimits()
	limits.TTL = 24 * time.Hour

	bothStoresWithLimits(t, limits, func(t *testing.T, s Store) {
		msgs := []Message{
			{Peer: peer, Text: "old", ID: "old-1", Timestamp: now.Add(-48 * time.Hour)},
			{Peer: peer, Text: "recent", ID: "new-1", Timestamp: now.Add(-time.Minute)},
		}

		res, err := s.Import(msgs, nil)
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if res.Messages != 2 {
			t.Fatalf("stored %d, want 2", res.Messages)
		}
		if res.Expiring != 1 {
			t.Fatalf("reported %d expiring, want 1", res.Expiring)
		}
	})
}

func TestImport_RejectsWhatTheLimitsRefuse(t *testing.T) {
	peer := testPeer("03c1")
	now := time.Now().UTC()

	limits := DefaultLimits()
	limits.MaxMessageSize = 16

	bothStoresWithLimits(t, limits, func(t *testing.T, s Store) {
		res, err := s.Import([]Message{
			{Peer: peer, Text: strings.Repeat("x", 64), ID: "big", Timestamp: now},
			{Peer: "", Text: "orphan", ID: "orphan", Timestamp: now},
			{Peer: peer, Text: "fits", ID: "ok", Timestamp: now},
		}, nil)
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if res.Messages != 1 {
			t.Fatalf("stored %d, want 1", res.Messages)
		}
		if res.Rejected != 2 {
			t.Fatalf("rejected %d, want 2 (oversized + no peer)", res.Rejected)
		}
	})
}

func TestImport_FullStoreTakesNothing(t *testing.T) {
	peer := testPeer("03d1")
	now := time.Now().UTC()

	limits := DefaultLimits()
	limits.TotalCapBytes = 1 // any real record is bigger

	bothStoresWithLimits(t, limits, func(t *testing.T, s Store) {
		// Fill past the cap through the normal path first: the mem store
		// measures stored bytes, so an empty one is not yet "full".
		_ = s.Append(Message{Peer: peer, Text: "seed", ID: "seed", Timestamp: now}) //nolint:errcheck

		res, err := s.Import([]Message{
			{Peer: peer, Text: "late", ID: "late", Timestamp: now},
		}, nil)
		if !errors.Is(err, ErrStorageFull) {
			t.Fatalf("err = %v, want ErrStorageFull", err)
		}
		if res.Messages != 0 || res.Rejected != 1 {
			t.Fatalf("result %+v, want nothing stored and 1 rejected", res)
		}
	})
}

func TestImport_EmptyArchiveIsANoOp(t *testing.T) {
	bothStores(t, func(t *testing.T, s Store) {
		res, err := s.Import(nil, nil)
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if (res != ImportResult{}) {
			t.Fatalf("result %+v, want the zero value", res)
		}
	})
}

// bothStoresWithLimits is bothStores for a table that needs its own Limits.
func bothStoresWithLimits(t *testing.T, limits Limits, fn func(t *testing.T, s Store)) {
	t.Helper()
	t.Run("bolt", func(t *testing.T) { fn(t, newTestStore(t, limits)) })
	t.Run("mem", func(t *testing.T) { fn(t, newTestMemStore(t, limits)) })
}
