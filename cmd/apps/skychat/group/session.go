// Package group — cmd/apps/skychat/group/session.go: runtime
// lifecycle for one live group, parallel to pairing.Pair.
//
// One Session per (group, role) pair on each visor:
//
//   - Owner role: hosts a treestore.Publisher with
//     SubscriberAllowlist set to every member's PK. Send writes
//     leaves under "msgs/<ts-nano>/<seq>" which subscribers pick up.
//     No subscriber on this side — owner doesn't need to "hear
//     themselves back" since Send fans out locally via the skychat
//     hub before publishing.
//
//   - Member role: hosts a treestore.Subscriber connected to the
//     owner's group feed on the deterministic port from Record.Port.
//     No publisher in D1. Outgoing messages (when wired into
//     skychat) go to the owner via the existing 1:1 pair-control
//     wire with a group_id tag; owner relays into the feed.
//
// Encryption: if the record's Mode is ModePrivate, AES-256-GCM
// envelope wraps the message body before Put and unwraps on
// receive. If ModePublic, the body is plaintext JSON. This is the
// only divergence from pairing.Pair — pairing always encrypts via
// the ECDH-derived per-pair key.
//
// Phase 2 (project memo project_windows_autoconfig_parity refers
// only to the Windows track; this is the group-chat track) shifts
// to distributed publishers so the owner is no longer a SPOF: each
// member publishes their own message feed, subscribers follow the
// owner's membership feed and every member's message feed. The
// Session type as written here is forward-compatible — Phase 2
// just spins up multiple Sessions per group, one per remote member.
package group

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// MessagePathPrefix is the prefix used for message leaves within a
// group feed. Matches the pairing analogue so an operator who
// understands one understands both.
const MessagePathPrefix = "msgs"

// MessageHandler is invoked on every newly-arrived group message
// after decryption (for private groups). Called from the
// subscriber's update goroutine; implementations must not block.
//
// The senderPK is the member who published the message (extracted
// from the decoded Message.SenderPK field, not from the CXO leaf
// path, so a malicious owner who relays from another member can't
// spoof attribution beyond what the sender's signature already
// gates).
type MessageHandler func(groupID string, senderPK cipher.PubKey, msg Message)

// Config bundles the inputs Session.Open needs. Mirrors
// pairing.Config where it makes sense; Record + Role replace the
// PeerPK-shaped fields.
type Config struct {
	// MyPK / MySK identify this visor.
	MyPK cipher.PubKey
	MySK cipher.SecKey

	// Record is the persisted group state. The session inherits ID,
	// OwnerPK, Port, Mode, AESKey, Members, and Role from it. The
	// caller must have already loaded this from group.Store.
	Record Record

	// DmsgC is the shared DMSG client.
	DmsgC *dmsg.Client

	// DataDir is the per-visor parent directory for CXO state. Each
	// Session carves its own group/<id>/ subtree inside it.
	DataDir string

	// Logger is optional; nil falls back to a tag-based default.
	Logger *logging.Logger

	// BatchWindow forwards to the publisher (see treestore.PubConfig).
	BatchWindow time.Duration
}

// Session is one live group as this visor knows it — either as the
// owner (publisher) or as a member (subscriber). Safe for concurrent
// use after Open returns; Send / Close may be called from any
// goroutine.
type Session struct {
	cfg  Config
	port uint16

	// Exactly one of pub or sub is non-nil, decided by cfg.Record.Role.
	pub *treestore.Publisher
	sub *treestore.Subscriber

	// seq disambiguates messages with the same nanosecond timestamp.
	// Owner-scoped (members don't publish in D1), but lives on the
	// Session so the Phase-2-future where every member is a publisher
	// just works.
	seq atomic.Uint64

	handler MessageHandler
	log     *logging.Logger
}

