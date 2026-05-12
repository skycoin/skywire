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
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// MessagePathPrefix is the prefix used for message leaves within a
// group feed. Matches the pairing analog so an operator who
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

	// pub is set on the owner side (the group's CXO message feed) and
	// also on the member side (a throwaway local CXO node hosting the
	// subscriber). sub is set on the member side only.
	pub *treestore.Publisher
	sub *treestore.Subscriber

	// relayListener is set on the owner side: a dmsg.Listen on
	// Record.Port+1 accepting member-relay submissions. Members
	// dial this port, write a framed RelayMessage envelope, and
	// the owner-side handler re-publishes into the CXO feed with
	// the original sender's PK attributed (see acceptRelay).
	// nil on the member side.
	relayListener net.Listener
	relayCtx      context.Context
	relayCancel   context.CancelFunc
	relayWG       sync.WaitGroup

	// seq disambiguates messages with the same nanosecond timestamp.
	seq atomic.Uint64

	handler MessageHandler
	log     *logging.Logger
}

// relayPortOffset is the dmsg-port delta from the group's CXO
// publish port to the relay-submission listener. Members dial
// Owner.PK:(Record.Port + relayPortOffset) to submit messages.
// Keeping it as a single +1 reuses the existing "owner owns
// Record.Port, members own Record.Port+1 on their side" pattern
// without introducing a third port slot.
const relayPortOffset uint16 = 1

// RelayMessage is the on-the-wire envelope a member sends to the
// owner's relay listener. The owner validates SenderPK against the
// group's Members allowlist, then re-publishes into the CXO feed
// using SenderPK so the attribution survives the relay hop.
//
// Note: this envelope is not encrypted on its own; for ModePrivate
// groups, the owner AES-GCM-seals the Text before publishing to
// the feed (same shape as a direct owner Send). The dmsg session
// gives transport encryption between the member and the owner.
type RelayMessage struct {
	SenderPK cipher.PubKey `json:"sender_pk"`
	Text     string        `json:"text"`
	TS       time.Time     `json:"ts"`
}

// relayMaxFrameSize bounds the claimed-length sanity check on the
// owner's read side. 64 KiB matches every other framed-wire in the
// skychat tree.
const relayMaxFrameSize = 64 * 1024

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

