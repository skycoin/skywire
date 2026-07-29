// Package group cmd/apps/skychat/group/session_lifecycle_test.go
//
// Coverage for the Session bring-up path that the rest of this package's tests
// skipped as "needs a mesh": Open's validation arms, openOwner / openMember /
// newPublisher, the owner heartbeat loop, ReplayHistoryThrough, and
// BroadcastRoster.
//
// None of this needs dmsg. group.Config has a native-TCP mode (TCPListenAddr +
// PeerAddrs) — the same one commands.startCXOGroup uses for --cxo-group — and
// in that mode newPublisher goes through treestore's TCP transport with an
// in-memory CXDS, so a real session comes up on 127.0.0.1 with a real
// publisher, real signing and a real feed.
//
// SCOPE: every test here is SINGLE-NODE. Session.Connect / ReconnectPeer and
// cross-node delivery are deliberately NOT covered, because the native-TCP CXO
// link is not reliably establishable in-process: it connects in isolation but
// blocks out its deadline under load, since a subscriber whose publisher has no
// fresh root to announce waits rather than syncing what is already on the feed.
// Production lives with exactly that — commands.startCXOGroup treats its first
// Connect as best-effort and drives per-peer reconnects on a ticker, and
// manager.detectStaleAndReconnect does the same — but a unit test built on it
// is just flaky, which is worse than no test. Cross-node group delivery belongs
// in the e2e lane alongside the dmsg-backed pairing integration tests.
package group

import (
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// --- fixture ----------------------------------------------------------------

// freeAddr reserves a loopback port and releases it for the session to bind.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close() //nolint:errcheck
	return addr
}

// tcpNode is one participant in a native-TCP group.
type tcpNode struct {
	pk    cipher.PubKey
	sk    cipher.SecKey
	addr  string
	sess  *Session
	inbox *msgInbox
}

// msgInbox collects messages delivered to a session's handler.
type msgInbox struct {
	mu   sync.Mutex
	msgs []Message
}

func (i *msgInbox) deliver(_ string, _ cipher.PubKey, m Message) {
	i.mu.Lock()
	i.msgs = append(i.msgs, m)
	i.mu.Unlock()
}

func (i *msgInbox) snapshot() []Message {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]Message(nil), i.msgs...)
}

// openTCPGroup opens BOTH roles of a native-TCP group. Nothing here connects
// them (see the scope note at the top of the file) — opening both is how
// openOwner AND openMember get exercised, and it lets a test assert the
// structural difference between the two.
func openTCPGroup(t *testing.T, heartbeat time.Duration) (owner, member *tcpNode) {
	t.Helper()
	id := uuid.NewString()

	ownerPK, ownerSK := cipher.GenerateKeyPair()
	memberPK, memberSK := cipher.GenerateKeyPair()
	ownerAddr, memberAddr := freeAddr(t), freeAddr(t)
	members := []cipher.PubKey{ownerPK, memberPK}

	rec := func(role Role) Record {
		return Record{
			ID:      id,
			Name:    "tcp-group",
			OwnerPK: ownerPK,
			Port:    8870,
			Mode:    ModePublic,
			Members: members,
			Admins:  members, // full mesh: everyone follows everyone
			Role:    role,
		}
	}

	open := func(pk cipher.PubKey, sk cipher.SecKey, addr string, peers map[cipher.PubKey]string, role Role, hb time.Duration) *tcpNode {
		box := &msgInbox{}
		s, err := Open(Config{
			MyPK: pk, MySK: sk,
			Record:            rec(role),
			TCPListenAddr:     addr,
			PeerAddrs:         peers,
			InMemoryDB:        true,
			BatchWindow:       20 * time.Millisecond,
			HeartbeatInterval: hb,
			Logger:            logging.MustGetLogger("group-tcp-" + string(role)),
		})
		if err != nil {
			t.Fatalf("Open(%s): %v", role, err)
		}
		t.Cleanup(func() { _ = s.Close() }) //nolint:errcheck
		s.SetMessageHandler(box.deliver)
		return &tcpNode{pk: pk, sk: sk, addr: addr, sess: s, inbox: box}
	}

	owner = open(ownerPK, ownerSK, ownerAddr, map[cipher.PubKey]string{memberPK: memberAddr}, RoleOwner, heartbeat)
	member = open(memberPK, memberSK, memberAddr, map[cipher.PubKey]string{ownerPK: ownerAddr}, RoleMember, 0)
	return owner, member
}

