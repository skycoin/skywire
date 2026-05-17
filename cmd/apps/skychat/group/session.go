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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

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

// MirrorPathPrefix WAS the prefix admins used to re-publish observed
// peer leaves as a redundancy strategy for offline senders (#2584).
// The publish-on-receive loop + dual-prefix peerSubs subscription
// it required doubled the per-peer subscription count in any
// group with admins. During the 2026-05-16/17 reliability runs the
// extra subscriptions accounted for the bulk of the failure-mode
// surface: each subscriber's state machine multiplied the chances
// some path was wedged at any moment.
//
// Removed in this PR. The offline-sender use case the mirroring
// was meant to address is now covered by per-peer history replay
// on reconnect (#2659) — a re-attaching subscriber walks the
// publisher's CXO tree from SinceTimestampNs and gets every leaf
// the publisher still has, no admin redundancy needed.
//
// The const stays exported so legacy on-disk data referencing the
// prefix parses cleanly. Current peerSubs no longer subscribe to
// it and no one publishes under it; leaves left on legacy
// publishers go unread until publisher restart clears them.
//
// Deprecated: not used by current code; removal scheduled after
// legacy-publisher TTL elapses.
const MirrorPathPrefix = "mirror"

// RosterPathPrefix is the prefix used for signed roster-mutation
// envelopes within a group feed (cross-visor admin/roster gossip —
// RFC at docs/skychat_group_gossip_rfc.md, types in gossip.go).
// Every roster mutation issued by an admin lands at
// "<RosterPathPrefix>/<seq>" on that admin's own publisher feed;
// other members' subscribers pick it up and apply through the
// reconciler (PR-3 of the gossip impl).
const RosterPathPrefix = "roster"

// AdminPathPrefix is the parallel prefix for signed admin-mutation
// envelopes (promote / demote). Same per-feed seq scheme as
// RosterPathPrefix.
const AdminPathPrefix = "admin"

// dedupKey reduces a leaf path to its content-identity suffix —
// the canonical "<senderPK>/<ts>/<seq>" tail. Pre-#2584-removal
// this also collapsed mirror/ copies into the same key so
// admin-mirrored duplicates of a primary leaf delivered once;
// with admin mirroring gone there's no cross-prefix duplicate to
// collapse, but the function stays as defense-in-depth for any
// future cross-source duplicate path (e.g. a leaf observed via
// both a direct peerSub and a legacy s.sub during the D4
// transition window) and to keep the recentSet hot for the inbox-
// replay path.
//
// Returns "" for paths NOT under MessagePathPrefix — non-message
// subtrees (heartbeats, roster/admin gossip envelopes) shouldn't
// participate in the inbox dedup window.
func dedupKey(path string) string {
	prefix := MessagePathPrefix + "/"
	if strings.HasPrefix(path, prefix) {
		return path[len(prefix):]
	}
	return ""
}

// inboxDedupCap bounds the per-session recent-message-set used to
// suppress duplicate deliveries when the same source message
// arrives via multiple paths (the original sender's msgs/<...> AND
// one or more admin mirrors of the same leaf). Sized for typical
// chat-app workloads: ~10msg/min from each of 30 members for ~30
// minutes fits comfortably; bursts past it lose dedup only for the
// oldest entries, which are vanishingly unlikely to re-appear at
// that point. The cap is per-Session, so a visor with N groups uses
// N * inboxDedupCap entries in the worst case — still trivially
// small (~64KB at N=30, 32-byte keys).
const inboxDedupCap = 1024

// recentSet is a fixed-capacity FIFO membership cache. Add returns
// true the first time a key is observed, false on every subsequent
// observation while the key is still in the window. Eviction is
// strict insertion-order — adequate for the dedup use case because
// duplicates of a given source leaf arrive within seconds of each
// other (N admins all observe the same source on their peerSubs at
// roughly the same time and re-publish under MirrorPathPrefix),
// not minutes apart, so the FIFO window comfortably covers the
// duplicate-arrival burst.
//
// Not LRU: an LRU would re-promote a key on every duplicate Add,
// keeping hot but useless dedup entries alive. FIFO bounds the
// memory and naturally ages out entries even when the same source
// leaf keeps re-appearing (which it shouldn't, but we don't want
// pathological inputs to defeat the bound).
//
// Concurrency: protected by an internal mutex because peerSubs each
// run their own update goroutine and may call Add concurrently for
// the same Session.
type recentSet struct {
	mu    sync.Mutex
	cap   int
	order []string
	idx   map[string]struct{}
}

func newRecentSet(capacity int) *recentSet {
	if capacity <= 0 {
		capacity = inboxDedupCap
	}
	return &recentSet{cap: capacity, idx: make(map[string]struct{}, capacity)}
}