// Open constructs the session's CXO node and brings up the role-
// appropriate publisher OR subscriber. For owner role, the
// publisher's SubscriberAllowlist is set to every member PK in the
// record. For member role, the subscriber is created attached to a
// fresh CXO node and waits for Connect to dial the owner.
func Open(cfg Config) (*Session, error) {
	if cfg.DmsgC == nil {
		return nil, errors.New("group: Open: DmsgC required")
	}
	if cfg.Record.ID == "" {
		return nil, errors.New("group: Open: Record.ID required")
	}
	if cfg.Record.OwnerPK == (cipher.PubKey{}) {
		return nil, errors.New("group: Open: Record.OwnerPK required")
	}
	if cfg.Record.Port == 0 {
		return nil, errors.New("group: Open: Record.Port required")
	}
	if !cfg.Record.Mode.IsValid() {
		return nil, fmt.Errorf("group: Open: invalid Mode %q", cfg.Record.Mode)
	}
	if cfg.Record.Mode == ModePrivate && len(cfg.Record.AESKey) != 32 {
		return nil, fmt.Errorf("group: Open: private mode requires 32-byte AESKey, got %d", len(cfg.Record.AESKey))
	}
	if cfg.DataDir == "" {
		return nil, errors.New("group: Open: DataDir required")
	}

	log := cfg.Logger
	if log == nil {
		log = logging.MustGetLogger("skychat-group")
	}

	switch cfg.Record.Role {
	case RoleOwner:
		return openOwner(cfg, log)
	case RoleMember:
		return openMember(cfg, log)
	default:
		return nil, fmt.Errorf("group: Open: invalid Role %q", cfg.Record.Role)
	}
}

// openOwner brings up a publisher with the member allowlist.
func openOwner(cfg Config, log *logging.Logger) (*Session, error) {
	pub, err := treestore.NewWithDMSG(cfg.DmsgC, cfg.MySK, treestore.PubConfig{
		BatchWindow:         cfg.BatchWindow,
		Logger:              log,
		DataDir:             filepath.Join(cfg.DataDir, "group", cfg.Record.ID),
		DmsgPort:            cfg.Record.Port,
		SubscriberAllowlist: append([]cipher.PubKey(nil), cfg.Record.Members...),
	})
	if err != nil {
		return nil, fmt.Errorf("group: Open owner: build publisher: %w", err)
	}
	return &Session{cfg: cfg, port: cfg.Record.Port, pub: pub, log: log}, nil
}

// openMember creates a subscriber. The connect-to-owner step is
// deferred to Connect so the caller controls ordering (e.g. after
// the local Record is persisted, before announcing membership to
// other apps).
func openMember(cfg Config, log *logging.Logger) (*Session, error) {
	// Member needs a CXO node to host the subscriber on. We build a
	// throwaway publisher with an empty allowlist (no one subscribes
	// to a member's per-group feed in D1) just to get a node. Phase
	// 2 turns this into a real per-member outbox feed.
	pub, err := treestore.NewWithDMSG(cfg.DmsgC, cfg.MySK, treestore.PubConfig{
		BatchWindow:         cfg.BatchWindow,
		Logger:              log,
		DataDir:             filepath.Join(cfg.DataDir, "group", cfg.Record.ID, "member"),
		DmsgPort:            cfg.Record.Port + 1, // separate port; owner owns Record.Port
		SubscriberAllowlist: []cipher.PubKey{},   // empty = nobody allowed
	})
	if err != nil {
		return nil, fmt.Errorf("group: Open member: build local node: %w", err)
	}
	sub, err := treestore.NewSubscriberOnNode(pub.Node(), cfg.Record.OwnerPK, treestore.SubConfig{
		Logger: log,
	})
	if err != nil {
		_ = pub.Close() //nolint:errcheck
		return nil, fmt.Errorf("group: Open member: attach subscriber: %w", err)
	}
	sub.SetPrefixes([]string{MessagePathPrefix})
	s := &Session{cfg: cfg, port: cfg.Record.Port, pub: pub, sub: sub, log: log}
	sub.OnUpdate(s.onUpdate)
	return s, nil
}

// Connect dials the owner's CXO node and starts the subscribe
// handshake. Owner sessions are a no-op (they don't subscribe to
// themselves). Idempotent: returns nil if already connected.
func (s *Session) Connect() error {
	if s.sub == nil {
		return nil // owner role
	}
	if err := s.sub.Connect(s.cfg.Record.OwnerPK); err != nil {
		return fmt.Errorf("group: Connect: %w", err)
	}
	return nil
}

