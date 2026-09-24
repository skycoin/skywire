//go:build !js
// +build !js

// Package history delete_peer_test.go — DeletePeer across both backends, and
// the store no longer naming a conversation it holds nothing of.
//
// DeletePeer is the durable half of deleting a conversation: the page re-adds
// every peer the store names on each load and refills the thread on open, so a
// delete that only touched the page's copy came back with the next load. The
// file carries the !js tag only because BoltStore does.
package history

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDeletePeer_ErasesTheConversation(t *testing.T) {
	gone := "03abc" + strings.Repeat("0", 61)
	kept := "03def" + strings.Repeat("0", 61)
	bothStores(t, func(t *testing.T, s Store) {
		base := time.Now().UTC()
		for i, id := range []string{"id-0", "id-1", "id-2"} {
			if err := s.Append(Message{Peer: gone, ID: id, Text: "msg-" + id, Timestamp: base.Add(time.Duration(i) * time.Millisecond)}); err != nil {
				t.Fatalf("Append %s: %v", id, err)
			}
		}
		if err := s.Append(Message{Peer: kept, ID: "other", Text: "stays", Timestamp: base}); err != nil {
			t.Fatalf("Append other: %v", err)
		}

		n, err := s.DeletePeer(gone)
		if err != nil {
			t.Fatalf("DeletePeer: %v", err)
		}
		if n != 3 {
			t.Fatalf("DeletePeer erased %d messages, want 3", n)
		}
		if msgs, err := s.ListByPeer(gone, 0); err != nil || len(msgs) != 0 {
			t.Fatalf("ListByPeer after DeletePeer = %v, %v; want nothing", msgs, err)
		}
		// The assertion the bug fails: the store must no longer name the
		// peer, or the page puts the conversation back on its next load.
		peers, err := s.Peers()
		if err != nil {
			t.Fatalf("Peers: %v", err)
		}
		if len(peers) != 1 || peers[0] != kept {
			t.Fatalf("Peers after DeletePeer = %v, want only %s", peers, kept)
		}
		if msgs, err := s.ListByPeer(kept, 0); err != nil || len(msgs) != 1 {
			t.Fatalf("the other conversation was touched: %v, %v", msgs, err)
		}

		// A peer with nothing stored is not an error: the same delete may be
		// replayed by a second tab, or persistence may have been off.
		if n, err := s.DeletePeer(gone); err != nil || n != 0 {
			t.Fatalf("repeat DeletePeer = %d, %v; want 0, nil", n, err)
		}
		if _, err := s.DeletePeer(""); !errors.Is(err, ErrEmptyPeer) {
			t.Fatalf("DeletePeer(\"\") = %v, want ErrEmptyPeer", err)
		}
	})
}

// A conversation emptied one message at a time must not be listed either.
// BoltStore left the empty bucket behind and Peers listed it, so deleting
// every message by hand still left a "New conversation" that came back on the
// next load; MemStore already dropped the entry.
func TestPeers_OmitsAConversationEmptiedByDeleteByID(t *testing.T) {
	peer := "03abc" + strings.Repeat("0", 61)
	bothStores(t, func(t *testing.T, s Store) {
		if err := s.Append(Message{Peer: peer, ID: "only", Text: "one", Timestamp: time.Now().UTC()}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		if found, err := s.DeleteByID(peer, "only"); err != nil || !found {
			t.Fatalf("DeleteByID = %v, %v; want true, nil", found, err)
		}
		peers, err := s.Peers()
		if err != nil {
			t.Fatalf("Peers: %v", err)
		}
		if len(peers) != 0 {
			t.Fatalf("Peers = %v after the only message was deleted, want none", peers)
		}
	})
}