// Add inserts key and returns true if it was not already present.
// Returns false (without re-inserting) when key is a duplicate
// within the current window. Empty keys are always treated as new
// (returns true) — callers should skip Add entirely for paths that
// shouldn't dedup (non-message subtrees).
func (r *recentSet) Add(key string) bool {
	if key == "" {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.idx[key]; ok {
		return false
	}
	r.idx[key] = struct{}{}
	r.order = append(r.order, key)
	if len(r.order) > r.cap {
		evict := r.order[0]
		r.order = r.order[1:]
		delete(r.idx, evict)
	}
	return true
}

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

	// dedup tracks the recent set of (sender, ts, seq) suffixes
	// observed across every peerSub. Used by onUpdate +
	// ReplayHistoryThrough to suppress duplicate deliveries when the
	// same source leaf arrives via the primary msgs/<...> path AND
	// one or more admin mirror/<...> copies. Capacity is bounded
	// (inboxDedupCap) so a pathological input can't grow the map.
	// Initialized to a non-nil empty set at Open; never replaced.
	dedup *recentSet

	// peerLastInboundNs is the per-peerSub liveness signal. Each entry
	// is bumped (to time.Now().UnixNano()) on every observable inbound
	// event from THAT specific peer's onUpdate fan-out, including a
	// successful per-peer Connect (which is positive evidence the
	// peerSub is attached).
	//
	// Why per-peer in addition to the session-level lastInboundNs:
	// detectStaleAndReconnect uses peer-granular liveness to decide
	// which peerSubs to reconnect. The session-level signal aggregates
	// across all peers, so one chatty peer keeps the session "fresh"
	// indefinitely and masks a silent peer's failed peerSub from the
	// reconnect machinery. The per-peer map closes that gap — staleness
	// is detected and a reconnect is kicked per peerSub, independent of
	// how active the others are.
	//
	// Key set is populated alongside Session.peerSubs: every time a
	// peerSub is created (newSession/openMember/openOwner/SetAllowlist),
	// a parallel atomic.Int64 is added here. Removed in SetAllowlist
	// when peerSubs[pk] is deleted. Guarded by Session.peerSubsMu (the
	// same lock that protects peerSubs) — readers grab a snapshot under
	// RLock and access the *atomic.Int64 entries lock-free.
	peerLastInboundNs map[cipher.PubKey]*atomic.Int64

	// peerUpdateCount is the per-peerSub count of OnUpdate callbacks
	// observed with events>0 (matches the seam that bumps
	// peerLastInboundNs). Parallel to peerLastInboundNs in lifetime
	// and locking: added in SetAllowlist when a peerSub is created,
	// removed when the peerSub is deleted, guarded by peerSubsMu.
	//
	// Operator-facing surface exposed via Manager.PeerUpdateCount and
	// downstream in pkg/visor.GroupInfo.PeerUpdateCount. The delta
	// against pkg/visor.GroupInfo.DeliverCount localizes drops: an
	// individual peer with UpdateCount near zero while DeliverCount
	// advances is the peerSub-not-firing case (a CXO subscriber
	// handshake / Conn-level failure); UpdateCount sum approaching
	// DeliverCount means all peer-subscribers fire but later layers
	// (sub channel, gRPC stream) drop.
	peerUpdateCount map[cipher.PubKey]*atomic.Uint64

	// subLastInboundNs is the per-s.sub liveness signal — the legacy
	// owner-feed subscriber's counterpart to peerLastInboundNs. Bumped
	// on every inbound UpdateEvent fanout via the legacy subscriber and
	// on every successful s.sub.Connect. Member-role only; stays zero
	// on owner sessions where s.sub is nil.
	//
	// Why separate from peerLastInboundNs: s.sub is the D1 single-
	// subscriber legacy field tracked outside the peerSubs map (its
	// merge into peerSubs is the deferred D4 cleanup). The per-peerSub
	// liveness machinery from #2606 only covers peerSubs entries, so
	// without this signal a wedged s.sub goes silently dead and
	// detectStaleAndReconnect has nothing to fire on. After a chat-app
	// or visor restart that doesn't re-run Open, the per-peerSub
	// reconnects fire for everything in peerSubs but s.sub stays in
	// whatever attached/dead state it landed in.
	subLastInboundNs atomic.Int64

	// rosterSeq and adminSeq are the per-feed monotonic sequence
	// counters appended to gossip-mutation leaf paths
	// (RosterPathPrefix/<seq>, AdminPathPrefix/<seq>). Initialized
	// to zero; first mutation publishes at seq=1. Atomic.Add()
	// returns the post-increment value, so each PublishRoster /
	// PublishAdmin call gets a fresh seq via a single CAS. The
	// counter is per-Session because it's per-publisher-feed; cross-
	// visor convergence happens in the reconciler via ParentSeq.
	//
	// Not persisted: on restart the counter resets to zero, which
	// is fine — the publisher rehydrates its tree (#2651) and the
	// next mutation lands at roster/00001 (or wherever the highest
	// existing seq + 1 is, once the rehydrate path observes it).
	// Subscribers dedup via the canonical-bytes signature, so a
	// post-restart seq collision is at worst a no-op replay.
	rosterSeq atomic.Uint64
	adminSeq  atomic.Uint64
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
		peerSubs:          make(map[cipher.PubKey]*treestore.Subscriber),
		peerLastInboundNs: make(map[cipher.PubKey]*atomic.Int64),
		peerUpdateCount:   make(map[cipher.PubKey]*atomic.Uint64),
		dedup:             newRecentSet(inboxDedupCap),
	}

	// Per-member subscribers: each non-owner-self member publishes
	// their own message feed at memberPK:Record.Port. Owner follows
	// every member's feed directly so the relay-hop becomes
	// vestigial. Subs are created here but not Connect'd; Connect
	// dials them all on the operator-controlled timing.
	//
	// Subscribed prefixes: msgs/ (the peer's own sends) AND mirror/
	// (admin-relayed copies of OTHER peers' sends, present when this
	// peer is itself an admin). Consuming both means an owner that's
	// offline doesn't take the group with it — any admin's mirror
	// of the offline member's leaves keeps the message reachable.
	// onUpdate's dedup set collapses the (primary + N mirror copies)
	// fan-in to a single delivery per source leaf.
	//
	// Admin-aggregator note (#2685 signed leaves): owners are
	// always admins by the IsAdmin contract, so they always sit on
	// the "subscribe to every member" side of the new topology.
	// The non-admin filtering applied in openMember does not apply
	// here — see openMember for the role-conditional rule, and
	// SetAdminRoster for the dynamic re-evaluation on PromoteAdmin
	// / DemoteAdmin.
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
		s.peerLastInboundNs[peerPK] = new(atomic.Int64)
		s.peerUpdateCount[peerPK] = new(atomic.Uint64)
		ps.OnUpdate(s.makePeerOnUpdate(peerPK))
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
		peerSubs:          make(map[cipher.PubKey]*treestore.Subscriber),
		peerLastInboundNs: make(map[cipher.PubKey]*atomic.Int64),
		peerUpdateCount:   make(map[cipher.PubKey]*atomic.Uint64),
		dedup:             newRecentSet(inboxDedupCap),
	}

	// Admin-aggregator topology (post-#2685 signed leaves):
	//
	//   - If THIS visor is an admin (owner OR in Record.Admins), it is
	//     an input-pipe + republisher: subscribe to every OTHER
	//     member's feed so we observe every signed leaf and can
	//     republish it onto our own canonical feed for the
	//     non-admin members that follow us.
	//
	//   - If THIS visor is NOT an admin, subscribe ONLY to admin
	//     members' canonical feeds. The admins republish every
	//     signed leaf verbatim, so following them is sufficient to
	//     see all messages — without paying the quadratic N×N
	//     subscription cost. Verification is unchanged: Session
	//     onUpdate runs the leaf-signature check on every leaf
	//     regardless of which feed it came from, so an admin
	//     republishing a tampered leaf would be rejected with the
	//     same error path as a direct peer leaf.
	//
	// The legacy `sub` field above stays wired for the owner-relay
	// fallback path; it subscribes to OwnerPK specifically (the
	// founder is always an admin by IsAdmin contract), so non-admin
	// members are always covered for the owner's republished view
	// even before peerSubs to other admins finish Connect.
	// desiredPeerSubsForRole encodes the admin-aggregator rule;
	// see its docstring. Iterating the pure-function output keeps
	// the openMember vs SetAdminRoster behavior in lockstep.
	desired := desiredPeerSubsForRole(cfg.Record, cfg.MyPK)
	for peerPK := range desired {
		ps, err := treestore.NewSubscriberOnNode(pub.Node(), peerPK, treestore.SubConfig{Logger: log})
		if err != nil {
			log.WithError(err).WithField("peer", peerPK.String()).
				Warn("group: member peer-sub create failed; will still receive owner-relayed messages")
			continue
		}
		// Subscribe to both primary (peer's own sends) AND mirror
		// (admin-relayed copies of other members' sends) — see the
		// openOwner equivalent for the full rationale.
		ps.SetPrefixes([]string{MessagePathPrefix})
		s.peerUpdateCount[peerPK] = new(atomic.Uint64)
		s.peerLastInboundNs[peerPK] = new(atomic.Int64)
		ps.OnUpdate(s.makePeerOnUpdate(peerPK))
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
	sub.OnUpdate(s.makeLegacySubOnUpdate())
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
func (s *Session) Connect(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Owner-feed subscriber (legacy path) — only set on member sessions.
	// Failure here is fatal for the member's read path TODAY since
	// peerSubs may not be enough until every visor migrates. Once D4
	// retires the relay/owner-feed flow we'll relax this to "any sub
	// succeeded" semantics.
	//
	// pkg/cxo/treestore.Subscriber.Connect is NOT context-aware
	// internally (it eventually calls dmsg.Client.ConnectPK which
	// blocks on network IO without a ctx hook). To honor our outer
	// deadline we wrap each Connect call in a goroutine and select
	// on ctx — the goroutine's underlying dial may still run to
	// completion in the background, but it won't block the outer
	// caller past ctx's deadline. Post-T2a (ctx-through-ConnectPK)
	// the underlying dmsg dial also honors ctx, so a canceled
	// Connect actually aborts the dial rather than leaking a
	// goroutine that runs to completion before discarding its result.
	if s.sub != nil {
		done := make(chan error, 1)
		go func() { done <- s.sub.Connect(ctx, s.cfg.Record.OwnerPK) }()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-done:
			if err != nil {
				return fmt.Errorf("group: Connect: %w", err)
			}
		}
		// Bump per-s.sub liveness: a successful Connect is positive
		// evidence the legacy owner-feed subscriber is attached, even
		// before the first inbound leaf arrives. Lets the staleness
		// window for s.sub start ticking from here, mirroring the
		// per-peerSub Connect-bump behavior below.
		s.subLastInboundNs.Store(time.Now().UnixNano())
	}
	// D1 per-PK peer subscribers. Best-effort: if a single peer's
	// publisher isn't reachable yet (e.g. they restarted), that one
	// connect fails silently and the reconnect loop retries. Don't
	// fail the whole Connect — partial connectivity is better than
	// none, and at least the owner-feed legacy path is up.
	//
	// We track the count of successful peer Connects so the
	// lastInboundNs bump below only fires when there's actual
	// evidence at least one subscriber is now attached. Pre-fix
	// behavior was to bump unconditionally — that masked the case
	// where every per-peer Connect failed, because the bump made
	// the session look fresh for the next subscriberStaleThreshold
	// (100s) and the reconnect loop skipped it. Result: silent
	// indefinite failure to recover, since tryReconnect treats a
	// nil-error Connect as "subscribe restored" and clears the
	// failure state, so the backoff schedule never engages either.
	s.peerSubsMu.RLock()
	peers := make(map[cipher.PubKey]*treestore.Subscriber, len(s.peerSubs))
	for k, v := range s.peerSubs {
		peers[k] = v
	}
	s.peerSubsMu.RUnlock()
	peerSuccessCount := 0
	now := time.Now().UnixNano()
	for pk, ps := range peers {
		// Bail out between peers if the outer deadline expired —
		// don't pile up more orphan goroutines than necessary.
		if err := ctx.Err(); err != nil {
			break
		}
		done := make(chan error, 1)
		pkLocal, psLocal := pk, ps
		go func() { done <- psLocal.Connect(ctx, pkLocal) }()
		select {
		case <-ctx.Done():
			// Outer deadline fired mid-Connect. The goroutine will
			// eventually return; the buffered channel keeps it from
			// leaking memory. We stop iterating remaining peers so
			// the reconnect tick honors the deadline.
			return ctx.Err()
		case err := <-done:
			if err != nil {
				s.log.WithError(err).WithField("peer", pk.String()).
					Debug("group: Connect: peer-sub Connect failed; will retry on next reconnect tick")
				continue
			}
		}
		peerSuccessCount++
		// Bump per-peer lastInbound on every successful per-peer
		// Connect. detectStaleAndReconnect uses this peer-granular
		// signal so a silent peer's failed peerSub gets retried even
		// when other peers keep the session-wide lastInbound fresh.
		s.peerSubsMu.RLock()
		a := s.peerLastInboundNs[pk]
		s.peerSubsMu.RUnlock()
		if a != nil {
			a.Store(now)
		}
	}
	// Only declare the session "fresh" (bump lastInboundNs) if SOME
	// subscriber side actually connected. Member-role sessions also
	// have the legacy s.sub path, which was already required to
	// succeed above; reaching this point means s.sub is healthy if
	// it exists, so member sessions bump unconditionally. Owner-role
	// sessions have only peerSubs — for them, "fresh" requires at
	// least one peer attached, otherwise the next reconnect tick
	// should re-enter the reconnect path instead of being masked by
	// a phantom freshness window.
	if s.sub != nil || peerSuccessCount > 0 {
		s.lastInboundNs.Store(time.Now().UnixNano())
		return nil
	}
	// Owner-role with zero peer connections succeeded: surface the
	// failure so tryReconnect's failure-count machinery engages.
	// The exponential backoff (10 → 30 fail-count thresholds) was
	// designed to age out permanently-unreachable groups without
	// hammering the network; pre-fix it never engaged on owner-role
	// because Connect always returned nil.
	return fmt.Errorf("group: Connect: 0/%d peer-subs reachable", len(peers))
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

// LegacySubLastInbound returns the time the legacy s.sub owner-feed
// subscriber most recently observed an inbound CXO event (or a
// successful Connect). Zero time on owner sessions (s.sub is nil),
// freshly-opened member sessions that haven't yet had their first
// Connect succeed, and torn-down sessions.
//
// Used by Manager.detectStaleAndReconnect to spot a wedged owner-feed
// subscriber independently of the per-peerSub liveness map (#2606).
// s.sub lives outside that map by design (deferred D4 cleanup), so it
// needs its own staleness signal.
func (s *Session) LegacySubLastInbound() time.Time {
	if s.sub == nil {
		return time.Time{}
	}
	ns := s.subLastInboundNs.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns).UTC()
}