// openOwner brings up a publisher with the member allowlist plus
// the relay listener that members dial when submitting messages.
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

	// Relay listener: dmsg.Listen on the +1 port. Members dial here
	// to submit messages for re-publish. Failures here are non-fatal
	// — the group is still usable for owner-broadcast even if no
	// relay listener comes up; just log + continue with relay = off.
	relayPort := cfg.Record.Port + relayPortOffset
	listener, err := cfg.DmsgC.Listen(relayPort)
	if err != nil {
		log.WithError(err).WithField("port", relayPort).
			Warn("group: owner relay listen failed; member-side send disabled")
		return &Session{cfg: cfg, port: cfg.Record.Port, pub: pub, log: log}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{
		cfg: cfg, port: cfg.Record.Port, pub: pub, log: log,
		relayListener: listener, relayCtx: ctx, relayCancel: cancel,
	}
	s.relayWG.Add(1)
	go s.acceptRelay()
	return s, nil
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

// Send publishes a chat message. Owner-side only: members must
// SubmitToOwner instead. Returns an error if invoked on a
// member-role session.
//
// For ModePrivate groups, the body is AES-256-GCM-sealed before
// being stored as the leaf value. The path itself (timestamp +
// seq) stays plaintext.
func (s *Session) Send(text string) error {
	if s.pub == nil || s.cfg.Record.Role != RoleOwner {
		return errors.New("group: Send: only owner-role sessions can publish; members must SubmitToOwner")
	}
	return s.publishAs(s.cfg.MyPK, text, time.Now().UTC())
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
	if s.relayCancel != nil {
		s.relayCancel()
	}
	if s.relayListener != nil {
		if err := s.relayListener.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.relayListener = nil
	}
	s.relayWG.Wait()
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

// acceptRelay accepts member-relay streams in a loop until Close is
// called. Each accepted stream gets its own short-lived goroutine
// that reads one framed RelayMessage, validates the sender against
// the group's allowlist, publishes to the CXO feed with the
// sender's PK attributed, and closes the stream.
func (s *Session) acceptRelay() {
	defer s.relayWG.Done()
	for {
		c, err := s.relayListener.Accept()
		if err != nil {
			if s.relayCtx.Err() == nil {
				s.log.WithError(err).Debug("group: relay accept ended")
			}
			return
		}
		s.relayWG.Add(1)
		go func(c net.Conn) {
			defer s.relayWG.Done()
			defer c.Close() //nolint:errcheck
			s.handleRelay(c)
		}(c)
	}
}

// handleRelay reads one RelayMessage from c, validates the sender,
// and re-publishes via the same Send path the owner uses for its
// own messages — with sender attribution from the relay envelope
// instead of the owner's PK.
func (s *Session) handleRelay(c net.Conn) {
	payload, err := readFrame(c)
	if err != nil {
		s.log.WithError(err).Debug("group: relay read")
		return
	}
	var rm RelayMessage
	if err := json.Unmarshal(payload, &rm); err != nil {
		s.log.WithError(err).Debug("group: relay unmarshal")
		return
	}
	if rm.SenderPK == (cipher.PubKey{}) {
		s.log.Debug("group: relay rejected: empty sender PK")
		return
	}
	if !s.isMember(rm.SenderPK) {
		s.log.WithField("sender", rm.SenderPK.Hex()).
			Warn("group: relay rejected: sender not in allowlist")
		return
	}
	if rm.TS.IsZero() {
		rm.TS = time.Now().UTC()
	}
	if err := s.publishAs(rm.SenderPK, rm.Text, rm.TS); err != nil {
		s.log.WithError(err).Debug("group: relay publish")
		return
	}
}

// isMember returns whether pk appears in the group's Members
// allowlist. Owner is implicitly a member (uniqueWithSelf put them
// there at Create time) so an owner-side echo through the relay
// also validates cleanly.
func (s *Session) isMember(pk cipher.PubKey) bool {
	for _, m := range s.cfg.Record.Members {
		if m == pk {
			return true
		}
	}
	return false
}

// publishAs is the shared write path used by both owner-direct
// Send and member-relay handleRelay. Sender attribution is
// parametric: the owner-direct case passes its own PK, the relay
// case passes the relay envelope's sender PK so the feed records
// the actual author rather than the owner-as-conduit.
func (s *Session) publishAs(senderPK cipher.PubKey, text string, ts time.Time) error {
	if s.pub == nil || s.cfg.Record.Role != RoleOwner {
		return errors.New("group: publish: only owner-role sessions can publish")
	}
	msg := Message{SenderPK: senderPK, TS: ts}
	switch s.cfg.Record.Mode {
	case ModePublic:
		msg.Text = text
	case ModePrivate:
		ct, nonce, err := Encrypt(s.cfg.Record.AESKey, []byte(text))
		if err != nil {
			return fmt.Errorf("group: publishAs: %w", err)
		}
		msg.Ciphertext = ct
		msg.Nonce = nonce
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("group: publishAs: marshal: %w", err)
	}
	seq := s.seq.Add(1)
	path := MessagePathPrefix + "/" + strconv.FormatInt(ts.UnixNano(), 10) + "/" + strconv.FormatUint(seq, 10)
	if err := s.pub.Put(path, body); err != nil {
		return fmt.Errorf("group: publishAs: put %q: %w", path, err)
	}
	return nil
}

// SubmitToOwner is the member-side outbound path: dial the owner's
// relay listener, write a framed RelayMessage, close. The owner's
// acceptRelay handler validates + republishes. Returns an error if
// invoked on an owner-role session (owners use Send directly) or
// if the dial fails.
func (s *Session) SubmitToOwner(ctx context.Context, text string) error {
	if s.cfg.Record.Role != RoleMember {
		return errors.New("group: SubmitToOwner: only member-role sessions submit via relay")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	addr := dmsg.Addr{PK: s.cfg.Record.OwnerPK, Port: s.cfg.Record.Port + relayPortOffset}
	stream, err := s.cfg.DmsgC.DialStream(dialCtx, addr)
	if err != nil {
		return fmt.Errorf("group: SubmitToOwner: dial owner relay: %w", err)
	}
	defer stream.Close() //nolint:errcheck
	rm := RelayMessage{SenderPK: s.cfg.MyPK, Text: text, TS: time.Now().UTC()}
	body, err := json.Marshal(rm)
	if err != nil {
		return fmt.Errorf("group: SubmitToOwner: marshal: %w", err)
	}
	if err := writeFrame(stream, body); err != nil {
		return fmt.Errorf("group: SubmitToOwner: write: %w", err)
	}
	return nil
}

// readFrame / writeFrame mirror the visor-app skychat post-#2504
// length-prefixed wire so a member's relay write and an owner's
// relay read interoperate with the same bit layout other framed
// wires in this tree use.
func readFrame(c net.Conn) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(c, hdr[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(hdr[:])
	if length == 0 {
		return nil, errors.New("group: zero-length frame")
	}
	if length > relayMaxFrameSize {
		return nil, fmt.Errorf("group: frame %d > max %d", length, relayMaxFrameSize)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(c, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func writeFrame(c net.Conn, payload []byte) error {
	if len(payload) == 0 {
		return errors.New("group: empty payload")
	}
	if len(payload) > relayMaxFrameSize {
		return fmt.Errorf("group: payload %d > max %d", len(payload), relayMaxFrameSize)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload))) //nolint:gosec
	if _, err := c.Write(hdr[:]); err != nil {
		return err
	}
	_, err := c.Write(payload)
	return err
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
