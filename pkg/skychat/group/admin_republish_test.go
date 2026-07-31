// Package group — cmd/apps/skychat/group/admin_republish_test.go:
// targeted coverage for the admin-aggregator Beta slice — the
// onUpdate admin-side republish path that writes verified leaves
// VERBATIM (same path, same body bytes) onto the admin's own
// publisher feed.
//
// Scope: the onUpdate code branch only — does the admin republish
// fire when it should, NOT fire when it shouldn't (non-admin
// session, own message, heartbeat, bad signature), and use the
// original ev.Value bytes (so ModePrivate ciphertext doesn't leak
// as plaintext through a re-marshal). Cross-visor convergence
// belongs in the integration PR.
package group

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/logging"
)

// adminSessionFixture builds a Session whose owner == MyPK == admin,
// wired to a real in-memory publisher so we can read back what
// onUpdate wrote. Mirrors testPublisherSession (gossip_emit_test.go)
// but sets up an inbox dedup set and a no-op handler so the loop in
// onUpdate doesn't crash on nil h.
func adminSessionFixture(t *testing.T) *Session {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	pub := newInProcessPublisher(t, sk)
	return &Session{
		cfg: Config{
			MyPK: pk,
			MySK: sk,
			Record: Record{
				ID:      uuid.NewString(),
				OwnerPK: pk, // founder is automatically admin
			},
		},
		pub:     pub,
		log:     logging.MustGetLogger("group.admin-republish-test"),
		dedup:   newRecentSet(inboxDedupCap),
		handler: func(_ string, _ cipher.PubKey, _ Message) {}, // no-op
	}
}

// nonAdminSessionFixture builds a Session whose MyPK is NOT in the
// group's admin set, so the IsAdmin gate in onUpdate fails. Used to
// confirm the republish branch is properly gated.
func nonAdminSessionFixture(t *testing.T) *Session {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	ownerPK, _ := cipher.GenerateKeyPair()
	pub := newInProcessPublisher(t, sk)
	return &Session{
		cfg: Config{
			MyPK: pk,
			MySK: sk,
			Record: Record{
				ID:      uuid.NewString(),
				OwnerPK: ownerPK, // someone else owns the group
				// Admins defaulted empty; pk is just a member.
			},
		},
		pub:     pub,
		log:     logging.MustGetLogger("group.admin-republish-test-nonadmin"),
		dedup:   newRecentSet(inboxDedupCap),
		handler: func(_ string, _ cipher.PubKey, _ Message) {}, // no-op
	}
}

// makeSignedLeaf builds a Message authored by senderSk, marshals it,
// and returns (path, body) suitable for synthesizing an UpdateEvent
// that mirrors what an admin would observe from a peerSub on
// senderPk's per-peer feed.
func makeSignedLeaf(t *testing.T, senderPK cipher.PubKey, senderSK cipher.SecKey, text string, ts time.Time, seq uint64) (string, []byte) {
	t.Helper()
	msg := Message{SenderPK: senderPK, Text: text, TS: ts}
	if err := SignMessage(&msg, senderSK); err != nil {
		t.Fatalf("SignMessage: %v", err)
	}
	body, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	path := fmt.Sprintf("%s/%s/%d/%d", MessagePathPrefix, senderPK.Hex(), ts.UnixNano(), seq)
	return path, body
}

func TestAdminRepublishesVerifiedLeafVerbatim(t *testing.T) {
	s := adminSessionFixture(t)
	senderPK, senderSK := cipher.GenerateKeyPair()
	ts := time.Unix(1700000000, 0).UTC()
	path, body := makeSignedLeaf(t, senderPK, senderSK, "hello from peer", ts, 7)

	s.onUpdate([]treestore.UpdateEvent{{Path: path, Value: body}})

	got := pubReadable(t, s.pub, path)
	if string(got) != string(body) {
		t.Errorf("admin republish wrote different bytes:\n  want %q\n  got  %q", body, got)
	}
}

// TestAdminRepublishPreservesCiphertext guards the path-rewrite
// regression a naive impl would introduce: if the admin re-marshaled
// msg AFTER the ModePrivate decrypt block had cleared msg.Ciphertext /
// .Nonce and stuffed plaintext into msg.Text, the republished body
// would leak the plaintext to every non-admin subscriber on the
// admin's canonical feed. Verbatim-by-ev.Value is the protection.
func TestAdminRepublishPreservesCiphertext(t *testing.T) {
	s := adminSessionFixture(t)
	// Switch the fixture's mode to private with a key so the decrypt
	// branch actually runs.
	s.cfg.Record.Mode = ModePrivate
	s.cfg.Record.AESKey = make([]byte, 32)
	for i := range s.cfg.Record.AESKey {
		s.cfg.Record.AESKey[i] = byte(i)
	}

	senderPK, senderSK := cipher.GenerateKeyPair()
	plaintext := "secret message"
	ct, nonce, err := Encrypt(s.cfg.Record.AESKey, []byte(plaintext))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	ts := time.Unix(1700000000, 0).UTC()
	msg := Message{SenderPK: senderPK, Ciphertext: ct, Nonce: nonce, TS: ts}
	if err := SignMessage(&msg, senderSK); err != nil {
		t.Fatalf("SignMessage: %v", err)
	}
	body, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	path := fmt.Sprintf("%s/%s/%d/%d", MessagePathPrefix, senderPK.Hex(), ts.UnixNano(), 1)

	s.onUpdate([]treestore.UpdateEvent{{Path: path, Value: body}})

	got := pubReadable(t, s.pub, path)
	if string(got) != string(body) {
		t.Fatalf("admin republish did not write verbatim bytes")
	}
	// Decode what we wrote and confirm it still carries the original
	// ciphertext, not the post-decrypt plaintext.
	var roundtrip Message
	if err := json.Unmarshal(got, &roundtrip); err != nil {
		t.Fatalf("roundtrip Unmarshal: %v", err)
	}
	if string(roundtrip.Ciphertext) == "" || roundtrip.Text == plaintext {
		t.Errorf("admin republish leaked plaintext: text=%q ct_len=%d", roundtrip.Text, len(roundtrip.Ciphertext))
	}
}