// HasLegacySub reports whether this session carries an attached
// legacy s.sub owner-feed subscriber. True only for member-role
// sessions where openMember successfully created the legacy
// subscriber; false on owner sessions and on member sessions whose
// s.sub was torn down by Close.
//
// Used by detectStaleAndReconnect to skip the legacy-sub stale check
// on sessions that have no legacy subscriber to begin with.
func (s *Session) HasLegacySub() bool {
	return s.sub != nil
}

// PeerLastInbound returns the per-peerSub last-inbound timestamp for
// the given peer PK. Zero time = no inbound ever observed from that
// peer (peerSub is either never-connected or silent since startup).
// Returns zero time if pk isn't a known peer of this session.
//
// Used by Manager.detectStaleAndReconnect to detect a silent peer's
// failed peerSub even when other peers in the session are chatty
// enough to keep the session-level LastInbound() fresh. Pre-this-
// method the reconnect machinery only saw the session aggregate and
// silently-failing peerSubs stayed in that state indefinitely.
func (s *Session) PeerLastInbound(pk cipher.PubKey) time.Time {
	s.peerSubsMu.RLock()
	a, ok := s.peerLastInboundNs[pk]
	s.peerSubsMu.RUnlock()
	if !ok {
		return time.Time{}
	}
	ns := a.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns).UTC()
}

