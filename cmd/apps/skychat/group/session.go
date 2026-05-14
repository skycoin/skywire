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
	cryptoRand "crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// cryptoRandRead aliases crypto/rand.Read so newRelayMsgID reads
// 8 bytes of entropy without re-importing the package's name at
// each call site.
var cryptoRandRead = cryptoRand.Read

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

	// HeartbeatInterval, when > 0, makes owner-role sessions emit a
	// periodic no-op heartbeat probe into the group feed. Members
	// observe the heartbeats via onUpdate, which bumps lastInboundNs
	// (the unified liveness signal) just as a real message would.
	// Heartbeats are what keep IsSubscriberAlive true on quiet groups
	// where no chat traffic flows for long stretches. Zero disables
	// heartbeat emission entirely (useful for tests and for
	// deployments that want to opt out of the ~1KB/min/group wire
	// cost). Recommended production value: 30s.
	HeartbeatInterval time.Duration
}

// Session is one live group as this visor knows it — either as the
// owner (publisher) or as a member (subscriber). Safe for concurrent
// use after Open returns; Send / Close may be called from any
// goroutine.
type Session struct {
	cfg  Config
	port uint16

	// pub is set on EVERY role. Pre-D1 it was owner-only with members
	// using a throwaway publisher purely to host their subscriber's
	// CXO node. Post-D1 every Session writes its own messages to its
	// own publisher (sender attribution survives via the path prefix
	// that includes Session.MyPK; see publishAs). Members' publishers
	// have an allowlist of Record.Members so other peers can subscribe
	// to them; owners' publishers also serve the group config / heartbeat
	// channel (still on the same Publisher; differentiated by path
	// prefix at the leaf level).
	pub *treestore.Publisher
	// sub is the legacy single subscriber pointed at the OWNER's feed.
	// Still used during D1 for backward-compat with old owners that
	// haven't migrated to a per-PK config feed; subsumed by peerSubs
	// once D4's migration path lands and the relay flow is fully
	// retired in D5. Member role only; nil on owner sessions.
	sub *treestore.Subscriber

	// peerSubs holds one CXO subscriber per OTHER member of the group
	// (every member-PK in Record.Members that isn't this Session's
	// MyPK). Both owner and member roles populate this map — owner
	// follows every member's per-PK message feed, members follow every
	// other member's. Allows a member to publish messages locally
	// without going through the owner relay, and other members
	// observe the leaves directly via their own subscriber.
	//
	// Lifecycle: openOwner / openMember populate this map at Open
	// time. Connect dials each entry. SetAllowlist (D2) adds/removes
	// entries as the group's member set changes. Close tears all
	// entries down.
	//
	// Guarded by peerSubsMu since SetAllowlist mutates while
	// onUpdate / Send concurrently read.
	peerSubsMu sync.RWMutex
	peerSubs   map[cipher.PubKey]*treestore.Subscriber

	// relayDmsg / relaySkynet are set on the owner side: parallel
	// listeners on Record.Port+1 over dmsg AND over the skywire
	// router (appnet.TypeSkynet). Members dial whichever they have
	// available, write a framed RelayMessage envelope, and the
	// owner-side handler re-publishes into the CXO feed with the
	// original sender's PK attributed (see acceptRelay). Same port
	// on both transports — the principle that every visor port
	// served on dmsg is also served on skynet at the same port.
	// relaySkynet is bound asynchronously since the SkywireNetworker
	// usually registers later in init than the group manager starts.
	// Either listener may be nil if its transport never came up.
	relayDmsg   net.Listener
	relaySkynet net.Listener
	relayMu     sync.Mutex // guards relaySkynet assignment from the binder goroutine
	relayCtx    context.Context
	relayCancel context.CancelFunc
	relayWG     sync.WaitGroup

	// seq disambiguates messages with the same nanosecond timestamp.
	seq atomic.Uint64

	// membersMu guards live updates to the owner-side member
	// allowlist used by isMember (the relay-side gate). Open seeds
	// it from cfg.Record.Members; SetAllowlist replaces it. The
	// CXO subscriber-side allowlist on pub is a separate state
	// living in publisher.allowState — kept in sync but accessed
	// through different code paths, so both need updating.
	membersMu sync.RWMutex
	members   []cipher.PubKey

	handler MessageHandler
	log     *logging.Logger

	// lastInboundNs is the unified liveness signal for this session,
	// in unix-nanoseconds. Set on every observable inbound event —
	// onUpdate firing on the CXO subscriber (member side) and the
	// owner-self-echo wrapper observing publishAs (owner side). Both
	// heartbeats and real chat messages bump it; the distinction
	// stops mattering here because any leaf arrival is positive
	// proof the subscriber is currently attached and pulling.
	//
	// Pre-refactor (#2530..#2534) this role was split across four
	// fields — Session.subAlive (Connect/Close), Session.lastHeartbeatNs
	// (heartbeat-only), Record.LastMessageAt (chat-only persisted),
	// Record.Status (configuration state pulling double duty as health).
	// They kept disagreeing in subtle ways and each fix patched ONE
	// pairwise disagreement; the holistic answer is a single signal
	// computed not stored, with the persisted LastMessageAt kept as
	// a coarse-grained crash-recovery seed.
	//
	// Initialized from Record.LastMessageAt on session Open so a
	// freshly-resumed session doesn't appear instantly stale before
	// the first new inbound arrives. Zero only if no traffic has
	// ever been seen for this group.
	//
	// Atomic so the chat-app's /status hot path can read without any
	// session-level mutex.
	lastInboundNs atomic.Int64

	// closed is set true on Close so IsSubscriberAlive can short-
	// circuit to false for torn-down sessions without consulting
	// lastInbound timing. Owner-role sessions ignore this — they're
	// always alive while the publisher is running.
	closed atomic.Bool

	// heartbeatCancel stops the owner-side heartbeat emission loop.
	// nil for member-role sessions or when HeartbeatInterval=0.
	heartbeatCancel context.CancelFunc
	heartbeatWG     sync.WaitGroup
}

