//go:build !js
// +build !js

// Package history delete_by_id_test.go — DeleteByID across both backends.
//
// DeleteByID is the durable half of delete-for-everyone: without it a
// retracted message survives in the store and reappears the next time a
// client hydrates from history instead of its local cache. Both backends
// are exercised through the same table so they can't drift; the file
// carries the !js tag only because BoltStore does.
package history

import (
	"strings"
	"testing"
	"time"
)

// bothStores runs fn against each Store implementation.
func bothStores(t *testing.T, fn func(t *testing.T, s Store)) {
	t.Helper()
	t.Run("bolt", func(t *testing.T) { fn(t, newTestStore(t, DefaultLimits())) })
	t.Run("mem", func(t *testing.T) { fn(t, newTestMemStore(t, DefaultLimits())) })
}

func TestDeleteByID_RemovesOnlyTheNamedMessage(t *testing.T) {
	peer := "03abc" + strings.Repeat("0", 61)
	bothStores(t, func(t *testing.T, s Store) {
		base := time.Now().UTC()
		for i, id := range []string{"id-0", "id-1", "id-2"} {
			if err := s.Append(Message{
				Peer:      peer,
				Text:      "msg-" + id,
				ID:        id,
				Timestamp: base.Add(time.Duration(i) * time.Millisecond),
			}); err != nil {
				t.Fatalf("Append %s: %v", id, err)
			}
		}

		found, err := s.DeleteByID(peer, "id-1")
		if err != nil {
			t.Fatalf("DeleteByID: %v", err)
		}
		if !found {
			t.Fatal("DeleteByID reported no match for a stored id")
		}

		msgs, err := s.ListByPeer(peer, 0)
		if err != nil {
			t.Fatalf("ListByPeer: %v", err)
		}
		if len(msgs) != 2 {
			t.Fatalf("len(msgs) = %d, want 2 after deleting one of three", len(msgs))
		}
		for _, m := range msgs {
			if m.ID == "id-1" {
				t.Errorf("id-1 still present after DeleteByID: %+v", m)
			}
		}
	})
}

// An inbound message is stored under the same peer bucket as an outbound
// one, so a delete arriving from the peer must reach their message too —
// that direction is the one OnDelete drives.
func TestDeleteByID_DeletesInboundMessages(t *testing.T) {
	peer := "03def" + strings.Repeat("0", 61)
	bothStores(t, func(t *testing.T, s Store) {
		if err := s.Append(Message{
			Peer: peer, From: peer, Outgoing: false,
			Text: "from them", ID: "theirs", Timestamp: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		found, err := s.DeleteByID(peer, "theirs")
		if err != nil || !found {
			t.Fatalf("DeleteByID = %v, %v; want true, nil", found, err)
		}
		msgs, err := s.ListByPeer(peer, 0)
		if err != nil {
			t.Fatalf("ListByPeer: %v", err)
		}
		if len(msgs) != 0 {
			t.Errorf("msgs = %+v, want empty", msgs)
		}
	})
}

// A delete can name a message this side never persisted (persistence was
// off when it arrived, or the record aged out). That's a miss, not an
// error — the live tombstone still applies.
func TestDeleteByID_UnknownIDIsNotAnError(t *testing.T) {
	peer := "03aaa" + strings.Repeat("0", 61)
	bothStores(t, func(t *testing.T, s Store) {
		if err := s.Append(Message{
			Peer: peer, Text: "kept", ID: "keep-me", Timestamp: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		found, err := s.DeleteByID(peer, "never-stored")
		if err != nil {
			t.Fatalf("DeleteByID: %v", err)
		}
		if found {
			t.Error("DeleteByID reported a match for an id that was never stored")
		}
		// Unknown peer: same contract.
		found, err = s.DeleteByID("03fff"+strings.Repeat("0", 61), "whatever")
		if err != nil {
			t.Fatalf("DeleteByID unknown peer: %v", err)
		}
		if found {
			t.Error("DeleteByID reported a match on an unknown peer")
		}
		msgs, err := s.ListByPeer(peer, 0)
		if err != nil {
			t.Fatalf("ListByPeer: %v", err)
		}
		if len(msgs) != 1 {
			t.Errorf("len(msgs) = %d, want the untouched message to remain", len(msgs))
		}
	})
}

func TestDeleteByID_RejectsEmptyPeerAndIgnoresEmptyID(t *testing.T) {
	bothStores(t, func(t *testing.T, s Store) {
		if _, err := s.DeleteByID("", "id"); err == nil {
			t.Error("DeleteByID with an empty peer = nil error, want ErrEmptyPeer")
		}
		// An empty id can't name anything; treat it as a miss rather than
		// letting a bad caller wipe records with no ID recorded.
		peer := "03bbb" + strings.Repeat("0", 61)
		if err := s.Append(Message{Peer: peer, Text: "no id", Timestamp: time.Now().UTC()}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		found, err := s.DeleteByID(peer, "")
		if err != nil {
			t.Fatalf("DeleteByID: %v", err)
		}
		if found {
			t.Error("DeleteByID(peer, \"\") matched a record; want a no-op")
		}
		msgs, err := s.ListByPeer(peer, 0)
		if err != nil {
			t.Fatalf("ListByPeer: %v", err)
		}
		if len(msgs) != 1 {
			t.Errorf("len(msgs) = %d, want the id-less message untouched", len(msgs))
		}
	})
}

// The delete must outlive the process: a message dropped from the bolt file
// stays gone when the store is reopened, which is exactly the reload path
// that used to resurrect it.
func TestDeleteByID_BoltSurvivesReopen(t *testing.T) {
	peer := "03ccc" + strings.Repeat("0", 61)
	dir := t.TempDir()
	path := dir + "/skychat.db"

	s, err := NewBoltStore(path, DefaultLimits())
	if err != nil {
		t.Fatalf("NewBoltStore: %v", err)
	}
	if err := s.Append(Message{Peer: peer, Text: "gone soon", ID: "bye", Timestamp: time.Now().UTC()}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := s.DeleteByID(peer, "bye"); err != nil {
		t.Fatalf("DeleteByID: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := NewBoltStore(path, DefaultLimits())
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() }) //nolint:errcheck
	msgs, err := reopened.ListByPeer(peer, 0)
	if err != nil {
		t.Fatalf("ListByPeer: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("msgs after reopen = %+v, want empty — the delete didn't stick", msgs)
	}
}