// PeerPKs returns the set of peer PKs this session has a peerSub for
// (i.e., every group member other than self that this visor follows).
// The slice is a snapshot — safe to iterate after the function returns
// without holding the session's internal lock. Sorted for determinism
// so callers iterating the slice produce stable behavior (mostly
// helpful in tests and structured-log output).
func (s *Session) PeerPKs() []cipher.PubKey {
	s.peerSubsMu.RLock()
	defer s.peerSubsMu.RUnlock()
	out := make([]cipher.PubKey, 0, len(s.peerSubs))
	for k := range s.peerSubs {
		out = append(out, k)
	}
	// Sort by hex form for determinism; cipher.PubKey doesn't have a
	// natural ordering otherwise. The ~5 byte allocations per call are
	// trivial vs. the reconnect machinery's work.
	sort.Slice(out, func(i, j int) bool { return out[i].Hex() < out[j].Hex() })
	return out
}

// ReconnectPeer re-runs Connect on a single peerSub. Counterpart to
// Session.Connect which retries every peer in one shot. The
// detectStaleAndReconnect loop uses this for per-peer recovery so a
// stale silent peer doesn't trigger redundant Connect retries on
// already-healthy peers (and the per-peer success/failure attribution
// keeps the manager's backoff state cleaner).
//
// On success: bumps the peer's per-peer lastInbound timestamp.
// Connect is positive evidence the peerSub is attached, so the
// liveness window for THIS peer restarts here.
//
// On failure: returns the underlying Subscriber.Connect error
// verbatim; caller decides whether to log, retry, or apply backoff.
// peerLastInboundNs is NOT bumped, so the staleness signal stays
// honest.
//
// No-op + nil error if pk isn't a known peer of this session.
//
// Honors the provided context: if ctx fires before the underlying
// Subscriber.Connect returns, ReconnectPeer returns ctx.Err()
// promptly. The Subscriber.Connect goroutine continues to run in
// the background (since treestore.Subscriber isn't yet ctx-aware
// internally — see Session.Connect's comment for the longer-term
// fix) but the buffered done channel prevents goroutine leakage:
// the goroutine completes and exits cleanly when the network IO
// finishes.
func (s *Session) ReconnectPeer(ctx context.Context, pk cipher.PubKey) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.peerSubsMu.RLock()
	ps := s.peerSubs[pk]
	a := s.peerLastInboundNs[pk]
	s.peerSubsMu.RUnlock()
	if ps == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- ps.Connect(ctx, pk) }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		if err != nil {
			return err
		}
	}
	if a != nil {
		a.Store(time.Now().UnixNano())
	}
	return nil
}

// ReconnectLegacySub re-runs Connect on the legacy s.sub owner-feed
// subscriber. Mirrors ReconnectPeer's shape but targets the s.sub
// field rather than a peerSubs entry. No-op + nil error on owner-
// role sessions and on sessions where s.sub is nil.
//
// On success: bumps both subLastInboundNs and the session-wide
// lastInboundNs. Connect itself is positive evidence the legacy
// subscriber is attached even before the first inbound leaf arrives,
// and on member sessions s.sub being healthy is sufficient to
// declare the session as a whole healthy — same rationale as
// Session.Connect's bump-on-success path. Skipping the session-wide
// bump would leave subscriber_alive=false after a successful
// reconnect until the next heartbeat or chat message arrives, which
// is the symptom this fix is meant to remove.
//
// On failure: returns Subscriber.Connect's error verbatim;
// neither timestamp is bumped so the next detectStaleAndReconnect
// tick still sees the session as stale and tries again.
//
// Honors the provided context: ctx cancellation returns ctx.Err()
// promptly, with the same goroutine-leak protection used by Connect
// + ReconnectPeer (buffered done channel, treestore.Subscriber is
// not yet ctx-aware internally).
func (s *Session) ReconnectLegacySub(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.sub == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- s.sub.Connect(ctx, s.cfg.Record.OwnerPK) }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		if err != nil {
			return err
		}
	}
	now := time.Now().UnixNano()
	s.subLastInboundNs.Store(now)
	s.lastInboundNs.Store(now)
	return nil
}

