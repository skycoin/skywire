// Package group pkg/skychat/group/send_test.go
//
// Coverage for the outbound write path (Session.Send / publishAs): the
// owner-side self-echo delivery, the signed leaf that lands on the
// publisher feed, ModePrivate body encryption at rest, and the
// heartbeat-not-delivered filter. Uses the in-proc publisher fixture
// (newInProcessPublisher / pubReadable from gossip_emit_test.go).
package group

import (
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

type sendCapture struct {
	mu   sync.Mutex
	msgs []Message
}

func (c *sendCapture) deliver(_ string, sender cipher.PubKey, m Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m.SenderPK = sender
	c.msgs = append(c.msgs, m)
}

func (c *sendCapture) all() []Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Message(nil), c.msgs...)
}

// ownerSendSession builds an owner-role Session backed by a real in-proc
// publisher, with a capturing handler installed.
func ownerSendSession(t *testing.T, mode Mode, aesKey []byte) (*Session, *sendCapture) {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	pub := newInProcessPublisher(t, sk)
	cap := &sendCapture{}
	s := &Session{
		cfg: Config{
			MyPK: pk, MySK: sk,
			Record: Record{
				ID: uuid.NewString(), OwnerPK: pk, Mode: mode, AESKey: aesKey,
				Role: RoleOwner, Members: []cipher.PubKey{pk},
			},
		},
		pub:     pub,
		dedup:   newRecentSet(inboxDedupCap),
		log:     logging.MustGetLogger("group.send-test"),
		members: []cipher.PubKey{pk},
	}
	s.SetMessageHandler(cap.deliver)
	return s, cap
}

func TestSessionSend_DeliversSelfEcho(t *testing.T) {
	s, cap := ownerSendSession(t, ModePublic, nil)
	if err := s.Send("hello group"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	msgs := cap.all()
	if len(msgs) != 1 || msgs[0].Text != "hello group" || msgs[0].SenderPK != s.cfg.MyPK {
		t.Fatalf("self-echo = %+v, want one 'hello group' authored by self", msgs)
	}
}

func TestPublishAs_PublicLeafSignedOnFeed(t *testing.T) {
	s, _ := ownerSendSession(t, ModePublic, nil)
	ts := time.Unix(1700000000, 0).UTC()
	if err := s.publishAs(s.cfg.MyPK, "on the feed", ts); err != nil {
		t.Fatalf("publishAs: %v", err)
	}
	// First publish -> seq 1; path is deterministic from (pk, ts, seq).
	path := fmt.Sprintf("%s/%s/%d/%d", MessagePathPrefix, s.cfg.MyPK.Hex(), ts.UnixNano(), 1)
	body := pubReadable(t, s.pub, path)

	var m Message
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode leaf: %v", err)
	}
	if m.Text != "on the feed" {
		t.Errorf("leaf text = %q, want 'on the feed'", m.Text)
	}
	if err := VerifyMessage(m); err != nil {
		t.Errorf("stored leaf must carry a valid signature: %v", err)
	}
}

func TestPublishAs_PrivateEncryptsLeaf(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	s, cap := ownerSendSession(t, ModePrivate, key)
	ts := time.Unix(1700000000, 0).UTC()
	if err := s.publishAs(s.cfg.MyPK, "secret", ts); err != nil {
		t.Fatalf("publishAs: %v", err)
	}

	// The self-echo handler still sees plaintext.
	if msgs := cap.all(); len(msgs) != 1 || msgs[0].Text != "secret" {
		t.Fatalf("self-echo should deliver plaintext, got %+v", msgs)
	}

	// The stored leaf is ciphertext, not plaintext.
	path := fmt.Sprintf("%s/%s/%d/%d", MessagePathPrefix, s.cfg.MyPK.Hex(), ts.UnixNano(), 1)
	body := pubReadable(t, s.pub, path)
	if bytesContains(body, "secret") {
		t.Error("private-mode leaf must not contain the plaintext body")
	}
	var m Message
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode leaf: %v", err)
	}
	if len(m.Ciphertext) == 0 || m.Text != "" {
		t.Errorf("private leaf should carry ciphertext and empty text, got text=%q ct_len=%d", m.Text, len(m.Ciphertext))
	}
	if err := VerifyMessage(m); err != nil {
		t.Errorf("private leaf must still verify: %v", err)
	}
	// And it decrypts back to the plaintext with the group key.
	pt, err := Decrypt(key, m.Ciphertext, m.Nonce)
	if err != nil || string(pt) != "secret" {
		t.Errorf("decrypt = %q, err=%v, want 'secret'", pt, err)
	}
}

func TestPublishAs_HeartbeatNotDelivered(t *testing.T) {
	s, cap := ownerSendSession(t, ModePublic, nil)
	ts := time.Unix(1700000000, 0).UTC()
	if err := s.publishAs(s.cfg.MyPK, HeartbeatMarker, ts); err != nil {
		t.Fatalf("publishAs heartbeat: %v", err)
	}
	// Heartbeats are wire-level probes — never surfaced to the handler.
	if msgs := cap.all(); len(msgs) != 0 {
		t.Errorf("heartbeat should not be delivered to the handler, got %+v", msgs)
	}
	// But the leaf IS written to the feed (members observe it for liveness).
	path := fmt.Sprintf("%s/%s/%d/%d", MessagePathPrefix, s.cfg.MyPK.Hex(), ts.UnixNano(), 1)
	if _, ok := s.pub.Get(path); !ok {
		// pubReadable would fatal; a direct Get with a short poll is enough
		// since publishAs above already returned.
		_ = pubReadable(t, s.pub, path)
	}
}

func TestSessionUnsend(t *testing.T) {
	s, _ := ownerSendSession(t, ModePublic, nil)
	ts := time.Unix(1700000000, 0).UTC()
	if err := s.publishAs(s.cfg.MyPK, "delete me", ts); err != nil {
		t.Fatalf("publishAs: %v", err)
	}
	path := fmt.Sprintf("%s/%s/%d/%d", MessagePathPrefix, s.cfg.MyPK.Hex(), ts.UnixNano(), 1)
	_ = pubReadable(t, s.pub, path) // ensure it landed

	if err := s.Unsend(ts.UnixNano()); err != nil {
		t.Fatalf("Unsend: %v", err)
	}
	// PrunePrefix wipes msgs/<myPK>/<ts> — the leaf is gone.
	prunedPrefix := MessagePathPrefix + "/" + s.cfg.MyPK.Hex() + "/" + strconv.FormatInt(ts.UnixNano(), 10)
	if _, ok := s.pub.Get(path); ok {
		t.Errorf("Unsend should have removed the leaf under %s", prunedPrefix)
	}
}

// bytesContains is a tiny substring check so this file needs no extra import.
func bytesContains(b []byte, sub string) bool {
	s := string(b)
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