// subscriberStaleThreshold is defined in manager.go (it's used by
// both Session.IsSubscriberAlive and Manager.detectStaleAndReconnect
// so it sits in the package-level const block there).

// HeartbeatMarker is the fixed body of an owner-emitted heartbeat
// message. Subscribers detect it by exact-match on Message.Text and
// DO NOT bubble the message up to the user handler / inbox —
// heartbeats are wire noise, not chat. The liveness side effect
// (bumping lastInboundNs) happens unconditionally at the top of
// onUpdate, before the heartbeat filter runs.
//
// Picked to be (a) unambiguous (operator typing this verbatim into a
// chat input would be a deliberate spoof, and the sender PK check
// still requires it to come from the owner) and (b) recognizable in
// logs / pcap dumps without decoding the JSON envelope around it.
const HeartbeatMarker = "__skychat_group_heartbeat__"

// IsHeartbeat returns true for messages that are owner-emitted
// heartbeat probes rather than chat content. Exposed so the visor's
// inbox layer (pkg/visor/group.go) can filter heartbeats from the
// poll ring without re-importing the constant in two places.
func IsHeartbeat(msg Message) bool { return msg.Text == HeartbeatMarker }

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
// MsgID, when set by the member, requests a RelayAck reply on the
// same stream after the owner's publishAs succeeds. Pre-#group-
// relay-ack members didn't set it; pre-#group-relay-ack owners
// don't reply. Both directions are backward-compatible: a missing
// MsgID just skips the ack roundtrip, a missing ack reply just
// times out cleanly on the member's read.
//
// Note: this envelope is not encrypted on its own; for ModePrivate
// groups, the owner AES-GCM-seals the Text before publishing to
// the feed (same shape as a direct owner Send). The dmsg session
// gives transport encryption between the member and the owner.
type RelayMessage struct {
	SenderPK cipher.PubKey `json:"sender_pk"`
	Text     string        `json:"text"`
	TS       time.Time     `json:"ts"`
	MsgID    string        `json:"msg_id,omitempty"`
}

// RelayAck is the owner's reply to a RelayMessage with MsgID set.
// Written on the same stream after the owner's publishAs has
// returned successfully — i.e. the owner has committed the leaf to
// the CXO feed. The member receives RelayAck as positive
// confirmation that the message reached the feed (not just that
// the relay stream accepted the bytes).
//
// On the wire: same length-prefixed framing as RelayMessage, JSON
// body with {ack: true, msg_id: <echoed>}. The Ack=true sentinel
// distinguishes a RelayAck from any future stream-bound envelope
// type the relay protocol may grow.
type RelayAck struct {
	Ack   bool   `json:"ack"`
	MsgID string `json:"msg_id,omitempty"`
}

// relayMaxFrameSize bounds the claimed-length sanity check on the
// owner's read side. 64 KiB matches every other framed-wire in the
// skychat tree.
const relayMaxFrameSize = 64 * 1024

// relayAckReadTimeout caps how long the member waits for the
// owner's RelayAck before giving up. Sized for "owner publishAs
// completes in a normal CXO commit window" — bbolt-backed
// publishers typically commit in tens of ms, but a cold-cache
// commit or a slow disk can stretch. 5s is generous enough to
// avoid spurious timeouts on healthy owners while still surfacing
// a dead-owner case promptly.
const relayAckReadTimeout = 5 * time.Second

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
		// Group message CXDS is content-addressed; subscribers
		// re-sync from the publisher's in-memory tree on reconnect.
		// Losing a torn batch at crash time is acceptable.
		NoSyncCXDS: true,
	})
	if err != nil {
		return nil, fmt.Errorf("group: Open owner: build publisher: %w", err)
	}

	// Relay listeners: dmsg + skynet on the +1 port. Members dial
	// whichever transport they prefer (skynet first, dmsg fallback).
	// Failures on either side are non-fatal — the group is still
	// usable for owner-broadcast even if neither listener comes up;
	// only the worst case (both fail) disables member-side send.
	relayPort := cfg.Record.Port + relayPortOffset
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{
		cfg: cfg, port: cfg.Record.Port, pub: pub, log: log,
		members:  append([]cipher.PubKey(nil), cfg.Record.Members...),
		relayCtx: ctx, relayCancel: cancel,
		peerSubs: make(map[cipher.PubKey]*treestore.Subscriber),
	}

	// Per-member subscribers: each non-owner-self member publishes
	// their own message feed at memberPK:Record.Port. Owner follows
	// every member's feed directly so the relay-hop becomes
	// vestigial. Subs are created here but not Connect'd; Connect
	// dials them all on the operator-controlled timing.
	for _, peerPK := range cfg.Record.Members {
		if peerPK == cfg.MyPK {
			continue
		}
		ps, err := treestore.NewSubscriberOnNode(pub.Node(), peerPK, treestore.SubConfig{Logger: log})
		if err != nil {
			log.WithError(err).WithField("peer", peerPK.String()).
				Warn("group: owner peer-sub create failed; group still usable via legacy relay path")
			continue
		}
		ps.SetPrefixes([]string{MessagePathPrefix})
		ps.OnUpdate(s.onUpdate)
		s.peerSubs[peerPK] = ps
	}
	if dmsgLis, err := cfg.DmsgC.Listen(relayPort); err != nil {
		log.WithError(err).WithField("port", relayPort).
			Warn("group: owner relay dmsg listen failed")
	} else {
		s.relayDmsg = dmsgLis
		s.relayWG.Add(1)
		go s.acceptRelayOn(dmsgLis, "dmsg")
	}
	s.relayWG.Add(1)
	go s.bindRelaySkynet(relayPort)

	// Heartbeat emission. Publishes a tiny no-op message to the feed
	// every HeartbeatInterval so members can detect a silently-stalled
	// CXO subscriber as "haven't seen a heartbeat in N intervals"
	// independent of whether real chat traffic is flowing. Zero
	// interval disables (useful for tests + low-overhead deployments).
	if cfg.HeartbeatInterval > 0 {
		hbCtx, hbCancel := context.WithCancel(context.Background())
		s.heartbeatCancel = hbCancel
		s.heartbeatWG.Add(1)
		go s.runHeartbeatLoop(hbCtx, cfg.HeartbeatInterval)
	}
	return s, nil
}