// makePeerOnUpdate wraps Session.onUpdate with per-peer lastInbound
// bookkeeping. Each peerSub gets its own wrapper at registration time
// (in newSession/openOwner/openMember/SetAllowlist) so onUpdate-firing
// preserves per-peer attribution that the treestore.UpdateCallback
// signature otherwise discards.
//
// The peer's atomic.Int64 is captured by closure; the wrapper is
// safe-for-concurrent-use because atomic.Int64.Store handles
// concurrency itself.
func (s *Session) makePeerOnUpdate(pk cipher.PubKey) treestore.UpdateCallback {
	return func(events []treestore.UpdateEvent) {
		if len(events) > 0 {
			s.peerSubsMu.RLock()
			a := s.peerLastInboundNs[pk]
			c := s.peerUpdateCount[pk]
			s.peerSubsMu.RUnlock()
			if a != nil {
				a.Store(time.Now().UnixNano())
			}
			if c != nil {
				c.Add(1)
			}
		}
		s.onUpdate(events)
	}
}

// PeerUpdateCounts returns a snapshot of the per-peer OnUpdate-callback
// count map. Counts are monotonic across the session's lifetime;
// readers may compare deltas across two snapshots to measure inbound
// activity per peer over an interval.
//
// Empty map for owner-role sessions (no peerSubs). The returned map
// is a copy — the caller can read it without holding peerSubsMu.
func (s *Session) PeerUpdateCounts() map[cipher.PubKey]uint64 {
	s.peerSubsMu.RLock()
	defer s.peerSubsMu.RUnlock()
	if len(s.peerUpdateCount) == 0 {
		return nil
	}
	out := make(map[cipher.PubKey]uint64, len(s.peerUpdateCount))
	for pk, c := range s.peerUpdateCount {
		if c != nil {
			out[pk] = c.Load()
		}
	}
	return out
}