// --- Open validation --------------------------------------------------------

func TestOpen_Validation(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	base := func() Config {
		return Config{
			MyPK: pk, MySK: sk,
			TCPListenAddr: "127.0.0.1:0",
			InMemoryDB:    true,
			Record: Record{
				ID: uuid.NewString(), OwnerPK: pk, Port: 8870,
				Mode: ModePublic, Role: RoleOwner,
			},
		}
	}

	cases := []struct {
		name string
		mut  func(*Config)
		want string
	}{
		{"no transport", func(c *Config) { c.TCPListenAddr = "" }, "TCPListenAddr"},
		{"no record id", func(c *Config) { c.Record.ID = "" }, "Record.ID"},
		{"no owner pk", func(c *Config) { c.Record.OwnerPK = cipher.PubKey{} }, "OwnerPK"},
		{"no port", func(c *Config) { c.Record.Port = 0 }, "Port"},
		{"invalid mode", func(c *Config) { c.Record.Mode = "sideways" }, "Mode"},
		{"private without key", func(c *Config) { c.Record.Mode = ModePrivate }, "AESKey"},
		{"short aes key", func(c *Config) {
			c.Record.Mode = ModePrivate
			c.Record.AESKey = make([]byte, 16)
		}, "AESKey"},
		{"no data dir", func(c *Config) { c.InMemoryDB = false }, "DataDir"},
		{"invalid role", func(c *Config) { c.Record.Role = "bystander" }, "Role"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := base()
			c.mut(&cfg)
			s, err := Open(cfg)
			if err == nil {
				_ = s.Close() //nolint:errcheck
				t.Fatalf("expected an error mentioning %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want it to name %q", err, c.want)
			}
		})
	}
}

// --- ReplayHistoryThrough ---------------------------------------------------

// TestSessionTCP_ReplayHistoryThrough — the wasm visor calls this after a Join
// to backfill history that arrived before its handler existed.
func TestSessionTCP_ReplayHistoryThrough(t *testing.T) {
	owner, _ := openTCPGroup(t, 0)

	sent := []string{"first", "second", "third"}
	for _, txt := range sent {
		if err := owner.sess.Send(txt); err != nil {
			t.Fatalf("Send(%q): %v", txt, err)
		}
	}
	// Self-echo is synchronous inside publishAs, but the batch window still has
	// to flush before the leaves are walkable.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && len(owner.inbox.snapshot()) < len(sent) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := len(owner.inbox.snapshot()); n < len(sent) {
		t.Fatalf("owner self-echoed %d of %d sends", n, len(sent))
	}

	replay := &msgInbox{}
	owner.sess.ReplayHistoryThrough(replay.deliver, 50)

	got := replay.snapshot()
	if len(got) < len(sent) {
		t.Fatalf("replayed %d messages, want at least %d: %+v", len(got), len(sent), got)
	}
	// Replay is ordered by message timestamp, not by feed path.
	for i := 1; i < len(got); i++ {
		if got[i].TS.Before(got[i-1].TS) {
			t.Errorf("replay out of order at %d: %v before %v", i, got[i].TS, got[i-1].TS)
		}
	}
	texts := map[string]bool{}
	for _, m := range got {
		texts[m.Text] = true
	}
	for _, want := range sent {
		if !texts[want] {
			t.Errorf("replay is missing %q", want)
		}
	}

	// Guards: a nil handler or a non-positive cap is a no-op, not a panic.
	owner.sess.ReplayHistoryThrough(nil, 10)
	owner.sess.ReplayHistoryThrough(replay.deliver, 0)
}

// --- heartbeat --------------------------------------------------------------