// ID returns the group's UUID.
func (s *Session) ID() string { return s.cfg.Record.ID }

// Port returns the DMSG port this session uses.
func (s *Session) Port() uint16 { return s.port }

// SetMessageHandler registers (or replaces) the inbound-message
// callback. Pass nil to unregister.
func (s *Session) SetMessageHandler(h MessageHandler) {
	s.handler = h
}

// Send publishes a chat message. Owner-side only in D1: members
// must POST to the owner via the existing 1:1 pair-control wire
// with a group_id tag, and the owner's skychat app calls Send to
// relay into the feed. Returns an error if invoked on a member-role
// session.
//
// For ModePrivate groups, the body is AES-256-GCM-sealed before
// being stored as the leaf value. The path itself (timestamp +
// seq) stays plaintext.
func (s *Session) Send(text string) error {
	if s.pub == nil || s.cfg.Record.Role != RoleOwner {
		return errors.New("group: Send: only owner-role sessions can publish in D1")
	}
	now := time.Now().UTC()
	msg := Message{
		SenderPK: s.cfg.MyPK,
		TS:       now,
	}
	switch s.cfg.Record.Mode {
	case ModePublic:
		msg.Text = text
	case ModePrivate:
		ct, nonce, err := Encrypt(s.cfg.Record.AESKey, []byte(text))
		if err != nil {
			return fmt.Errorf("group: Send: %w", err)
		}
		msg.Ciphertext = ct
		msg.Nonce = nonce
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("group: Send: marshal: %w", err)
	}
	seq := s.seq.Add(1)
	path := MessagePathPrefix + "/" + strconv.FormatInt(now.UnixNano(), 10) + "/" + strconv.FormatUint(seq, 10)
	if err := s.pub.Put(path, body); err != nil {
		return fmt.Errorf("group: Send: put %q: %w", path, err)
	}
	return nil
}

// SetAllowlist updates the publisher's subscriber allowlist to the
// given member set. Owner-side only. Used when an invite is issued
// or a member is removed.
func (s *Session) SetAllowlist(members []cipher.PubKey) error {
	if s.pub == nil || s.cfg.Record.Role != RoleOwner {
		return errors.New("group: SetAllowlist: only owner-role sessions have an allowlist")
	}
	s.pub.SetAllowlist(append([]cipher.PubKey(nil), members...))
	return nil
}

// Close tears down the subscriber + publisher (and the underlying
// CXO node, owned by the publisher). Idempotent.
func (s *Session) Close() error {
	var firstErr error
	if s.sub != nil {
		if err := s.sub.Close(); err != nil {
			firstErr = err
		}
		s.sub = nil
	}
	if s.pub != nil {
		if err := s.pub.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.pub = nil
	}
	return firstErr
}

// onUpdate is the subscriber callback for member-role sessions.
// Decodes each leaf, decrypts if private, and dispatches via the
// registered handler. Tolerates undecodable leaves (e.g. future
// metadata under a different prefix, or a tampered ciphertext) by
// silently dropping them with a debug log.
func (s *Session) onUpdate(events []treestore.UpdateEvent) {
	h := s.handler
	if h == nil {
		return
	}
	for _, ev := range events {
		if ev.Value == nil {
			continue
		}
		var msg Message
		if err := json.Unmarshal(ev.Value, &msg); err != nil {
			s.log.WithError(err).WithField("path", ev.Path).
				Debug("group: ignoring undecodable leaf")
			continue
		}
		if s.cfg.Record.Mode == ModePrivate {
			plain, err := Decrypt(s.cfg.Record.AESKey, msg.Ciphertext, msg.Nonce)
			if err != nil {
				s.log.WithError(err).WithField("path", ev.Path).
					Debug("group: ignoring leaf failing decrypt")
				continue
			}
			msg.Text = string(plain)
			// Don't expose the ciphertext to handlers; they should
			// only see plaintext from this point.
			msg.Ciphertext = nil
			msg.Nonce = nil
		}
		h(s.cfg.Record.ID, msg.SenderPK, msg)
	}
}