// makeLegacySubOnUpdate wraps Session.onUpdate with s.sub-specific
// lastInbound bookkeeping. Mirrors makePeerOnUpdate's shape for the
// legacy D1 owner-feed subscriber that lives outside the peerSubs map.
//
// Registered in place of the raw s.onUpdate on s.sub in openMember so
// the per-s.sub staleness signal (subLastInboundNs) is bumped exactly
// when an inbound CXO event arrives via the legacy path. Without this
// wrapper, detectStaleAndReconnect has no way to tell s.sub apart from
// the peerSubs and a wedged s.sub stays dead until the operator does
// leave + rejoin.
func (s *Session) makeLegacySubOnUpdate() treestore.UpdateCallback {
	return func(events []treestore.UpdateEvent) {
		if len(events) > 0 {
			s.subLastInboundNs.Store(time.Now().UnixNano())
		}
		s.onUpdate(events)
	}
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
// Returns the list of peers whose peerSubs were torn down — the
// caller (Manager) is responsible for clearing any state keyed by
// (group ID, peer PK), e.g. the per-peer reconnect backoff map.
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
func (s *Session) SetAllowlist(members []cipher.PubKey) ([]cipher.PubKey, error) {
	if s.pub == nil || s.cfg.Record.Role != RoleOwner {
		return nil, errors.New("group: SetAllowlist: only owner-role sessions have an allowlist")
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
	// evicted tracks peers whose peerSubs we just tore down; returned
	// to the caller so Manager state keyed by (id, peerPK) — the
	// per-peer reconnect backoff map in particular — can be cleaned
	// up alongside the session-side eviction. Without this, evicted
	// peers' backoff state could outlive its peerSub: harmless if
	// the peer is never re-added (the entry just consumes a few bytes
	// until session close), confusing if the same peer is later
	// re-added because the stale backoff window suppresses reconnect
	// attempts to a fresh peerSub.
	var evicted []cipher.PubKey
	s.peerSubsMu.Lock()
	// Drop removed peers — close their subs while still holding the
	// lock so a racing Connect doesn't pick up a half-torn sub.
	for pk, ps := range s.peerSubs {
		if _, keep := desired[pk]; !keep {
			_ = ps.Close() //nolint:errcheck,gosec
			delete(s.peerSubs, pk)
			delete(s.peerLastInboundNs, pk)
			delete(s.peerUpdateCount, pk)
			evicted = append(evicted, pk)
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
		s.peerLastInboundNs[pk] = new(atomic.Int64)
		s.peerUpdateCount[pk] = new(atomic.Uint64)
		ps.OnUpdate(s.makePeerOnUpdate(pk))
		// Best-effort connect — same semantics as Connect's per-peer
		// retry: a peer that's offline now will be picked up on the
		// next reconnect tick once the publisher comes back. Bound
		// the dial with a short timeout so a single unreachable peer
		// in the allowlist doesn't block roster expansion. The retry
		// loop will pick it up on a later tick with its own deadline.
		dctx, dcancel := context.WithTimeout(context.Background(), reconnectAttemptTimeout)
		err = ps.Connect(dctx, pk)
		dcancel()
		if err != nil {
			s.log.WithError(err).WithField("peer", pk.String()).
				Debug("group: SetAllowlist: peer-sub Connect failed; will retry on next reconnect tick")
		} else if a := s.peerLastInboundNs[pk]; a != nil {
			a.Store(time.Now().UnixNano())
		}
		s.peerSubs[pk] = ps
	}
	s.peerSubsMu.Unlock()
	return evicted, nil
}

// desiredPeerSubsForRole computes the set of peer PKs this visor
// should hold per-peer CXO subscriptions for, under the admin-
// aggregator topology (#2685):
//
//	desired = { pk in r.Members : pk != myPK AND (r.IsAdmin(myPK) OR r.IsAdmin(pk)) }
//
// Admins follow every member (input pipe + republish). Non-admins
// follow only admins (the admin canonical feed receives every
// signed leaf verbatim). The legacy s.sub subscription to OwnerPK
// is independent of this set; non-admins always have the owner
// covered via legacy s.sub plus (transitively) via owner-as-admin
// membership in this set.
//
// Pure function: no Session state, no CXO. Used at openMember and
// SetAdminRoster so the rule has one source of truth.
func desiredPeerSubsForRole(r Record, myPK cipher.PubKey) map[cipher.PubKey]struct{} {
	iAmAdmin := r.IsAdmin(myPK)
	out := make(map[cipher.PubKey]struct{}, len(r.Members))
	for _, pk := range r.Members {
		if pk == myPK {
			continue
		}
		if !iAmAdmin && !r.IsAdmin(pk) {
			continue
		}
		out[pk] = struct{}{}
	}
	return out
}

// SetAdminRoster re-evaluates this session's peerSubs against a new
// admin set under the admin-aggregator topology (#2685). Idempotent
// add/remove of per-peer subscriptions to match the rule:
//
//	desired_set = { pk in Members : pk != MyPK AND (iAmAdmin OR pk in Admins) }
//
// Hooks Manager.PromoteAdmin / Manager.DemoteAdmin: when a peer's
// admin status flips, the live session's input topology must follow
// or the peer's leaves stop reaching non-admin members. Mirrors
// SetAllowlist's structure (compute desired, drop removed, add new)
// and returns the evicted peers so the caller can clean
// (groupID, peerPK)-keyed state (per-peer reconnect backoff, etc.).
//
// admins is the new admin set; cfg.Record.Members is read live so the
// member-set side of the desired computation reflects whatever the
// most recent SetAllowlist landed. Founder admin status is implicit
// — the IsAdmin check uses the union of the founder PK and Admins.
//
// Owner-role sessions: this is a no-op because openOwner already
// subscribes to every non-self member (owners are always admins by
// IsAdmin contract, so the rule degenerates to "subscribe to all").
// Member-role admin status can change at runtime; that's the case
// this function handles.
//
// Errors: returns (evicted, error). Connect failures on newly-added
// peers are logged at Debug and the entry stays in peerSubs — the
// background reconnect loop picks them up on the next tick. Returns
// an error only if the session has no node to attach subs to (a
// torn-down session) or the receiver is nil; per-peer subscriber
// creation errors are logged + skipped, matching SetAllowlist's
// best-effort contract.
func (s *Session) SetAdminRoster(admins []cipher.PubKey) ([]cipher.PubKey, error) {
	if s == nil || s.pub == nil {
		return nil, errors.New("group: SetAdminRoster: nil session or no publisher attached")
	}
	// Cache the snapshot under cfg.Record.Admins so iAmAdmin /
	// IsAdmin checks elsewhere (publish path, openOwner doc, etc.)
	// see the new view too. Founder PK stays where it is on
	// cfg.Record.OwnerPK; IsAdmin's union semantics handle it.
	adminSnap := append([]cipher.PubKey(nil), admins...)
	s.cfg.Record.Admins = adminSnap

	// members snapshot read under membersMu where one exists (owner
	// role); on member-role sessions the field is nil and we fall
	// back to cfg.Record.Members which never mutates after Open.
	// Splice the live snapshot into a temporary Record for the pure
	// helper — we already mutated cfg.Record.Admins above so the
	// IsAdmin contract reads the new view.
	r := s.cfg.Record
	s.membersMu.RLock()
	if s.members != nil {
		r.Members = append([]cipher.PubKey(nil), s.members...)
	} else {
		r.Members = append([]cipher.PubKey(nil), s.cfg.Record.Members...)
	}
	s.membersMu.RUnlock()
	desired := desiredPeerSubsForRole(r, s.cfg.MyPK)

	var evicted []cipher.PubKey
	s.peerSubsMu.Lock()
	// Drop peers no longer desired — close their subs while still
	// holding the lock so a racing Connect doesn't pick up a
	// half-torn sub (same pattern as SetAllowlist).
	for pk, ps := range s.peerSubs {
		if _, keep := desired[pk]; !keep {
			_ = ps.Close() //nolint:errcheck,gosec
			delete(s.peerSubs, pk)
			delete(s.peerLastInboundNs, pk)
			delete(s.peerUpdateCount, pk)
			evicted = append(evicted, pk)
		}
	}
	// Add newly-desired peers.
	for pk := range desired {
		if _, exists := s.peerSubs[pk]; exists {
			continue
		}
		ps, err := treestore.NewSubscriberOnNode(s.pub.Node(), pk, treestore.SubConfig{Logger: s.log})
		if err != nil {
			s.log.WithError(err).WithField("peer", pk.String()).
				Warn("group: SetAdminRoster: peer-sub create failed; will retry on next admin-roster change")
			continue
		}
		ps.SetPrefixes([]string{MessagePathPrefix})
		s.peerLastInboundNs[pk] = new(atomic.Int64)
		s.peerUpdateCount[pk] = new(atomic.Uint64)
		ps.OnUpdate(s.makePeerOnUpdate(pk))
		// Best-effort connect — same per-peer retry contract as
		// SetAllowlist. A peer that's offline now will be picked up
		// on the next reconnect tick once the publisher comes back.
		dctx, dcancel := context.WithTimeout(context.Background(), reconnectAttemptTimeout)
		err = ps.Connect(dctx, pk)
		dcancel()
		if err != nil {
			s.log.WithError(err).WithField("peer", pk.String()).
				Debug("group: SetAdminRoster: peer-sub Connect failed; will retry on next reconnect tick")
		} else if a := s.peerLastInboundNs[pk]; a != nil {
			a.Store(time.Now().UnixNano())
		}
		s.peerSubs[pk] = ps
	}
	s.peerSubsMu.Unlock()
	return evicted, nil
}

// PublishRosterMutation signs op+peerPK as a RosterMutation envelope
// (per docs/skychat_group_gossip_rfc.md) and writes it to this
// session's publisher under RosterPathPrefix/<seq>. Subscribers on
// other visors observe the leaf via their normal OnUpdate path; the
// reconciler (step 3 of the gossip impl) decodes, verifies, and
// applies.
//
// Caller is responsible for ensuring this visor has roster authority
// to issue the mutation (Manager.AddMember already gates on
// r.IsAdmin(myPK) before calling this). The envelope's IssuerPK is
// always this session's MyPK; signature uses MySK.
//
// parentSeq is the highest roster-mutation seq THIS issuer has
// observed before issuing — used by the reconciler to void mutations
// from a since-demoted admin. Callers that don't track parent_seq
// can pass 0; the reconciler treats parent_seq=0 as "no causal
// constraint" and applies the mutation if signature + admin-set
// gate pass.
//
// Returns the seq the mutation was published at (>=1), or an error
// if signing or Put fails. The session must have a live publisher
// (s.pub != nil) — sessions opened in a degenerate state (no DMSG)
// have nil publishers and return an error here.
func (s *Session) PublishRosterMutation(op RosterOp, peerPK cipher.PubKey, parentSeq uint64) (uint64, error) {
	if s == nil || s.pub == nil {
		return 0, errors.New("group: PublishRosterMutation: no live publisher")
	}
	seq := s.rosterSeq.Add(1)
	m := RosterMutation{
		GroupID:   uuid.MustParse(s.cfg.Record.ID),
		Op:        op,
		PeerPK:    peerPK,
		ParentSeq: parentSeq,
		IssuedAt:  time.Now().UTC(),
	}
	if err := SignRoster(&m, s.cfg.MySK); err != nil {
		return 0, fmt.Errorf("group: PublishRosterMutation: sign: %w", err)
	}
	body, err := MarshalRoster(m)
	if err != nil {
		return 0, fmt.Errorf("group: PublishRosterMutation: marshal: %w", err)
	}
	path := fmt.Sprintf("%s/%05d", RosterPathPrefix, seq)
	if err := s.pub.Put(path, body); err != nil {
		return 0, fmt.Errorf("group: PublishRosterMutation: Put %s: %w", path, err)
	}
	return seq, nil
}

// PublishAdminMutation is the parallel emitter for AdminMutation —
// promote / demote of a peer's admin authority. Same envelope shape
// and semantics as PublishRosterMutation, scoped to the admin-feed
// path (AdminPathPrefix/<seq>) and using s.adminSeq.
func (s *Session) PublishAdminMutation(op AdminOp, peerPK cipher.PubKey, parentSeq uint64) (uint64, error) {
	if s == nil || s.pub == nil {
		return 0, errors.New("group: PublishAdminMutation: no live publisher")
	}
	seq := s.adminSeq.Add(1)
	m := AdminMutation{
		GroupID:   uuid.MustParse(s.cfg.Record.ID),
		Op:        op,
		PeerPK:    peerPK,
		ParentSeq: parentSeq,
		IssuedAt:  time.Now().UTC(),
	}
	if err := SignAdmin(&m, s.cfg.MySK); err != nil {
		return 0, fmt.Errorf("group: PublishAdminMutation: sign: %w", err)
	}
	body, err := MarshalAdmin(m)
	if err != nil {
		return 0, fmt.Errorf("group: PublishAdminMutation: marshal: %w", err)
	}
	path := fmt.Sprintf("%s/%05d", AdminPathPrefix, seq)
	if err := s.pub.Put(path, body); err != nil {
		return 0, fmt.Errorf("group: PublishAdminMutation: Put %s: %w", path, err)
	}
	return seq, nil
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
	// Sign with this session's SK. publishAs accepts a parametric
	// senderPK (the relay path needs to publish as someone else),
	// but the SIGNATURE is always over (senderPK, msg) with this
	// session's SK — i.e., the signer asserts "I, this session,
	// republished this Message and I take responsibility for it".
	// In the steady-state federated case where senderPK == MyPK, the
	// signature is the original author's own signature.
	//
	// The relay path (handleRelay → owner publishes member's text)
	// produces a signature where senderPK != MyPK; subscribers
	// verifying see a signature that DOESN'T bind to the claimed
	// SenderPK and reject the leaf. This is intentional: post-D1
	// federated send is the norm, and the legacy relay path is a
	// pre-D1 fallback. A signed legacy-relay leaf would let the
	// owner forge messages as any member. Forcing relay leaves to
	// fail Verify pushes operators toward the federated path; we
	// can add a separate "owner-asserts-by-relay" envelope type
	// later if the legacy path needs to survive past TTL.
	if err := SignMessage(&msg, s.cfg.MySK); err != nil {
		return fmt.Errorf("group: publishAs: sign: %w", err)
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
	// they are wire-level liveness probes, not chat content.
	//
	// Critically: do NOT bump lastInboundNs here. Pre-#2595 this
	// bump was harmless because detectStaleAndReconnect skipped
	// owner-role sessions entirely (the "useful symmetry" the prior
	// comment claimed didn't actually drive any behavior). Post-
	// #2595 the reconnect loop covers owner-role peerSubs too, and
	// the owner's OWN heartbeat (every 30s by default) bumping
	// lastInboundNs keeps every owner session permanently
	// "fresh" — staleness threshold (100s) is never crossed,
	// reconnect never fires, peer-publisher restarts are never
	// recovered from. The bump made reconnect a no-op for owners
	// since #2595 landed.
	//
	// lastInboundNs is the **inbound** liveness signal — it should
	// only advance on observable inbound events from PEERS. Local
	// heartbeat emission is an outbound write; it proves the
	// publisher is healthy enough to publish but says nothing about
	// whether peer subscribers are attached or peer publishers are
	// reachable. Conflating the two masked the peerSub reachability
	// problem that #2600's Connect-error-on-zero-success aimed to
	// surface.
	if text == HeartbeatMarker {
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
	// Walk both prefixes on every source: the local publisher carries
	// our own sends under msgs/ plus (if this visor is an admin) any
	// mirror/ copies of peers' leaves; peer subscribers may carry
	// mirror leaves authored by the peer when the peer is an admin.
	// Dedup is applied by canonical suffix below, so a leaf reachable
	// through multiple paths produces a single replayed message.
	seen := make(map[string]struct{})
	// Walk callbacks always return true — replay is unconditionally
	// "drain everything available"; we filter duplicates after the
	// fact rather than short-circuiting the iterator. Returning false
	// here would tell the treestore to stop walking, which is the
	// wrong semantics for dedup-by-content (the duplicate may still
	// have unique siblings in the same subtree).
	walk := func(path string, value []byte) bool { //nolint:unparam // Walk requires bool return; we never short-circuit on purpose
		key := dedupKey(path)
		if key != "" {
			if _, dup := seen[key]; dup {
				return true
			}
			seen[key] = struct{}{}
		}
		// Defensive copy: tree-store Walk may reuse the value
		// slice between callback invocations.
		v := make([]byte, len(value))
		copy(v, value)
		leaves = append(leaves, leaf{path: path, value: v})
		return true
	}
	walkBoth := func(walker func(prefix string, f func(path string, value []byte) bool)) {
		walker(MessagePathPrefix, walk)
		walker(MirrorPathPrefix, walk)
	}
	if s.pub != nil {
		walkBoth(s.pub.Walk)
	}
	if s.sub != nil {
		walkBoth(s.sub.Walk)
	}
	s.peerSubsMu.RLock()
	peerSubs := make([]*treestore.Subscriber, 0, len(s.peerSubs))
	for _, ps := range s.peerSubs {
		peerSubs = append(peerSubs, ps)
	}
	s.peerSubsMu.RUnlock()
	for _, ps := range peerSubs {
		walkBoth(ps.Walk)
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
	// Admin-mirror side-effect removed: pre-simplification, admins
	// re-published every observed leaf under MirrorPathPrefix so an
	// offline original-sender wouldn't take the message with them.
	// In practice the redundancy doubled the per-peer subscription
	// count (each peer fanned out to msgs/ AND mirror/ prefixes) and
	// added a publish-on-receive feedback loop that obscured the
	// failure-mode picture during reliability runs. The same
	// offline-sender use case is now covered by per-peer history
	// replay on reconnect (#2659): when a member re-attaches, the
	// per-peer subscriber walks the publisher's CXO tree from
	// SinceTimestampNs and gets every leaf the publisher still has,
	// without needing a separate admin-driven mirror.
	h := s.handler
	if h == nil {
		return
	}
	for _, ev := range events {
		if ev.Value == nil {
			continue
		}
		// Cross-source dedup: collapse the (primary + N mirror copies)
		// fan-in to one delivery per source leaf. The dedup key is the
		// canonical path suffix shared by msgs/X/Y/Z and mirror/X/Y/Z,
		// so whichever copy arrives first wins; subsequent copies are
		// skipped silently. recentSet.Add returns false for already-
		// seen keys. dedupKey returns "" for non-message subtrees,
		// which recentSet.Add treats as always-new — those flow through
		// to handler invocation as before (heartbeats / config / etc.).
		if key := dedupKey(ev.Path); key != "" {
			if !s.dedup.Add(key) {
				continue
			}
		}
		var msg Message
		if err := json.Unmarshal(ev.Value, &msg); err != nil {
			s.log.WithError(err).WithField("path", ev.Path).
				Debug("group: ignoring undecodable leaf")
			continue
		}
		// Verify the leaf's signature against the claimed SenderPK.
		// Admin-aggregator republishes are accepted iff the original
		// author's signature still verifies — admins can omit but not
		// alter. Backward-compat: pre-signing publishers produce
		// unsigned leaves (Signature == cipher.Sig{}); these surface
		// as ErrLeafUnsignedLegacy which we accept-with-warning during
		// the deprecation window, then flip to strict-reject once
		// publisher TTL elapses. Any other VerifyMessage error
		// (forged signature, wrong PK, unknown canonical version) is
		// a strict reject right now — the leaf is silently dropped
		// at debug log because an honest admin shouldn't be
		// republishing leaves that don't verify and we don't want a
		// noisy log surface for what's effectively an attack signal.
		if err := VerifyMessage(msg); err != nil {
			if errors.Is(err, ErrLeafUnsignedLegacy) {
				s.log.WithField("path", ev.Path).WithField("sender_pk", msg.SenderPK.String()).
					Debug("group: accepting unsigned legacy leaf (deprecated; publisher predates signing)")
				// Fall through — accept the leaf.
			} else {
				s.log.WithError(err).WithField("path", ev.Path).WithField("sender_pk", msg.SenderPK.String()).
					Debug("group: rejecting leaf with bad signature")
				continue
			}
		}
		// Admin-aggregator republish (Beta slice): if this session is
		// an admin on the group record, write the verified leaf
		// VERBATIM (same path, same body bytes) onto this admin's own
		// publisher feed. Non-admin members in the admin-aggregator
		// topology subscribe to admin canonical feeds rather than to
		// every peer's per-peer feed, so this republish is what makes
		// the source leaf reachable through this admin when the
		// original sender's publisher is offline.
		//
		// Gating:
		//   - VerifyMessage above succeeded (or was the unsigned-legacy
		//     accept-with-warning) — admins act as gatekeepers, not as
		//     forwarders of forged input. ErrLeafBadSignature already
		//     continued out of the loop above.
		//   - s.pub != nil — degraded sessions (e.g. the test fixture
		//     with no publisher) skip cleanly instead of panicking.
		//   - msg.SenderPK != s.cfg.MyPK — our own publishAs already
		//     wrote this leaf on our publisher at this path; a second
		//     Put is a content-equal no-op but skip the round-trip.
		//   - !IsHeartbeat(msg) — heartbeats are per-publisher liveness
		//     probes; non-admin subscribers get liveness from the
		//     admin's OWN heartbeats, no need to mirror others'.
		//   - dedup.Add returned true above (line ~2104) — protects
		//     against cross-admin republish loops: when admin-A and
		//     admin-B both observe sender-C's leaf, whichever observes
		//     first republishes; the other's observation (via its own
		//     peerSub or via cross-admin subscription) finds the leaf
		//     already in the dedup set and skips here too.
		//
		// Failures are debug-logged, not propagated. Admin republish is
		// opportunistic redundancy — the user-facing inbox fan-out
		// below MUST NOT be gated on the republish Put succeeding.
		if s.pub != nil && !IsHeartbeat(msg) && msg.SenderPK != s.cfg.MyPK && s.cfg.Record.IsAdmin(s.cfg.MyPK) {
			if err := s.pub.Put(ev.Path, ev.Value); err != nil {
				s.log.WithError(err).WithField("path", ev.Path).WithField("sender_pk", msg.SenderPK.String()).
					Debug("group: admin-republish put failed")
			}
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