// runHeartbeatLoop is the owner-side periodic heartbeat publisher.
// Emits a HeartbeatMarker message every interval until ctx is
// canceled (Close path). The publishAs call goes through the normal
// owner write path, which means heartbeats also exercise the
// publisher's batch + CXO save machinery — a healthy probe that
// implicitly catches publisher-side wedges too.
//
// Failures are debug-logged and the loop continues. A persistent
// publisher problem will show up as both "publishAs heartbeat
// failed" repeated in the log AND members' Session.LastInbound()
// going stale (driving subscriber_alive=false in /status); the
// latter is the operator-facing signal.
func (s *Session) runHeartbeatLoop(ctx context.Context, interval time.Duration) {
	defer s.heartbeatWG.Done()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.publishAs(s.cfg.MyPK, HeartbeatMarker, time.Now().UTC()); err != nil {
				s.log.WithError(err).Debug("group: heartbeat publish failed")
			}
		}
	}
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
	// Member's throwaway local CXO node uses Record.Port — the SAME
	// port number the owner's publisher binds on the owner's visor.
	// Two visors can both bind the same DMSG port number; the port is
	// per-PK. The DMSGFactory in CXO uses one port for both Listen
	// AND outbound Dial (see cxo/node/transport/dmsg.go:89), so the
	// subscriber's ConnectPK to the owner dials at the same port —
	// which MUST be Record.Port to hit the owner's CXO publisher, not
	// the relay listener (which is at Record.Port+1).
	//
	// Local collision risk: theoretical only — happens if the same
	// visor is also publisher of a different group on the SAME
	// Record.Port (~1/65535 birthday collision per-pair across
	// groups). When it does happen, bind fails with a clear "address
	// in use" error; not silent corruption.
	pub, err := treestore.NewWithDMSG(cfg.DmsgC, cfg.MySK, treestore.PubConfig{
		BatchWindow: cfg.BatchWindow,
		Logger:      log,
		DataDir:     filepath.Join(cfg.DataDir, "group", cfg.Record.ID, "member"),
		DmsgPort:    cfg.Record.Port,
		// D1: every group member publishes their own message feed,
		// and the rest of the group subscribes to it directly
		// (bypassing the owner-relay hop). Allowlist is the full
		// member set so peers can subscribe. Pre-D1 this was empty
		// (the member's publisher existed only to host the
		// subscriber's CXO node; no one ever subscribed to it).
		SubscriberAllowlist: append([]cipher.PubKey(nil), cfg.Record.Members...),
		// Member-side throwaway node — pure cache, no durability
		// requirement at all.
		NoSyncCXDS: true,
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
	s := &Session{
		cfg: cfg, port: cfg.Record.Port, pub: pub, sub: sub, log: log,
		peerSubs: make(map[cipher.PubKey]*treestore.Subscriber),
	}

	// Federated mode: subscribe to every OTHER member's feed,
	// INCLUDING the owner's. The legacy `sub` field above remains
	// wired as a backward-compat fallback for owners on pre-federated
	// binaries, but new owners publish to their own feed just like
	// every other member — so peerSubs covers them too.
	for _, peerPK := range cfg.Record.Members {
		if peerPK == cfg.MyPK {
			continue
		}
		ps, err := treestore.NewSubscriberOnNode(pub.Node(), peerPK, treestore.SubConfig{Logger: log})
		if err != nil {
			log.WithError(err).WithField("peer", peerPK.String()).
				Warn("group: member peer-sub create failed; will still receive owner-relayed messages")
			continue
		}
		ps.SetPrefixes([]string{MessagePathPrefix})
		ps.OnUpdate(s.onUpdate)
		s.peerSubs[peerPK] = ps
	}
	// Seed lastInboundNs from the persisted LastMessageAt so a
	// resumed session doesn't look instantly stale before the first
	// new inbound arrives. Zero LastMessageAt (brand-new join, no
	// traffic yet) seeds zero — IsSubscriberAlive will report false
	// until Connect succeeds or the first inbound lands, both of
	// which bump the timestamp.
	if !cfg.Record.LastMessageAt.IsZero() {
		s.lastInboundNs.Store(cfg.Record.LastMessageAt.UnixNano())
	}
	sub.OnUpdate(s.onUpdate)
	return s, nil
}

// Connect dials the owner's CXO node and starts the subscribe
// handshake. Owner sessions are a no-op (they don't subscribe to
// themselves). Idempotent: returns nil if already connected.
//
// On success, bumps lastInboundNs to now — Connect itself is
// positive evidence the subscriber side is healthy, so the
// liveness window starts ticking from here rather than from "no
// inbound yet". On error, lastInboundNs stays at whatever it was
// (seeded from Record.LastMessageAt at Open) and the Manager's
// background reconnect loop keeps retrying.
func (s *Session) Connect() error {
	// Owner-feed subscriber (legacy path) — only set on member sessions.
	// Failure here is fatal for the member's read path TODAY since
	// peerSubs may not be enough until every visor migrates. Once D4
	// retires the relay/owner-feed flow we'll relax this to "any sub
	// succeeded" semantics.
	if s.sub != nil {
		if err := s.sub.Connect(s.cfg.Record.OwnerPK); err != nil {
			return fmt.Errorf("group: Connect: %w", err)
		}
	}
	// D1 per-PK peer subscribers. Best-effort: if a single peer's
	// publisher isn't reachable yet (e.g. they restarted), that one
	// connect fails silently and the reconnect loop retries. Don't
	// fail the whole Connect — partial connectivity is better than
	// none, and at least the owner-feed legacy path is up.
	s.peerSubsMu.RLock()
	peers := make(map[cipher.PubKey]*treestore.Subscriber, len(s.peerSubs))
	for k, v := range s.peerSubs {
		peers[k] = v
	}
	s.peerSubsMu.RUnlock()
	for pk, ps := range peers {
		if err := ps.Connect(pk); err != nil {
			s.log.WithError(err).WithField("peer", pk.String()).
				Debug("group: Connect: peer-sub Connect failed; will retry on next reconnect tick")
		}
	}
	s.lastInboundNs.Store(time.Now().UnixNano())
	return nil
}

// LastInbound returns the time at which this session most recently
// observed any inbound CXO event — a chat leaf, an owner heartbeat,
// or any other update. Zero if no traffic has ever been seen AND
// Record.LastMessageAt was zero at Open (so the seed yielded zero).
//
// Used by Manager.detectStaleAndReconnect to identify subscribers
// that have silently stopped pulling from the owner, and by
// IsSubscriberAlive to compute the liveness boolean operators see
// in /status.
func (s *Session) LastInbound() time.Time {
	ns := s.lastInboundNs.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns).UTC()
}