// TestSessionTCP_HeartbeatPublishes — the owner-side heartbeat is what lets
// members detect a silently-stalled subscriber. It rides the normal publish
// path but must NOT surface as chat content.
func TestSessionTCP_HeartbeatPublishes(t *testing.T) {
	owner, _ := openTCPGroup(t, 50*time.Millisecond)

	deadline := time.Now().Add(20 * time.Second)
	var seen bool
	for time.Now().Before(deadline) && !seen {
		owner.sess.pub.Walk(MessagePathPrefix, func(_ string, value []byte) bool {
			if strings.Contains(string(value), HeartbeatMarker) {
				seen = true
				return false
			}
			return true
		})
		if !seen {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if !seen {
		t.Fatal("the owner heartbeat never reached the feed")
	}

	// ...but is filtered out of the chat handler, so it never renders as a message.
	for _, m := range owner.inbox.snapshot() {
		if strings.Contains(m.Text, HeartbeatMarker) {
			t.Errorf("a heartbeat leaked into the chat handler: %+v", m)
		}
	}
}

// --- BroadcastRoster --------------------------------------------------------

// TestBroadcastRoster_AdminOnly — the roster re-broadcast is how late joiners
// hydrate the member set. Only admins may issue it; a non-admin publishing
// roster mutations would let any member rewrite the group.
func TestBroadcastRoster_AdminOnly(t *testing.T) {
	founder, fsk := cipher.GenerateKeyPair()
	a, _ := cipher.GenerateKeyPair()
	outsider, osk := cipher.GenerateKeyPair()

	// Founder is implicitly an admin → publishes roster mutations.
	own := newRosterSession(t, founder, fsk, Record{
		ID: uuid.NewString(), OwnerPK: founder, Role: RoleOwner, Mode: ModePublic,
		Members: []cipher.PubKey{founder, a}, Admins: []cipher.PubKey{founder, a},
	})
	own.BroadcastRoster()

	var rosterLeaves int
	own.pub.Walk(RosterPathPrefix, func(string, []byte) bool { rosterLeaves++; return true })
	if rosterLeaves == 0 {
		t.Error("an admin's BroadcastRoster should publish roster mutations")
	}

	// A non-admin publishes nothing.
	notAdmin := newRosterSession(t, outsider, osk, Record{
		ID: uuid.NewString(), OwnerPK: founder, Role: RoleMember, Mode: ModePublic,
		Members: []cipher.PubKey{founder, outsider}, Admins: []cipher.PubKey{founder},
	})
	notAdmin.BroadcastRoster()

	var outsiderLeaves int
	notAdmin.pub.Walk(RosterPathPrefix, func(string, []byte) bool { outsiderLeaves++; return true })
	if outsiderLeaves != 0 {
		t.Errorf("a non-admin published %d roster leaves, want 0", outsiderLeaves)
	}

	// A publisher-less session is a no-op rather than a nil-deref.
	(&Session{}).BroadcastRoster()
}

// TestOpen_RoleShapes pins the structural difference openOwner / openMember
// produce, which is what the rest of the session logic branches on: only a
// member carries the legacy owner-feed subscriber, and both roles follow every
// other member under the full-mesh admin roster.
func TestOpen_RoleShapes(t *testing.T) {
	owner, member := openTCPGroup(t, 0)

	if owner.sess.sub != nil {
		t.Error("an owner session must not have a legacy owner-feed subscriber")
	}
	if member.sess.sub == nil {
		t.Error("a member session must have the legacy owner-feed subscriber")
	}
	if owner.sess.pub == nil || member.sess.pub == nil {
		t.Error("both roles publish their own feed post-federation")
	}
	if got := owner.sess.PeerPKs(); len(got) != 1 || got[0] != member.pk {
		t.Errorf("owner peerSubs = %v, want just the member", hexes(got))
	}
	if got := member.sess.PeerPKs(); len(got) != 1 || got[0] != owner.pk {
		t.Errorf("member peerSubs = %v, want just the owner", hexes(got))
	}
	// Ports and ids come straight off the record.
	if owner.sess.ID() != member.sess.ID() {
		t.Error("both roles share the group id")
	}
	if owner.sess.Port() != 8870 {
		t.Errorf("Port() = %d, want the record's 8870", owner.sess.Port())
	}
	// A fresh session has seen nothing from its peer yet.
	if !owner.sess.PeerLastInbound(member.pk).IsZero() {
		t.Error("a freshly opened session should have no per-peer inbound yet")
	}
}

// TestSubmitToOwner_MemberRoleOnly — the relay is the member's fallback send
// path. An owner has nowhere to relay TO (it is the relay), so calling it on an
// owner session is a programming error and must be reported rather than
// silently dialing itself.
func TestSubmitToOwner_MemberRoleOnly(t *testing.T) {
	owner, _ := openTCPGroup(t, 0)

	err := owner.sess.SubmitToOwner(t.Context(), "owner relaying to itself")
	if err == nil {
		t.Fatal("SubmitToOwner on an owner-role session should error")
	}
	if !strings.Contains(err.Error(), "member-role") {
		t.Errorf("err = %v, want it to name the member-role requirement", err)
	}
}