func TestNonAdminDoesNotRepublish(t *testing.T) {
	s := nonAdminSessionFixture(t)
	senderPK, senderSK := cipher.GenerateKeyPair()
	ts := time.Unix(1700000000, 0).UTC()
	path, body := makeSignedLeaf(t, senderPK, senderSK, "hi", ts, 1)

	s.onUpdate([]treestore.UpdateEvent{{Path: path, Value: body}})

	// Non-admin session must NOT have written the leaf to its own pub.
	if _, ok := s.pub.Get(path); ok {
		t.Errorf("non-admin republished a leaf it should have ignored")
	}
}

func TestAdminSkipsOwnMessage(t *testing.T) {
	s := adminSessionFixture(t)
	// Own-message: synthesize a leaf with SenderPK == s.cfg.MyPK at a
	// fresh seq. The path that onUpdate looks at uses MyPK; our test
	// leaf was never written by publishAs, so if the admin republishes
	// it we'd see it appear on s.pub. The gate (msg.SenderPK !=
	// s.cfg.MyPK) should suppress that write.
	ts := time.Unix(1700000000, 0).UTC()
	path, body := makeSignedLeaf(t, s.cfg.MyPK, s.cfg.MySK, "from me", ts, 99)

	s.onUpdate([]treestore.UpdateEvent{{Path: path, Value: body}})

	if _, ok := s.pub.Get(path); ok {
		t.Errorf("admin republished its own message (the SenderPK==MyPK gate failed)")
	}
}

func TestAdminSkipsHeartbeat(t *testing.T) {
	s := adminSessionFixture(t)
	senderPK, senderSK := cipher.GenerateKeyPair()
	ts := time.Unix(1700000000, 0).UTC()
	path, body := makeSignedLeaf(t, senderPK, senderSK, HeartbeatMarker, ts, 1)

	s.onUpdate([]treestore.UpdateEvent{{Path: path, Value: body}})

	if _, ok := s.pub.Get(path); ok {
		t.Errorf("admin republished a heartbeat (the IsHeartbeat gate failed)")
	}
}

func TestAdminSkipsBadSignature(t *testing.T) {
	s := adminSessionFixture(t)
	senderPK, senderSK := cipher.GenerateKeyPair()
	ts := time.Unix(1700000000, 0).UTC()
	msg := Message{SenderPK: senderPK, Text: "honest", TS: ts}
	if err := SignMessage(&msg, senderSK); err != nil {
		t.Fatalf("SignMessage: %v", err)
	}
	// Tamper: flip a byte in the text after signing. VerifyMessage
	// must now reject; admin republish must NOT fire.
	msg.Text = "tampered"
	body, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	path := fmt.Sprintf("%s/%s/%d/%d", MessagePathPrefix, senderPK.Hex(), ts.UnixNano(), 1)

	s.onUpdate([]treestore.UpdateEvent{{Path: path, Value: body}})

	if _, ok := s.pub.Get(path); ok {
		t.Errorf("admin republished a leaf whose signature failed verification")
	}
}

// TestAdminDedupSuppressesSecondRepublish exercises the loop guard:
// when the same canonical leaf arrives twice (e.g. via two peerSubs
// observing the same source feed), the second observation must NOT
// produce a second Put. Otherwise N peers fanning out to one admin
// produce N writes to the same path. Treestore.Put on identical
// content is a no-op but the round-trip is still wasted, and once
// cross-admin subscription lands (Gamma's slice) the loop guard
// becomes load-bearing against ping-pong.
func TestAdminDedupSuppressesSecondRepublish(t *testing.T) {
	s := adminSessionFixture(t)
	senderPK, senderSK := cipher.GenerateKeyPair()
	ts := time.Unix(1700000000, 0).UTC()
	path, body := makeSignedLeaf(t, senderPK, senderSK, "once", ts, 1)

	s.onUpdate([]treestore.UpdateEvent{{Path: path, Value: body}})
	got := pubReadable(t, s.pub, path)
	if string(got) != string(body) {
		t.Fatalf("first observation should have republished verbatim")
	}

	// Second observation of the SAME canonical leaf — dedupKey collapses
	// to the same suffix, dedup.Add returns false, the loop continues
	// past every gate including ours. We can't tell from pub.Get that
	// the Put didn't fire a second time (content identical), but we
	// CAN tell from the dedup set state: Add of the same key returns
	// false, which is what the loop relies on.
	if s.dedup.Add(dedupKey(path)) {
		t.Errorf("dedup set should suppress second add of the same canonical key")
	}
}