// IsSubscriberAlive reports whether the subscriber side of this
// session is currently live. Surfaced through the chat-app's
// /status endpoint as the per-group `subscriber_alive` field so
// operators can spot a member session whose Connect failed silently
// or whose subscriber has been torn down.
//
// Semantics — computed, not stored:
//   - Owner-role sessions: always true (no subscriber to track; the
//     publisher side is owned by this visor and there's nothing to
//     "lose connection" to).
//   - Member-role sessions, after Close: false (the session has been
//     explicitly torn down; the underlying CXO subscriber is gone).
//   - Member-role sessions, no inbound ever: false (we never had
//     evidence the subscriber attached).
//   - Member-role sessions, any prior inbound: true iff the last
//     inbound is within subscriberStaleThreshold. Stale beyond that
//     and the Manager's detectStaleAndReconnect loop will kick a
//     reconnect; the flag stays false until the next inbound (or a
//     successful Connect()) refreshes lastInboundNs.
//
// The unification was previously four-flagged (subAlive bool +
// lastHeartbeatNs + LastMessageAt + Status's health semantics).
// Each pair of those could disagree, and we patched the
// disagreements one by one (#2530, #2532, #2533, #2534). The fix
// here removes the disagreement class entirely: there is ONE
// timestamp, and aliveness is a pure function of it.
func (s *Session) IsSubscriberAlive() bool {
	if s.sub == nil {
		return true // owner role
	}
	if s.closed.Load() {
		return false
	}
	last := s.LastInbound()
	if last.IsZero() {
		return false
	}
	return time.Since(last) <= subscriberStaleThreshold
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
	if s.pub == nil {
		return errors.New("group: Send: session has no publisher")
	}
	// D1: every role publishes via its own publisher. Pre-D1 this was
	// owner-only and members went through SubmitToOwner → relay-listener
	// → owner re-publish. SubmitToOwner is retained for backward-compat
	// with non-migrated owners; D5 retires it.
	return s.publishAs(s.cfg.MyPK, text, time.Now().UTC())
}

// SetAllowlist updates the publisher's subscriber allowlist AND the
// owner-side relay-gate's view of the member set, both live. Owner-
// side only. Used when an invite is issued or a member is removed.
//
// Two gates that both need refreshing:
//
//   - publisher's CXO subscriber allowlist (s.pub.SetAllowlist) —
//     gates who can SUBSCRIBE to the CXO feed.
//   - relay-side isMember check (s.members) — gates who the relay
//     listener accepts framed RelayMessage envelopes from.
//
// Pre-fix, only the publisher side was refreshed; s.members was
// frozen at Open from cfg.Record.Members. A member added AFTER Open
// could subscribe to the feed (publisher accepted them), but their
// SubmitToOwner frames hit the relay listener and got rejected with
// "sender not in allowlist" because isMember walked the stale list.
// Visible symptom: member sends silently dropped on the owner; the
// member's local Record.Members showing the original allowlist while
// the owner had since added more peers.
func (s *Session) SetAllowlist(members []cipher.PubKey) error {
	if s.pub == nil || s.cfg.Record.Role != RoleOwner {
		return errors.New("group: SetAllowlist: only owner-role sessions have an allowlist")
	}
	snap := append([]cipher.PubKey(nil), members...)
	s.pub.SetAllowlist(append([]cipher.PubKey(nil), members...))
	s.membersMu.Lock()
	s.members = snap
	s.membersMu.Unlock()

	// D1: keep the owner's per-PK peer-sub map in sync with the new
	// allowlist. Add subscribers for newly-added members; close and
	// drop subscribers for removed members. Excludes self.
	desired := make(map[cipher.PubKey]struct{}, len(snap))
	for _, pk := range snap {
		if pk == s.cfg.MyPK {
			continue
		}
		desired[pk] = struct{}{}
	}
	s.peerSubsMu.Lock()
	// Drop removed peers — close their subs while still holding the
	// lock so a racing Connect doesn't pick up a half-torn sub.
	for pk, ps := range s.peerSubs {
		if _, keep := desired[pk]; !keep {
			_ = ps.Close() //nolint:errcheck,gosec
			delete(s.peerSubs, pk)
		}
	}
	// Add new peers.
	for pk := range desired {
		if _, exists := s.peerSubs[pk]; exists {
			continue
		}
		ps, err := treestore.NewSubscriberOnNode(s.pub.Node(), pk, treestore.SubConfig{Logger: s.log})
		if err != nil {
			s.log.WithError(err).WithField("peer", pk.String()).
				Warn("group: SetAllowlist: peer-sub create failed; will retry on next allowlist change")
			continue
		}
		ps.SetPrefixes([]string{MessagePathPrefix})
		ps.OnUpdate(s.onUpdate)
		// Best-effort connect — same semantics as Connect's per-peer
		// retry: a peer that's offline now will be picked up on the
		// next reconnect tick once the publisher comes back.
		if err := ps.Connect(pk); err != nil {
			s.log.WithError(err).WithField("peer", pk.String()).
				Debug("group: SetAllowlist: peer-sub Connect failed; will retry on next reconnect tick")
		}
		s.peerSubs[pk] = ps
	}
	s.peerSubsMu.Unlock()
	return nil
}

// Close tears down the subscriber + publisher (and the underlying
// CXO node, owned by the publisher). Idempotent.
func (s *Session) Close() error {
	var firstErr error
	// Mark closed first so any concurrent /status read sees the
	// session as dead even if Close races with a long Subscriber.Close.
	// IsSubscriberAlive short-circuits to false on this flag for
	// member-role sessions, so we don't have to also rewind
	// lastInboundNs.
	s.closed.Store(true)
	// Stop the owner-side heartbeat emitter (if running). No-op on
	// member sessions or when HeartbeatInterval=0 at Open time.
	if s.heartbeatCancel != nil {
		s.heartbeatCancel()
		s.heartbeatWG.Wait()
	}
	if s.relayCancel != nil {
		s.relayCancel()
	}
	if s.relayDmsg != nil {
		if err := s.relayDmsg.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.relayDmsg = nil
	}
	s.relayMu.Lock()
	skyLis := s.relaySkynet
	s.relaySkynet = nil
	s.relayMu.Unlock()
	if skyLis != nil {
		if err := skyLis.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	s.relayWG.Wait()
	if s.sub != nil {
		if err := s.sub.Close(); err != nil {
			firstErr = err
		}
		s.sub = nil
	}
	// D1 per-PK peer subscribers — close before the local publisher so
	// the subscribers' Disconnect frames flush through pub.Node() while
	// the node is still alive. Iterate over a snapshot under the lock,
	// then nil-out the map outside the loop so a concurrent SetAllowlist
	// can't observe a half-torn-down state.
	s.peerSubsMu.Lock()
	peers := s.peerSubs
	s.peerSubs = nil
	s.peerSubsMu.Unlock()
	for pk, ps := range peers {
		if err := ps.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		_ = pk
	}
	if s.pub != nil {
		if err := s.pub.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.pub = nil
	}
	return firstErr
}

// acceptRelayOn accepts member-relay streams from one listener (dmsg
// or skynet) in a loop until Close is called. Each accepted stream
// gets its own short-lived goroutine that reads one framed
// RelayMessage, validates the sender against the group's allowlist,
// publishes to the CXO feed with the sender's PK attributed, and
// closes the stream. The `transport` label is for diagnostic logs.
func (s *Session) acceptRelayOn(lis net.Listener, transport string) {
	defer s.relayWG.Done()
	for {
		c, err := lis.Accept()
		if err != nil {
			if s.relayCtx.Err() == nil {
				s.log.WithError(err).WithField("transport", transport).
					Debug("group: relay accept ended")
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

// bindRelaySkynet waits for the appnet SkywireNetworker to register
// (which happens during initRouter, typically after the group manager
// starts) and then binds a skynet listener on the relay port. Runs in
// a single goroutine for the life of the Session and exits when the
// relayCtx is canceled.
//
// Why deferred binding: the visor's init order brings up dmsg before
// the router. A Session that opens while dmsg is up but skynet isn't
// would otherwise miss the skynet listener forever. Polling with
// backoff is simple and bounded — once the networker is up, we bind
// once and switch to the same accept-loop shape as the dmsg side.
func (s *Session) bindRelaySkynet(port uint16) {
	defer s.relayWG.Done()
	addr := appnet.Addr{
		Net:    appnet.TypeSkynet,
		PubKey: s.cfg.MyPK,
		Port:   routing.Port(port),
	}
	backoff := 200 * time.Millisecond
	maxBackoff := 5 * time.Second
	for {
		if _, err := appnet.ResolveNetworker(appnet.TypeSkynet); err == nil {
			break
		}
		select {
		case <-s.relayCtx.Done():
			return
		case <-time.After(backoff):
			if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
		}
	}
	lis, err := appnet.ListenContext(s.relayCtx, addr)
	if err != nil {
		s.log.WithError(err).WithField("port", port).
			Warn("group: owner relay skynet listen failed")
		return
	}
	s.relayMu.Lock()
	if s.relayCtx.Err() != nil {
		s.relayMu.Unlock()
		_ = lis.Close() //nolint:errcheck
		return
	}
	s.relaySkynet = lis
	s.relayMu.Unlock()
	s.relayWG.Add(1)
	s.acceptRelayOn(lis, "skynet")
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
	// Send a RelayAck back so the member can distinguish "I wrote
	// bytes" from "owner committed the leaf to the CXO feed". Only
	// when the member set MsgID — empty MsgID is a pre-ack member
	// who can't parse the response, so writing one would be wasted
	// bytes and risk confusing the read side. Best-effort: a failed
	// ack write doesn't roll back the publish (the message IS in
	// the feed), but the member will see ErrRelayNoAck and can
	// decide policy.
	if rm.MsgID != "" {
		ack := RelayAck{Ack: true, MsgID: rm.MsgID}
		body, err := json.Marshal(ack)
		if err != nil {
			s.log.WithError(err).Debug("group: relay ack marshal")
			return
		}
		if err := writeFrame(c, body); err != nil {
			s.log.WithError(err).Debug("group: relay ack write")
		}
	}
}

// isMember returns whether pk appears in the group's Members
// allowlist. Owner is implicitly a member (uniqueWithSelf put them
// there at Create time) so an owner-side echo through the relay
// also validates cleanly.
//
// Reads the live s.members list (seeded from cfg.Record.Members at
// Open, refreshed by SetAllowlist on AddMember/RemoveMember). The
// stale cfg.Record.Members snapshot is not consulted here — that's
// the bug 02f9aa58 caught: a peer added AFTER Open would otherwise
// be silently rejected at the relay gate while passing the CXO
// subscriber-side allowlist gate.
func (s *Session) isMember(pk cipher.PubKey) bool {
	s.membersMu.RLock()
	defer s.membersMu.RUnlock()
	for _, m := range s.members {
		if m == pk {
			return true
		}
	}
	return false
}

// publishAs is the shared write path used by:
//   - owner-direct Send / heartbeat (owner publishes as itself)
//   - member-direct Send (D1 — member publishes as itself on its own feed)
//   - owner-side relay re-publish (legacy handleRelay path during D5
//     migration window — owner publishes as the relayed sender on the
//     OWNER's feed for backward-compat with subscribers that haven't
//     migrated to per-PK subscriptions yet)
//
// Sender attribution is parametric: each caller passes the PK that
// should appear as the message author. publishAs writes to THIS
// session's own publisher (s.pub) regardless of senderPK — every
// session has a publisher in D1, and a member writing to its own
// publisher with senderPK=cfg.MyPK is the normal D1 path.
func (s *Session) publishAs(senderPK cipher.PubKey, text string, ts time.Time) error {
	if s.pub == nil {
		return errors.New("group: publish: session has no publisher")
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
	// D1: include senderPK hex in the leaf path so messages from
	// different publishers attributed to different senders never
	// collide on the same (ts, seq) suffix. Pre-D1 every leaf was
	// authored by the owner so a single seq counter was sufficient;
	// post-D1 each publisher has its own counter and they MUST be
	// disambiguated on the path. The receiver's onUpdate doesn't
	// parse the path (attribution comes from the JSON body), so the
	// senderPK segment is purely a uniqueness key on the storage
	// side.
	path := MessagePathPrefix + "/" + senderPK.Hex() + "/" + strconv.FormatInt(ts.UnixNano(), 10) + "/" + strconv.FormatUint(seq, 10)
	if err := s.pub.Put(path, body); err != nil {
		return fmt.Errorf("group: publishAs: put %q: %w", path, err)
	}

	// Owner-side local echo: deliver to the local handler so the
	// owner's own inbox sees their sends. The original design assumed
	// the owner doesn't need to "hear themselves back" because the
	// skychat hub fans out locally before publishing — but that's
	// not how the visor-level group inbox path works. Without a
	// self-echo, `skywire cli skychat group listen` shows nothing
	// when the owner is the only participant, which is misleading
	// during testing (looks like the publish failed silently).
	//
	// We deliver the plaintext Message regardless of ModePrivate —
	// the local handler should see the same content the (decoded)
	// subscriber path would surface, not the encrypted bytes.
	// Filter heartbeat self-echoes out of the handler delivery —
	// they are wire-level liveness probes, not chat content. Still
	// bump lastInboundNs so the owner's own session has a fresh
	// liveness timestamp (useful symmetry with member sessions; the
	// detectStaleAndReconnect pass skips owners but other diagnostics
	// may look at it).
	if text == HeartbeatMarker {
		s.lastInboundNs.Store(time.Now().UnixNano())
		return nil
	}
	if h := s.handler; h != nil {
		h(s.cfg.Record.ID, senderPK, Message{
			SenderPK: senderPK,
			Text:     text,
			TS:       ts,
		})
	}
	return nil
}

// SubmitToOwner is the member-side outbound path: dial the owner's
// relay listener, write a framed RelayMessage, close. The owner's
// acceptRelay handler validates + republishes. Returns an error if
// invoked on an owner-role session (owners use Send directly) or
// if neither transport can reach the owner.
//
// Transport selection: skynet first (so groups work over arbitrary
// skywire transports including stcpr/sudph), dmsg fallback if the
// networker isn't registered yet or the skynet dial fails. The dmsg
// path is the bootstrap-time fallback and the failure recovery for
// peers whose router can't currently reach the owner. Matches the
// general principle that every visor port served on dmsg is also
// served on skynet — and clients prefer skynet but tolerate dmsg.
func (s *Session) SubmitToOwner(ctx context.Context, text string) error {
	if s.cfg.Record.Role != RoleMember {
		return errors.New("group: SubmitToOwner: only member-role sessions submit via relay")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	msgID := newRelayMsgID()
	rm := RelayMessage{SenderPK: s.cfg.MyPK, Text: text, TS: time.Now().UTC(), MsgID: msgID}
	body, err := json.Marshal(rm)
	if err != nil {
		return fmt.Errorf("group: SubmitToOwner: marshal: %w", err)
	}
	relayPort := s.cfg.Record.Port + relayPortOffset
	skyAddr := appnet.Addr{
		Net:    appnet.TypeSkynet,
		PubKey: s.cfg.Record.OwnerPK,
		Port:   routing.Port(relayPort),
	}
	if conn, dialErr := dialSkynetRelay(ctx, skyAddr); dialErr == nil {
		ackErr := writeAndReadAck(conn, body, msgID)
		_ = conn.Close() //nolint:errcheck
		if ackErr == nil {
			return nil
		}
		// A noAck error is a non-fatal: the bytes were written but
		// the owner didn't reply (pre-#group-relay-ack binary, or
		// owner's publishAs is slow). Surface as a soft error the
		// caller can choose to treat as "delivered but unconfirmed."
		if errors.Is(ackErr, ErrRelayNoAck) {
			return ackErr
		}
		s.log.WithError(ackErr).Debug("group: SubmitToOwner: skynet write/ack failed, falling back to dmsg")
	} else {
		s.log.WithError(dialErr).Debug("group: SubmitToOwner: skynet dial failed, falling back to dmsg")
	}
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	dmsgAddr := dmsg.Addr{PK: s.cfg.Record.OwnerPK, Port: relayPort}
	stream, err := s.cfg.DmsgC.DialStream(dialCtx, dmsgAddr)
	if err != nil {
		return fmt.Errorf("group: SubmitToOwner: dial owner relay (dmsg): %w", err)
	}
	defer stream.Close() //nolint:errcheck
	if err := writeAndReadAck(stream, body, msgID); err != nil {
		if errors.Is(err, ErrRelayNoAck) {
			return err
		}
		return fmt.Errorf("group: SubmitToOwner: write (dmsg): %w", err)
	}
	return nil
}

// ErrRelayNoAck signals that the relay message bytes were written
// successfully but no RelayAck arrived within relayAckReadTimeout
// (or the owner is on a pre-ack binary). Caller can treat this as
// "delivered to the owner's wire but not confirmed-published" —
// e.g. surface to the operator as "sent (unconfirmed)" rather than
// either a clean success or a hard failure.
var ErrRelayNoAck = errors.New("group: relay: no ack within timeout (owner pre-ack or slow publish)")

// writeAndReadAck writes the framed body to conn and, if msgID is
// non-empty, attempts to read a framed RelayAck within
// relayAckReadTimeout. Returns nil on confirmed ack, ErrRelayNoAck
// on missing/timeout-out ack but successful write, or a wrapped
// error on write failure.
func writeAndReadAck(c net.Conn, body []byte, msgID string) error {
	if err := writeFrame(c, body); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if msgID == "" {
		return nil
	}
	// Read-deadline scope: deadline applies only to this read, not
	// to any future use of the conn. Caller closes the conn anyway,
	// so we don't bother resetting the deadline on exit.
	if dl, ok := c.(interface{ SetReadDeadline(time.Time) error }); ok {
		_ = dl.SetReadDeadline(time.Now().Add(relayAckReadTimeout)) //nolint:errcheck
	}
	payload, err := readFrame(c)
	if err != nil {
		return ErrRelayNoAck
	}
	var ack RelayAck
	if err := json.Unmarshal(payload, &ack); err != nil {
		return ErrRelayNoAck
	}
	if !ack.Ack || ack.MsgID != msgID {
		return ErrRelayNoAck
	}
	return nil
}

// newRelayMsgID returns a hex-encoded 64-bit random identifier for
// pairing a RelayMessage with its RelayAck. Cheap to generate, big
// enough to make collisions across a session's relay traffic
// astronomically unlikely.
func newRelayMsgID() string {
	var buf [8]byte
	_, _ = cryptoRandRead(buf[:]) //nolint:errcheck // best-effort; tail of buf still random-enough for an id
	return fmt.Sprintf("%016x", binary.BigEndian.Uint64(buf[:]))
}

// dialSkynetRelay attempts the skynet dial with a 15s deadline.
// Returns an error if the networker isn't registered, or the dial
// itself fails. Caller is expected to fall back to dmsg.
func dialSkynetRelay(ctx context.Context, addr appnet.Addr) (net.Conn, error) {
	if _, err := appnet.ResolveNetworker(appnet.TypeSkynet); err != nil {
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return appnet.DialContext(dialCtx, addr)
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

// ReplayHistoryThrough pumps the last `cap` messages from every
// publisher/subscriber tree this session can reach through the
// given handler. Empty or uninitialized sessions are no-ops.
//
// D1 sources walked:
//   - the local publisher (s.pub) — owner's own sends + heartbeats +
//     any owner-relay re-publishes; member's own direct sends
//   - the legacy owner-feed subscriber (s.sub) — present on member
//     sessions, mirrors the owner's feed
//   - every D1 per-PK peer subscriber (s.peerSubs) — other members'
//     direct sends published to their own feeds
//
// Sorting is by Message.TS (decoded), not by path, because the D1
// path layout msgs/<senderHex>/<ts-nano>/<seq> would sort by sender
// first if compared lexically — yielding per-sender chronological
// groups but not a global chronological order.
//
// Best-effort: per-leaf decode / decrypt failures are silently
// skipped (consistent with onUpdate's policy — a torn leaf
// shouldn't block the rest of replay).
func (s *Session) ReplayHistoryThrough(handler MessageHandler, cap int) {
	if handler == nil || cap <= 0 {
		return
	}
	type leaf struct {
		path  string
		value []byte
	}
	var leaves []leaf
	walk := func(path string, value []byte) bool {
		// Defensive copy: tree-store Walk may reuse the value
		// slice between callback invocations.
		v := make([]byte, len(value))
		copy(v, value)
		leaves = append(leaves, leaf{path: path, value: v})
		return true
	}
	if s.pub != nil {
		s.pub.Walk(MessagePathPrefix, walk)
	}
	if s.sub != nil {
		s.sub.Walk(MessagePathPrefix, walk)
	}
	s.peerSubsMu.RLock()
	peerSubs := make([]*treestore.Subscriber, 0, len(s.peerSubs))
	for _, ps := range s.peerSubs {
		peerSubs = append(peerSubs, ps)
	}
	s.peerSubsMu.RUnlock()
	for _, ps := range peerSubs {
		ps.Walk(MessagePathPrefix, walk)
	}
	if len(leaves) == 0 {
		return
	}
	// Decode every leaf upfront so we can sort by Message.TS and
	// drop undecodable leaves before the cap window is applied
	// (otherwise a window full of garbage leaves would starve the
	// handler of real messages within the same cap budget).
	type decoded struct {
		path string
		msg  Message
	}
	out := make([]decoded, 0, len(leaves))
	for _, l := range leaves {
		var msg Message
		if err := json.Unmarshal(l.value, &msg); err != nil {
			s.log.WithError(err).WithField("path", l.path).
				Debug("group: replay: undecodable leaf, skipping")
			continue
		}
		if s.cfg.Record.Mode == ModePrivate && len(msg.Ciphertext) > 0 {
			plain, dErr := Decrypt(s.cfg.Record.AESKey, msg.Ciphertext, msg.Nonce)
			if dErr != nil {
				s.log.WithError(dErr).WithField("path", l.path).
					Debug("group: replay: decrypt failed, skipping")
				continue
			}
			msg.Text = string(plain)
			msg.Ciphertext = nil
			msg.Nonce = nil
		}
		out = append(out, decoded{path: l.path, msg: msg})
	}
	if len(out) == 0 {
		return
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].msg.TS.Before(out[j].msg.TS)
	})
	start := 0
	if len(out) > cap {
		start = len(out) - cap
	}
	for _, d := range out[start:] {
		handler(s.cfg.Record.ID, d.msg.SenderPK, d.msg)
	}
}

// onUpdate is the subscriber callback for member-role sessions.
// Decodes each leaf, decrypts if private, and dispatches via the
// registered handler. Tolerates undecodable leaves (e.g. future
// metadata under a different prefix, or a tampered ciphertext) by
// silently dropping them with a debug log.
//
// Bumps lastInboundNs once per non-empty events batch — the
// callback firing is itself proof the subscriber is attached and
// pulling, regardless of whether the events decode, decrypt, or
// turn out to be heartbeats. This is the unified liveness signal
// that replaces the four-flag tangle (subAlive bool +
// lastHeartbeatNs + LastMessageAt + Status's health semantics).
func (s *Session) onUpdate(events []treestore.UpdateEvent) {
	if len(events) > 0 {
		s.lastInboundNs.Store(time.Now().UnixNano())
	}
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
		// Heartbeat probe from the owner: lastInboundNs is already
		// bumped above (event count > 0). Don't bubble heartbeats
		// up to the user handler — they're wire-level liveness
		// probes, not chat content.
		if IsHeartbeat(msg) {
			continue
		}
		h(s.cfg.Record.ID, msg.SenderPK, msg)
	}
}
