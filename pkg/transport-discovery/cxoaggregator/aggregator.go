// Package cxoaggregator pkg/transport-discovery/cxoaggregator/aggregator.go c4-net-discovery
// that subscribes to per-visor TreeStore feeds and mirrors received
// telemetry into the TPD's redis store.
//
// Architecture: visors dial TPD's CXO listener (the visor knows
// TPD's PK from Transport.DiscoveryDmsg) and the inbound conns drive
// subscription. The aggregator does NOT enumerate visors and dial
// out — that approach has trouble with reconnection (no clean signal
// when a remote node restarts) and requires plumbing the full visor
// list. Reverse-dial:
//
//   - Visor brings up its publisher and periodically calls
//     pub.AnnounceTo(tpdPK), which dials this aggregator's CXO node
//     over DMSG. ConnectPK is idempotent — alive conn = no-op,
//     dropped conn = redial.
//   - Aggregator's reconcile loop walks Node.Connections(), and for
//     each conn it isn't already subscribed to as a feed (where the
//     feed PK == the conn's remote PK), calls conn.Subscribe.
//   - When CXO finishes filling a Root from a subscribed feed,
//     OnRootFilled fires; the aggregator walks the TreeStore tree,
//     parses leaves, dispatches transports/<uuid>/current updates
//     to BandwidthSink.
//
// Visor restart is handled implicitly: visor reconnects to DMSG,
// re-dials TPD, the new conn arrives at TPD, next reconcile
// subscribes again. No staleness detection needed on TPD's side.
//
// One CXO Node serves all subscriptions; visors with many feeds (or
// future TPD-side re-publishing) share storage and goroutines
// instead of paying a per-visor node cost.
package cxoaggregator

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/cxo/node"
	cxotransport "github.com/skycoin/skywire/pkg/cxo/node/transport"
	"github.com/skycoin/skywire/pkg/cxo/skyobject"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
)

// Sink receives per-transport updates from visor TreeStore feeds. The
// telemetry methods (UpdateBandwidth / UpdateLatency /
// RecordTransportHeartbeat / IngestTransportTimeline) are satisfied
// by the TPD's redis store directly. The metadata methods
// (RegisterTransportFromCXO / DeregisterTransportFromCXO) require
// the redis write plus the API-layer DHT mirror update; the wiring
// in cmd/svc/transport-discovery/commands/root.go composes a sink
// adapter that delegates each method to the appropriate place.
type Sink interface {
	UpdateBandwidth(ctx context.Context, transportID string, reporterPK cipher.PubKey, sent, recv uint64) error
	UpdateLatency(ctx context.Context, transportID string, minMS, maxMS, avgMS float64) error
	// at is the visor-side observation time (snap.SampledAt), so a
	// heartbeat that crosses a 5-minute slot boundary in transit
	// still credits the slot the visor was actually online for.
	RecordTransportHeartbeat(ctx context.Context, tpID uuid.UUID, tpType string, at time.Time) error
	// IngestTransportTimeline OR-merges a visor-supplied 36-byte
	// per-transport uptime bitmap into the persistent timeline for
	// (tpID, date). Used by dispatchLeaf to absorb the
	// transports/<id>/<date>/timeline leaves.
	IngestTransportTimeline(ctx context.Context, tpID uuid.UUID, date string, bitmap []byte) error
	// RegisterTransportFromCXO accepts a transport entry published on
	// a visor's TreeStore feed under transports/<uuid>/entry. The
	// implementation MUST verify reporter is an edge of the entry
	// (auth equivalence to the SW-Sig httpauth used by the HTTP
	// register endpoints). version is the publishing visor's build
	// version (formerly carried in the SW-Visor-Version header).
	RegisterTransportFromCXO(ctx context.Context, entry *transport.Entry, reporter cipher.PubKey, version string) error
	// DeregisterTransportFromCXO accepts a delete signal published on
	// a visor's TreeStore feed under transports/<uuid>/tombstone. The
	// implementation MUST verify reporter is an edge of the existing
	// entry; an unknown ID is treated as a no-op (idempotent) so a
	// tombstone that arrives after a TPD-side eviction doesn't error.
	DeregisterTransportFromCXO(ctx context.Context, id uuid.UUID, reporter cipher.PubKey) error
	// ReconcileTransportsFromCXO applies the visor's full transport list
	// published as a single snapshot leaf at transports/list: it
	// registers every entry the reporter is an edge of AND deregisters
	// any of the reporter's existing transports that are ABSENT from the
	// list (absence = deletion). This is the declarative replacement for
	// the per-transport entry/tombstone leaves — self-healing, since a
	// missed update is corrected by the next snapshot.
	ReconcileTransportsFromCXO(ctx context.Context, entries []*transport.Entry, reporter cipher.PubKey, version string) error
}

// BandwidthSink is retained as an alias for callers that only need the
// bandwidth half of the contract.
//
// Deprecated: use Sink. Kept so external embedders that implemented
// BandwidthSink keep compiling until they migrate.
type BandwidthSink = Sink

// liveSnapshot mirrors pkg/visor/stats.LiveSnapshot. Re-declared on
// the TPD side to keep the dependency direction one-way: visor →
// spec → wire format → TPD-side parser. Field renames must be
// reflected in both places.
type liveSnapshot struct {
	SentBytes    uint64    `json:"sent_bytes"`
	RecvBytes    uint64    `json:"recv_bytes"`
	LatencyMinMS float64   `json:"latency_min_ms,omitempty"`
	LatencyMaxMS float64   `json:"latency_max_ms,omitempty"`
	LatencyAvgMS float64   `json:"latency_avg_ms,omitempty"`
	SampledAt    time.Time `json:"sampled_at"`
	Type         string    `json:"type,omitempty"`
}

// transportEntryLeaf is the wire shape for transports/<uuid>/entry
// leaves. Carries the bare transport.Entry plus the publishing
// visor's build version (parity with the SW-Visor-Version header
// used on POST /v3/transports/). Re-declared on both sides to keep
// the dependency direction one-way; the visor publisher in
// pkg/transport/manager.go publishes the same JSON shape.
type transportEntryLeaf struct {
	Version string           `json:"version,omitempty"`
	Entry   *transport.Entry `json:"entry"`
}

// transportListLeaf is the wire shape for the transports/list snapshot
// leaf: the visor's full current transport set plus its build version.
// Re-declared here (one-way dependency); the visor publisher in
// pkg/transport/manager.go (publishTPDList) publishes the same JSON.
type transportListLeaf struct {
	Version string             `json:"version,omitempty"`
	Entries []*transport.Entry `json:"entries,omitempty"` // legacy full form (pre-compact visors)
	// Compact is the space-optimized form: each row is the remote edge +
	// type only (the reporter's own PK and the derivable transport ID are
	// dropped — ~62% smaller). Reconstructed to full entries via
	// transport.EntryFromCompact(reporter, ce). Visors on develop publish
	// this; older visors still publish Entries, which are read as-is.
	Compact []transport.CompactEntry `json:"c,omitempty"`
}

// entries returns the reconstructed full-entry set from whichever form
// the publisher sent: the compact rows (reconstructed against reporter)
// when present, else the legacy full Entries.
func (l *transportListLeaf) entries(reporter cipher.PubKey) []*transport.Entry {
	if len(l.Compact) > 0 {
		out := make([]*transport.Entry, 0, len(l.Compact))
		for _, ce := range l.Compact {
			out = append(out, transport.EntryFromCompact(reporter, ce))
		}
		return out
	}
	return l.Entries
}

// tombstoneLeaf is the wire shape for transports/<uuid>/tombstone
// leaves. The timestamp is informational (the visor's local
// deletion time); TPD applies the deletion at receive time and does
// not use the timestamp for ordering.
type tombstoneLeaf struct {
	DeletedAt time.Time `json:"deleted_at"`
}

// Config configures the Aggregator.
type Config struct {
	// ReconcileInterval is how often the aggregator re-scans the
	// CXO node's connection list and ensures each is subscribed to
	// the remote PK's feed. Defaults to 30s — small enough that a
	// fresh visor conn doesn't wait too long to see updates flow.
	ReconcileInterval time.Duration
	// CleanupInterval is how often the aggregator prunes superseded
	// Roots (keeping only the latest per feed) and sweeps the freed
	// objects from its CXO store. Without this the in-memory store
	// accumulates every Root every visor ever published — the store
	// is ephemeral (redis is authoritative), so only the newest Root
	// per feed matters. Defaults to 2m. Mirrors the treestore
	// Publisher's runCleanup, which the aggregator's node lacked.
	CleanupInterval time.Duration
	// MaxFillingTime caps how long the CXO node will keep a single Root
	// fill in flight before aborting it with ErrTimeout. The node default
	// (node.NewConfig) is 10m, which is far too generous for TPD's
	// aggregator: it subscribes to every visor feed, and any flapping peer
	// leaves its fill hung — holding all the fill's "wanted" objects in the
	// in-memory cache (which cleanDown skips, so LRU can't evict them) for
	// the full 10m before the timeout fires Unwant. With hundreds of
	// unstable peers that standing pool of hung-fill memory is the residual
	// leak the Root-prune cleanup can't reach. The aggregator is best-effort
	// (an aborted fill just retries when the visor republishes), so we cap
	// this low — a healthy transport-tree fill completes in seconds, and a
	// fill still running after this long is a broken peer, not a slow one.
	// Defaults to 90s.
	MaxFillingTime time.Duration
	// Logger overrides the default tagged logger.
	Logger *logging.Logger
	// SecKey binds the aggregator's CXO node identity to TPD's service
	// secret key so the node's handshake-advertised PK is TPD's KNOWN PK.
	// This matters because the visor's telemetry / tp-list feeds gate their
	// subscriber allowlist on the CXO node's PeerID (see pkg/cxo/treestore
	// allowlist + pkg/visor feed gating): visors allow the TPD PK they hold
	// in transport.discovery_dmsg. Left zero, node.NewNode generates a
	// RANDOM keypair, so the aggregator dials every gated visor as an
	// unknown PK and its subscribe is rejected — TPD then never fills that
	// visor's transports (the persistent visor↔TPD transport-count gap on
	// any visor running feed gating). The publisher path (treestore.
	// NewWithDMSG) binds the same way for the same reason.
	SecKey cipher.SecKey
	// InMemoryDB / DataDir control the aggregator's CXO storage.
	// Default is in-memory (subscribed object cache only — TPD's
	// authoritative store is redis, the CXO cache exists just to
	// satisfy CXO's filling protocol).
	InMemoryDB bool
	DataDir    string
	// DmsgPort is the DMSG port this aggregator's CXO node listens on
	// (and dials visors back on). Zero falls back to
	// cxotransport.DefaultCXOPort (50), the visor telemetry feed. TPD
	// runs a SECOND aggregator on skyenv.DmsgVisorTPListCXOPort (69) for
	// the visors' dedicated tp-list discovery feed, which fills fully
	// because its Root is tiny — the two feeds MUST be on separate nodes
	// (hence separate ports) so the same visor PK doesn't deliver two
	// different Roots to one node and flap its head.
	DmsgPort uint16
}

// Aggregator is the CXO subscriber side of the visor-stats data path.
// It owns one CXO Node listening on DMSG; visors dial in, subscribe
// happens per-conn during reconcile, and OnRootFilled walks the
// TreeStore tree to feed BandwidthSink.
type Aggregator struct {
	cxoNode *node.Node
	sink    Sink
	conf    Config
	log     *logging.Logger

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}

	// nudge triggers an immediate reconcile out of band from the
	// ReconcileInterval ticker. Buffered(1) so bursts of connects
	// coalesce into a single pending reconcile (which is idempotent
	// and walks the full conn set anyway).
	nudge chan struct{}

	// orphanStrikes counts consecutive cleanup ticks a shared feed has
	// had no connected conn. A feed is reclaimed (un-shared + all its
	// Roots deleted) once it reaches orphanGraceTicks, so a visor that
	// briefly drops and redials within the grace window keeps its feed.
	// Only touched from cleanup() (single goroutine), so no lock.
	orphanStrikes map[skycipher.PubKey]int

	// dialing guards in-flight dial-backs so the heartbeat cadence can't
	// storm ConnectPK for the same visor. Keyed by feed PK. Guarded by mu.
	dialing map[skycipher.PubKey]struct{}

	// fetching coalesces in-flight targeted discovery-leaf fetches so a
	// visor that republishes rapidly can't spawn a pile of concurrent
	// per-object request goroutines for the same feed. Keyed by feed PK.
	// Guarded by mu.
	fetching map[skycipher.PubKey]struct{}

	// lastList caches the most-recent successfully decoded transport-list
	// snapshot per reporter PK. A transient targeted-fetch failure (the
	// delivering conn closed before the handful of tp-list objects
	// arrived) re-applies the last good full set on the next Root, holding
	// the reporter's transports against the redis TTL until a fresh fetch
	// lands. Dropped when the feed is reclaimed (reclaimOrphanFeeds).
	// Guarded by mu.
	lastList map[skycipher.PubKey]cachedList
}

// cachedList is the last good decoded transport-list snapshot for a
// reporter, kept so a failed targeted fetch can re-apply it (fallback #2).
type cachedList struct {
	entries []*transport.Entry
	version string
}

// ensureConnTimeout bounds each dial-back to a visor's CXO node so an
// unreachable visor doesn't pin a goroutine.
const ensureConnTimeout = 20 * time.Second

// tpdListLeafName is the current top-level CXO leaf path for a visor's
// transport-list discovery snapshot (mirrors tpdListPath in
// pkg/transport/manager.go). It is a direct child of the feed Root so its
// inline bytes land in the Root's first child-index page — reachable in a
// truncated partial fill, unlike the legacy "transports/list" which sorted
// last among the "transports" node's per-uuid children.
const tpdListLeafName = "tp-list"

// maxTargetedFetchObjects caps how many objects the targeted
// discovery-leaf fetch (reconcileTargeted) will request from a visor for
// one Root. The tp-list path is the registry + root TreeNode + the small
// top-level Children index page(s) + the tp-list TreeEntry — a handful of
// objects. The cap bounds a malicious or oversized Root so a crafted deep
// top-level index can't drive unbounded per-object requests to the peer.
const maxTargetedFetchObjects = 64

// targetedFetchTimeout bounds the whole targeted discovery-leaf fetch
// (all per-object requests for one Root together). Each underlying object
// request is separately bounded by the conn's ResponseTimeout; this is
// the ceiling on the sequential walk so a slow-drip peer can't pin the
// fetch goroutine.
const targetedFetchTimeout = 30 * time.Second

// errTargetedFetchBudget is returned by boundedGetter once the object
// count or wall-clock budget for a targeted fetch is exhausted. It
// unwinds the treestore walk cleanly (childByName treats an unreadable
// child as absent) rather than hanging.
var errTargetedFetchBudget = errors.New("cxoaggregator: targeted discovery fetch budget exceeded")

// orphanGraceTicks is how many consecutive cleanup ticks a feed must
// have zero connected conns before it's reclaimed. At the 2-minute
// default CleanupInterval this is a ~4-minute grace, long enough to
// ride out a visor's dmsg reconnect without churning a stable feed.
const orphanGraceTicks = 2

// New constructs an Aggregator. Sets up a CXO Node, enables DMSG so
// remote visors can dial in, and wires OnRootFilled to walk
// TreeStore Roots and dispatch to sink.
func New(dmsgC *dmsg.Client, sink Sink, conf Config) (*Aggregator, error) {
	if conf.ReconcileInterval <= 0 {
		conf.ReconcileInterval = 30 * time.Second
	}
	if conf.CleanupInterval <= 0 {
		conf.CleanupInterval = 2 * time.Minute
	}
	if conf.MaxFillingTime <= 0 {
		conf.MaxFillingTime = 90 * time.Second
	}
	if conf.Logger == nil {
		conf.Logger = logging.MustGetLogger("tpd-cxo-aggregator")
	}

	cfg := node.NewConfig()
	// Bind the node identity to TPD's service key (when provided) so its
	// handshake PK is TPD's known PK, matching what gated visors allowlist.
	// Zero SecKey => node.NewNode mints a random keypair => gated visors
	// reject the aggregator's subscribe. See Config.SecKey.
	if conf.SecKey != (cipher.SecKey{}) {
		cfg.SecKey = skycipher.SecKey(conf.SecKey)
	}
	// We're DMSG-only — disable the CXO node's default TCP/UDP/RPC
	// listeners. node.NewConfig defaults TCP.Listen to ":8870" and RPC to
	// ":8871", and those hardcoded ports mean two Nodes in the same process
	// collide on bind: NewNode's TCP/RPC Listen returns "address already in
	// use" and the whole aggregator fails to construct. TPD runs TWO
	// aggregators in one process (the port-50 telemetry feed and the port-69
	// tp-list feed), so the SECOND New silently failed here — its node never
	// came up, so it never dmsg-Listened on its DMSG port and never accepted
	// the visors' tp-list announces (the persistent visor↔TPD transport-count
	// gap). None of these listeners are reachable over DMSG anyway; the
	// publisher path (treestore.NewWithDMSG) already zeroes them for the same
	// reason.
	cfg.TCP.Listen = ""
	cfg.UDP.Listen = ""
	cfg.RPC = ""
	// Override the 10m node default: cap hung fills from flapping peers so
	// their "wanted" objects are released in seconds, not minutes. See the
	// Config.MaxFillingTime doc for why this is the aggregator's residual
	// memory leak.
	cfg.MaxFillingTime = conf.MaxFillingTime
	cfg.Config = skyobject.NewConfig()
	cfg.Config.InMemoryDB = conf.InMemoryDB || conf.DataDir == ""
	if conf.DataDir != "" {
		cfg.Config.DataDir = conf.DataDir
	}

	// Wire the cxo node's internal logger so its FillPin / MsgReceivePin
	// / connection-handshake debug output goes to stderr (where docker
	// logs reads it). Without this, cxo's "[fill] ..." and "[%s]
	// handleSub %s" lines are silent regardless of tpd's --loglvl, and
	// a fill failure / handshake mismatch is invisible. Debug is gated
	// on the global logger level so prod runs at INFO get nothing extra.
	cfg.Logger.Output = os.Stderr
	cfg.Logger.Prefix = "[tpd-cxo-aggregator:node] "
	if lvl := logging.GetLevel(); lvl == logrus.DebugLevel || lvl == logrus.TraceLevel {
		cfg.Logger.Debug = true
		// Filter to the pins that diagnose Subscribe → Root delivery →
		// fill chains. ConnPin is loud-on-startup but only one-shot;
		// MsgPin would be too chatty (every Root + every chunk request),
		// so include only MsgReceivePin which captures the publisher
		// side of subscribe/root receipt.
		cfg.Logger.Pins = node.FillPin | node.ConnPin | node.MsgReceivePin
	}

	cxoNode, err := node.NewNode(cfg)
	if err != nil {
		return nil, err
	}
	dmsgPort := conf.DmsgPort
	if dmsgPort == 0 {
		dmsgPort = cxotransport.DefaultCXOPort
	}
	factory := cxotransport.NewDMSGFactory(dmsgC, dmsgPort)
	if err := cxoNode.EnableDMSG(factory); err != nil {
		_ = cxoNode.Close() //nolint:errcheck
		return nil, err
	}

	a := &Aggregator{
		cxoNode:       cxoNode,
		sink:          sink,
		conf:          conf,
		log:           conf.Logger,
		done:          make(chan struct{}),
		nudge:         make(chan struct{}, 1),
		orphanStrikes: make(map[skycipher.PubKey]int),
		fetching:      make(map[skycipher.PubKey]struct{}),
		lastList:      make(map[skycipher.PubKey]cachedList),
	}
	// Subscribe to a visor's feed the moment it dials in, rather than
	// waiting up to ReconcileInterval (30s) for the next poll. A fresh
	// visor (notably a browser wasm-visor) that AnnounceTo's the TPD and
	// then registers a transport is otherwise invisible to the shared
	// redis edge-index — and thus to the route-finder — for up to a full
	// reconcile period, so `route find <fresh-visor> <exit>` 404s
	// ("transport not found") until the poll catches up. The conn's
	// handshake completes (peerID set) before OnConnect fires, so the
	// nudged reconcile can subscribe immediately; it stays idempotent via
	// alreadySubscribed, and the periodic ticker remains the safety net.
	cxoNode.Config().OnConnect = func(_ *node.Conn) error {
		select {
		case a.nudge <- struct{}{}:
		default:
		}
		return nil
	}
	// Wire all three Root-lifecycle callbacks. Together they make the
	// CXO replication chain observable from tpd's logs:
	//
	//   - OnRootReceived: a Root payload arrived from the visor's
	//     publisher. Logged at Debug. Lets us tell "Subscribe but no
	//     data" (no OnRootReceived ever) from "data arrives but
	//     can't be processed" (OnRootReceived fires but OnRootFilled
	//     doesn't).
	//   - OnRootFilled: the Root and ALL its referenced objects have
	//     been replicated successfully — the aggregator can now walk
	//     the tree. This is the existing dispatch trigger.
	//   - OnFillingBreaks: replication of a Root's referenced objects
	//     failed (timeout, peer drop, missing object). Logged at Warn
	//     so it surfaces at the default log level — without this,
	//     fill failures are silent.
	cxoNode.Config().OnRootReceived = func(c *node.Conn, r *registry.Root) error {
		a.log.WithField("visor", cipher.PubKey(r.Pub)).WithField("seq", r.Nonce).
			Debug("CXO aggregator: root received")
		// The inbound conn the visor AnnounceTo'd us on closes moments after
		// delivering this Root, so the immediate fill of its referenced objects
		// hits "no connections to fill from". Establish a TPD-OWNED outbound
		// conn to the visor — we hold it, so subsequent Roots (the publisher
		// re-announces on a heartbeat) have a stable source to fill from.
		a.ensureConn(r.Pub)
		// Discovery decoupled from the whole-Root telemetry fill: pull JUST
		// the small tp-list leaf path off this Root now, over the delivering
		// conn, independent of the 90s MaxFillingTime fill of the bulky
		// per-transport telemetry subtree (which for a busy visor never
		// completes in the conn's window — the discovery gap). The full list
		// lands from a handful of objects, so it survives the same short conn
		// that a whole-tree fill can't. Detached so its blocking per-object
		// requests can't stall the CXO node's per-head event loop (this
		// callback runs on it).
		a.reconcileTargeted(c, r)
		return nil
	}
	cxoNode.Config().OnRootFilled = func(_ *node.Node, r *registry.Root) {
		a.log.WithField("visor", cipher.PubKey(r.Pub)).WithField("seq", r.Nonce).
			Debug("CXO aggregator: root filled")
		a.handleRootFilled(r)
	}
	cxoNode.Config().OnFillingBreaks = func(_ *node.Node, r *registry.Root, reason error) {
		a.log.WithError(reason).WithField("visor", cipher.PubKey(r.Pub)).WithField("seq", r.Nonce).
			Warn("CXO aggregator: root filling broke")
		// Discovery-critical recovery: a broken full-tree fill still usually
		// left transports/list in the store (see recoverTransportListOnBreak).
		a.recoverTransportListOnBreak(r)
	}
	return a, nil
}

// Run starts the reconcile loop. Returns immediately; the loop
// continues until ctx is canceled or Close is called. Idempotent.
func (a *Aggregator) Run(ctx context.Context) {
	a.mu.Lock()
	if a.cancel != nil {
		a.mu.Unlock()
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.mu.Unlock()

	go a.loop(loopCtx)
}

// Close stops the loop and tears down the CXO node. Idempotent.
func (a *Aggregator) Close() error {
	a.mu.Lock()
	cancel := a.cancel
	a.cancel = nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
		<-a.done
	}
	return a.cxoNode.Close()
}

// FeedPK returns the aggregator's own PK — the value visors should
// dial via dmsgT.ConnectPK to put a conn on this aggregator. In
// practice this is the TPD deployment's PK and visors get it from
// Transport.DiscoveryDmsg, but exposing it here makes wiring tests
// straightforward.
func (a *Aggregator) FeedPK() cipher.PubKey {
	return cipher.PubKey(a.cxoNode.ID())
}

func (a *Aggregator) loop(ctx context.Context) {
	defer close(a.done)
	t := time.NewTicker(a.conf.ReconcileInterval)
	defer t.Stop()
	ct := time.NewTicker(a.conf.CleanupInterval)
	defer ct.Stop()

	a.reconcile()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.reconcile()
		case <-ct.C:
			a.cleanup()
		case <-a.nudge:
			// A visor just connected (OnConnect). Subscribe now instead
			// of waiting for the next tick. reconcile() is idempotent.
			a.reconcile()
		}
	}
}

// cleanup prunes superseded Roots (keeping only the latest per feed)
// and sweeps the now-ownerless objects from the aggregator's CXO
// store. TPD's authoritative store is redis and each Root is
// dispatched on fill, so only the newest Root per feed is needed;
// without this the in-memory store grows without bound as every
// visor republishes on each transport change. Same primitives the
// treestore Publisher's runCleanup uses (keepLast=1) — the
// aggregator's receive-side node just never had a cleanup loop.
func (a *Aggregator) cleanup() {
	c := a.cxoNode.Container()
	if err := cxoutils.RemoveRootObjects(c, 1); err != nil {
		a.log.WithError(err).Debug("CXO aggregator: RemoveRootObjects failed; will retry next tick")
		return
	}
	a.reclaimOrphanFeeds(c)
	if err := cxoutils.RemoveObjects(c); err != nil {
		a.log.WithError(err).Debug("CXO aggregator: RemoveObjects failed; will retry next tick")
	}
}

// reclaimOrphanFeeds un-shares and hard-deletes feeds that no longer
// have a connected conn. RemoveRootObjects(c, 1) keeps the LAST Root of
// every feed forever, so without this a feed whose visor has gone away
// (notably an ephemeral wasm/browser visor with a one-shot PK) pins its
// final Root tree — and every object it references at rc>0 — in the
// in-memory CXDS permanently. Over time the store grows without bound as
// visor PKs churn (the +105 MB/12min inuse + 1576-goroutine growth seen
// in production heap deltas). DontShare unsubscribes the conns and
// removes the feed from the node's share set; DelFeed deletes all its
// Roots and decrements the referenced objects to rc==0 so the following
// RemoveObjects sweep can finally reclaim them.
//
// Reclamation is grace-gated (orphanGraceTicks) so a visor that drops
// and redials within a few cleanup ticks keeps its feed rather than
// paying a full re-replication.
func (a *Aggregator) reclaimOrphanFeeds(c *skyobject.Container) {
	connected := make(map[skycipher.PubKey]struct{})
	for _, conn := range a.cxoNode.Connections() {
		if pk := conn.PeerID(); pk != (skycipher.PubKey{}) {
			connected[pk] = struct{}{}
		}
	}

	self := a.cxoNode.ID()
	for _, feed := range c.Feeds() {
		if feed == self {
			// The node's own identity feed — never reclaim it.
			delete(a.orphanStrikes, feed)
			continue
		}
		if _, ok := connected[feed]; ok {
			// Live feed — clear any accumulated strikes.
			delete(a.orphanStrikes, feed)
			continue
		}
		a.orphanStrikes[feed]++
		if a.orphanStrikes[feed] < orphanGraceTicks {
			continue
		}
		delete(a.orphanStrikes, feed)
		if err := a.cxoNode.DontShare(feed); err != nil {
			a.log.WithError(err).WithField("visor", cipher.PubKey(feed)).
				Debug("CXO aggregator: DontShare orphan feed failed; will retry next tick")
			continue
		}
		if err := c.DelFeed(feed); err != nil {
			a.log.WithError(err).WithField("visor", cipher.PubKey(feed)).
				Debug("CXO aggregator: DelFeed orphan feed failed; will retry next tick")
			continue
		}
		// Drop the reporter's cached transport-list snapshot: the feed is
		// gone (its Roots deleted), so there's nothing to re-apply and the
		// entry would otherwise pin memory as visor PKs churn.
		a.mu.Lock()
		delete(a.lastList, feed)
		a.mu.Unlock()
		a.log.WithField("visor", cipher.PubKey(feed)).
			Debug("CXO aggregator: reclaimed orphan feed (no connected conn)")
	}
}

// ensureConn keeps a TPD-initiated, persistent CXO connection to a visor whose
// feed we aggregate. The aggregator otherwise ONLY accepts inbound conns (the
// visor's AnnounceTo), and those close moments after delivering a Root — so the
// fill of the Root's referenced objects finds "no connections to fill from" and
// TPD ingests almost nothing (the transport-discovery gap). A TPD-owned
// outbound conn survives (we hold it), giving every subsequent Root a stable
// source to fill from. Idempotent: ConnectPK returns the cached live conn when
// one exists; a per-PK in-flight guard prevents dial storms under the heartbeat
// cadence; unreachable visors just fail the bounded dial and are retried on the
// next Root.
func (a *Aggregator) ensureConn(feedPK skycipher.PubKey) {
	if feedPK == (skycipher.PubKey{}) || feedPK == a.cxoNode.ID() {
		return
	}
	a.mu.Lock()
	if a.dialing == nil {
		a.dialing = make(map[skycipher.PubKey]struct{})
	}
	if _, busy := a.dialing[feedPK]; busy {
		a.mu.Unlock()
		return
	}
	a.dialing[feedPK] = struct{}{}
	a.mu.Unlock()

	go func() {
		defer func() {
			a.mu.Lock()
			delete(a.dialing, feedPK)
			a.mu.Unlock()
		}()
		var pk cipher.PubKey
		copy(pk[:], feedPK[:])
		ctx, cancel := context.WithTimeout(context.Background(), ensureConnTimeout)
		defer cancel()
		if _, err := a.cxoNode.DMSG().ConnectPK(ctx, pk); err != nil {
			a.log.WithError(err).WithField("visor", cipher.PubKey(feedPK)).
				Debug("CXO aggregator: dial-back to visor failed; will retry on next Root")
			return
		}
		// Warm conn established/confirmed — nudge a reconcile so we subscribe
		// on it promptly (idempotent via alreadySubscribed).
		select {
		case a.nudge <- struct{}{}:
		default:
		}
	}()
}

// reconcile walks the current CXO conn set and subscribes to every
// remote PK's feed that isn't already subscribed on its conn. Any
// conn that has dropped since the last reconcile simply isn't in
// the list anymore — no explicit cleanup needed.
func (a *Aggregator) reconcile() {
	for _, conn := range a.cxoNode.Connections() {
		peerPK := conn.PeerID()
		if peerPK == (skycipher.PubKey{}) {
			// Conn handshake hasn't completed; skip until next tick.
			continue
		}
		if alreadySubscribed(conn, peerPK) {
			continue
		}
		if err := conn.Subscribe(peerPK); err != nil {
			a.log.WithError(err).WithField("visor", cipher.PubKey(peerPK)).
				Debug("CXO aggregator: Subscribe failed; will retry next reconcile")
			continue
		}
		a.log.WithField("visor", cipher.PubKey(peerPK)).Debug("CXO aggregator: subscribed to visor feed")
	}
}

// alreadySubscribed reports whether conn is already subscribed to
// the given feed. Used to make reconcile idempotent — repeat ticks
// don't keep stacking subscriptions.
func alreadySubscribed(conn *node.Conn, feed skycipher.PubKey) bool {
	for _, f := range conn.Feeds() {
		if f == feed {
			return true
		}
	}
	return false
}

// handleRootFilled is the OnRootFilled callback — invoked when CXO
// finishes filling a Root from a subscribed feed. r.Pub identifies
// the visor whose feed produced this Root; we use it as the
// reporter PK for any bandwidth dispatches.
func (a *Aggregator) handleRootFilled(r *registry.Root) {
	if r == nil || len(r.Refs) == 0 {
		return
	}
	pack, err := a.cxoNode.Container().Pack(r, treestore.Registry)
	if err != nil {
		a.log.WithError(err).Debug("CXO aggregator: get pack failed")
		return
	}
	var rootNode treestore.TreeNode
	if err := r.Refs[0].Value(pack, &rootNode); err != nil {
		a.log.WithError(err).Debug("CXO aggregator: decode root TreeNode failed")
		return
	}
	reporter := cipher.PubKey(r.Pub)
	// A root with no publisher identity can't be attributed to a visor.
	// Dispatching it would write bandwidth under the zero PubKey — the
	// "0000…:sent" per-edge fields and the zero-PK per-visor key that became
	// the null-PK orphan transports and the ghost-attributed bandwidth in
	// /metrics. Drop it rather than mis-attribute.
	if reporter == (cipher.PubKey{}) {
		a.log.Debug("CXO aggregator: dropping root with zero publisher PK")
		return
	}
	a.walkAndDispatch(pack, &rootNode, "", reporter)
}

// recoverTransportListOnBreak is the OnFillingBreaks best-effort path.
//
// A visor's Root carries the small transports/list discovery leaf PLUS
// its bulky per-transport telemetry subtrees (transports/<uuid>/current,
// timeline, tier/service bitmaps). CXO fills the WHOLE Root before
// OnRootFilled fires, and under fleet load the delivering inbound conn
// dies mid-fill — measured live, only ~2-transport trees finish in the
// conn's window, so any visor with a real transport set (median 5, up to
// hundreds) never lands its discovery data and TPD reflected ~9% of the
// graph. But CXO fills top-down and keeps whatever objects it fetched
// (closeFiller does not roll back the store), so the small top-level
// transports/list leaf is usually already present when the fill breaks
// on a deep telemetry object. Extract and dispatch JUST that leaf so
// discovery lands even when the full telemetry fill can't complete;
// telemetry self-heals on a later fill that does finish.
//
// The redis reconcile runs on a detached goroutine so a slow store write
// can't stall the CXO node's event loop (this callback fires on it, and
// under mass breaks it fires hundreds of times per reconcile window).
func (a *Aggregator) recoverTransportListOnBreak(r *registry.Root) {
	if r == nil || len(r.Refs) == 0 {
		return
	}
	reporter := cipher.PubKey(r.Pub)
	if reporter == (cipher.PubKey{}) {
		return
	}
	pack, err := a.cxoNode.Container().Pack(r, treestore.Registry)
	if err != nil {
		return
	}
	var rootNode treestore.TreeNode
	if err := r.Refs[0].Value(pack, &rootNode); err != nil {
		return // root node object itself wasn't fetched before the break
	}
	// Prefer the top-level "tp-list" leaf (current visors): it lives in the
	// Root's small first child-index page, so a truncated fill almost always
	// has it. Fall back to the legacy nested "transports"/"list" for visors
	// that predate the top-level move.
	leaf, dispatchPath, ok := findDiscoveryLeaf(pack, &rootNode)
	if !ok || leaf == nil {
		return
	}
	a.log.WithField("visor", reporter).WithField("path", dispatchPath).
		Debug("CXO aggregator: recovered transport list from partial fill")
	leafCopy := append([]byte(nil), leaf...)
	go a.dispatchLeaf(dispatchPath, leafCopy, reporter)
}

// reconcileTargeted decouples discovery from the whole-Root telemetry fill.
//
// On every OnRootReceived it does a BOUNDED, TARGETED fetch of just the
// tp-list leaf path — registry, root TreeNode, the small top-level Children
// index page(s), and the tp-list TreeEntry — over the delivering conn, and
// reconciles the reporter's full transport set immediately. This is
// INDEPENDENT of the 90s MaxFillingTime whole-Root fill and of
// OnFillingBreaks: the telemetry subtree fill can still run and fail as
// before; discovery no longer waits on it. Because the tp-list snapshot is a
// single inlined TreeEntry, landing that handful of path objects lands the
// ENTIRE transport list — where the whole-tree fill of a busy visor's
// hundreds of telemetry objects can't complete in the short-lived conn's
// window (the ~10% discovery gap this fixes).
//
// Objects are read through a Preview pack: found in the aggregator's CXO
// store if the concurrent fill already fetched them, else requested from the
// remote peer on demand and held in memory only — never written to the store,
// so this neither competes with nor disturbs the whole-Root fill or the
// cleanup sweep.
//
// Idempotent vs the OnRootFilled path: both call the declarative
// ReconcileTransportsFromCXO (register-all + deregister-absent), so if the
// whole-Root fill also later completes it simply re-applies the same set.
//
// Runs on a detached goroutine (the per-object requests block on the network)
// and is coalesced per feed so a rapidly republishing visor can't pile up
// concurrent fetches.
func (a *Aggregator) reconcileTargeted(conn *node.Conn, r *registry.Root) {
	if conn == nil || r == nil || len(r.Refs) == 0 {
		return
	}
	reporter := cipher.PubKey(r.Pub)
	// A root with no publisher identity can't be attributed to a visor —
	// same guard as handleRootFilled, so a zero-PK root never writes under
	// the null key.
	if reporter == (cipher.PubKey{}) {
		return
	}

	// Coalesce: one in-flight targeted fetch per feed. A visor that
	// republishes rapidly (or delivers the same Root on several conns)
	// otherwise spawns a goroutine + per-object request burst each time.
	a.mu.Lock()
	if a.fetching == nil {
		a.fetching = make(map[skycipher.PubKey]struct{})
	}
	if _, busy := a.fetching[r.Pub]; busy {
		a.mu.Unlock()
		return
	}
	a.fetching[r.Pub] = struct{}{}
	a.mu.Unlock()

	go func() {
		defer func() {
			a.mu.Lock()
			delete(a.fetching, r.Pub)
			a.mu.Unlock()
		}()

		entries, version, ok := a.fetchDiscoveryLeaf(conn, r)
		if ok {
			// Fresh full snapshot: cache it (so a later failed fetch can
			// re-apply it) and reconcile.
			a.mu.Lock()
			a.lastList[r.Pub] = cachedList{entries: entries, version: version}
			a.mu.Unlock()
			a.applyReconcile(entries, reporter, version)
			return
		}
		// Fetch failed (conn closed mid-fetch, leaf absent, or budget hit):
		// re-apply the last good snapshot if we have one so the reporter's
		// transports survive the redis TTL until a later Root lands fresh.
		// Gated on receiving a Root, so a truly-gone visor (no more Roots)
		// stops being refreshed and its transports expire normally.
		a.mu.Lock()
		cached, has := a.lastList[r.Pub]
		a.mu.Unlock()
		if has {
			a.applyReconcile(cached.entries, reporter, cached.version)
		}
	}()
}

// fetchDiscoveryLeaf builds a Preview pack over conn (remote on-demand,
// in-memory object fetch) and walks JUST the tp-list path with the same
// partial-tolerant helpers the OnFillingBreaks recovery uses. Fetching is
// bounded by object count (maxTargetedFetchObjects) and wall clock
// (targetedFetchTimeout) via boundedGetter, so a huge or hostile Root can't
// wedge the fetch. Returns the reconstructed full entry set and true iff the
// leaf was present and decoded cleanly.
func (a *Aggregator) fetchDiscoveryLeaf(conn *node.Conn, r *registry.Root) (entries []*transport.Entry, version string, ok bool) {
	return a.fetchDiscoveryLeafWithGetter(conn.Getter(), r)
}

// fetchDiscoveryLeafWithGetter is the getter-parameterized core of
// fetchDiscoveryLeaf, split out so the targeted walk/decode can be exercised
// against a synthetic Getter (no live conn) in tests.
func (a *Aggregator) fetchDiscoveryLeafWithGetter(g skyobject.Getter, r *registry.Root) (entries []*transport.Entry, version string, ok bool) {
	reporter := cipher.PubKey(r.Pub)
	bg := newBoundedGetter(g, maxTargetedFetchObjects, targetedFetchTimeout)
	pack, err := a.cxoNode.Container().Preview(r, bg)
	if err != nil {
		a.log.WithError(err).WithField("visor", reporter).
			Debug("CXO aggregator: targeted preview pack failed")
		return nil, "", false
	}
	var rootNode treestore.TreeNode
	if err := r.Refs[0].Value(pack, &rootNode); err != nil {
		a.log.WithError(err).WithField("visor", reporter).
			Debug("CXO aggregator: targeted root node fetch failed")
		return nil, "", false
	}
	leaf, path, found := findDiscoveryLeaf(pack, &rootNode)
	if !found || leaf == nil {
		return nil, "", false
	}
	leaf = cxoutils.Gunzip(leaf) // publisher may gzip; raw bodies pass through
	var list transportListLeaf
	if err := json.Unmarshal(leaf, &list); err != nil {
		a.log.WithError(err).WithField("visor", reporter).WithField("path", path).
			Debug("CXO aggregator: targeted transport list decode failed")
		return nil, "", false
	}
	a.log.WithField("visor", reporter).WithField("path", path).
		Debug("CXO aggregator: targeted discovery-leaf fetch landed transport list")
	return list.entries(reporter), list.Version, true
}

// applyReconcile hands a reporter's full transport set to the sink. Shared by
// the fresh targeted fetch and the cached re-apply; the sink call is
// declarative (register-all + deregister-absent) so repeated application is a
// no-op.
func (a *Aggregator) applyReconcile(entries []*transport.Entry, reporter cipher.PubKey, version string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.sink.ReconcileTransportsFromCXO(ctx, entries, reporter, version); err != nil {
		a.log.WithError(err).WithField("reporter", reporter).
			Debug("CXO aggregator: targeted ReconcileTransportsFromCXO failed")
	}
}

// boundedGetter wraps a skyobject.Getter with an object-count cap and a
// wall-clock deadline. It bounds the targeted discovery-leaf fetch so a Root
// with a hostile or oversized top-level index can't drive unbounded
// per-object requests to a peer or pin the fetch goroutine. Once either bound
// is exceeded every subsequent Get returns errTargetedFetchBudget, which
// unwinds the treestore walk cleanly (childByName treats an unreadable child
// as absent) rather than hanging.
type boundedGetter struct {
	inner    skyobject.Getter
	max      int
	deadline time.Time

	mu    sync.Mutex
	count int
}

func newBoundedGetter(inner skyobject.Getter, max int, within time.Duration) *boundedGetter {
	return &boundedGetter{inner: inner, max: max, deadline: time.Now().Add(within)}
}

// Get satisfies skyobject.Getter.
func (b *boundedGetter) Get(key skycipher.SHA256) (val []byte, err error) {
	b.mu.Lock()
	if b.count >= b.max || time.Now().After(b.deadline) {
		b.mu.Unlock()
		return nil, errTargetedFetchBudget
	}
	b.count++
	b.mu.Unlock()
	return b.inner.Get(key)
}

// findDiscoveryLeaf locates the transport-list snapshot leaf in a (possibly
// partially filled) Root tree, trying the current top-level "tp-list" path
// first and the legacy nested "transports"/"list" second. Returns the inline
// leaf bytes and the dispatch path to hand to dispatchLeaf.
func findDiscoveryLeaf(pack registry.Pack, rootNode *treestore.TreeNode) (leaf []byte, path string, ok bool) {
	if l, _, found := childByName(pack, rootNode, tpdListLeafName); found && l != nil {
		return l, tpdListLeafName, true
	}
	if _, tsub, found := childByName(pack, rootNode, "transports"); found && tsub != nil {
		if l, _, found := childByName(pack, tsub, "list"); found && l != nil {
			return l, "transports/list", true
		}
	}
	return nil, "", false
}

// childByName returns the inline leaf bytes or the sub-node of n's child
// with the given name. It tolerates objects a partial fill never fetched:
// an unreadable child (its object absent from the store) simply yields
// ok=false rather than erroring the whole walk. Exactly one of leaf/sub
// is non-nil on ok=true.
func childByName(pack registry.Pack, n *treestore.TreeNode, name string) (leaf []byte, sub *treestore.TreeNode, ok bool) {
	count, err := n.Children.Len(pack)
	if err != nil {
		return nil, nil, false
	}
	for i := 0; i < count; i++ {
		var entry treestore.TreeEntry
		if _, err := n.Children.ValueByIndex(pack, i, &entry); err != nil {
			continue // this child's index object wasn't fetched; skip it
		}
		if entry.Name != name {
			continue
		}
		if len(entry.Leaf) > 0 {
			return entry.Leaf, nil, true
		}
		if entry.Sub.Hash != (skycipher.SHA256{}) {
			var s treestore.TreeNode
			if err := entry.Sub.Value(pack, &s); err != nil {
				return nil, nil, false // sub-node object not fetched
			}
			return nil, &s, true
		}
		return nil, nil, false
	}
	return nil, nil, false
}

// walkAndDispatch recursively descends a TreeStore tree and routes
// recognized leaf paths to BandwidthSink. Unrecognized paths are
// held in CXO's local cache and ignored — daily transport rollups,
// tier bitmaps, and service bitmaps are received but not yet
// dispatched anywhere; routing them to redis is a follow-up.
func (a *Aggregator) walkAndDispatch(pack registry.Pack, n *treestore.TreeNode, basePath string, reporter cipher.PubKey) {
	count, err := n.Children.Len(pack)
	if err != nil {
		return
	}
	for i := 0; i < count; i++ {
		var entry treestore.TreeEntry
		if _, err := n.Children.ValueByIndex(pack, i, &entry); err != nil {
			continue
		}
		fullPath := entry.Name
		if basePath != "" {
			fullPath = basePath + "/" + entry.Name
		}
		if len(entry.Leaf) > 0 {
			a.dispatchLeaf(fullPath, entry.Leaf, reporter)
			continue
		}
		if entry.Sub.Hash != (skycipher.SHA256{}) {
			var sub treestore.TreeNode
			if err := entry.Sub.Value(pack, &sub); err == nil {
				a.walkAndDispatch(pack, &sub, fullPath, reporter)
			}
		}
	}
}

// dispatchLeaf is the path → action dispatcher. Recognized leaf
// shapes:
//
//	transports/<uuid>/entry               → register (metadata)
//	transports/<uuid>/tombstone           → deregister
//	transports/<uuid>/current             → bandwidth + latency + heartbeat
//	transports/<uuid>/<YYYY-MM-DD>/timeline → per-transport uptime bitmap
//
// Tier and service bitmaps still flow through the CXO cache but
// aren't yet projected into TPD's redis uptime tables.
func (a *Aggregator) dispatchLeaf(path string, leaf []byte, reporter cipher.PubKey) {
	// Publishers may gzip leaf bodies (bandwidth/storage parity with the
	// HTTP path). Gunzip is self-describing — it magic-byte-detects gzip and
	// passes raw bodies through unchanged — so this reads both compressed and
	// uncompressed feeds, which is what lets a visor switch to gzip without
	// breaking a TPD that predates it (deploy this reader first).
	leaf = cxoutils.Gunzip(leaf)
	// tp-list (current) / transports/list (legacy) — the full-set snapshot
	// (declarative CRUD). Reconcile the reporter's transport set against it
	// (register new + deregister absent).
	if path == tpdListLeafName || path == "transports/list" {
		var list transportListLeaf
		if err := json.Unmarshal(leaf, &list); err != nil {
			a.log.WithError(err).WithField("path", path).Debug("CXO aggregator: transport list leaf decode failed")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.sink.ReconcileTransportsFromCXO(ctx, list.entries(reporter), reporter, list.Version); err != nil {
			a.log.WithError(err).WithField("reporter", reporter).Debug("CXO aggregator: ReconcileTransportsFromCXO failed")
		}
		return
	}
	if tpID, date, ok := parseTransportTimelinePath(path); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.sink.IngestTransportTimeline(ctx, tpID, date, leaf); err != nil {
			a.log.WithError(err).WithField("transport", tpID).WithField("date", date).
				Debug("CXO aggregator: IngestTransportTimeline failed")
		}
		return
	}
	if tpID, ok := parseTransportEntryPath(path); ok {
		var leafEntry transportEntryLeaf
		if err := json.Unmarshal(leaf, &leafEntry); err != nil {
			a.log.WithError(err).WithField("path", path).Debug("CXO aggregator: entry leaf decode failed")
			return
		}
		// The leaf carries the entry; the path carries the UUID. They
		// must agree — a mismatch is either a publisher bug or
		// tampering and we drop it.
		if leafEntry.Entry == nil || leafEntry.Entry.ID != tpID {
			a.log.WithField("path", path).Debug("CXO aggregator: entry leaf id mismatch")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.sink.RegisterTransportFromCXO(ctx, leafEntry.Entry, reporter, leafEntry.Version); err != nil {
			a.log.WithError(err).WithField("transport", tpID).
				Debug("CXO aggregator: RegisterTransportFromCXO failed")
		}
		return
	}
	if tpID, ok := parseTransportTombstonePath(path); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.sink.DeregisterTransportFromCXO(ctx, tpID, reporter); err != nil {
			a.log.WithError(err).WithField("transport", tpID).
				Debug("CXO aggregator: DeregisterTransportFromCXO failed")
		}
		return
	}
	id, ok := parseCurrentTransportPath(path)
	if !ok {
		return
	}
	var snap liveSnapshot
	if err := json.Unmarshal(leaf, &snap); err != nil {
		a.log.WithError(err).WithField("path", path).Debug("CXO aggregator: live snapshot decode failed")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.sink.UpdateBandwidth(ctx, id.String(), reporter, snap.SentBytes, snap.RecvBytes); err != nil {
		a.log.WithError(err).WithField("transport", id).Debug("CXO aggregator: UpdateBandwidth failed")
	}
	// Both edges of a transport publish their own snapshot, and TPD's
	// store is last-writer-wins. Require all three fields to be > 0 so
	// a partial-zero snapshot from an edge whose probe never completed
	// can't clobber a good record written by the other edge.
	if snap.LatencyMinMS > 0 && snap.LatencyMaxMS > 0 && snap.LatencyAvgMS > 0 {
		if err := a.sink.UpdateLatency(ctx, id.String(), snap.LatencyMinMS, snap.LatencyMaxMS, snap.LatencyAvgMS); err != nil {
			a.log.WithError(err).WithField("transport", id).Debug("CXO aggregator: UpdateLatency failed")
		}
	}
	// Heartbeat into the per-transport uptime tables (tp-uptime:*),
	// previously written only by the HTTP /transports/ register path.
	// Type is empty on snapshots from pre-uptime visors — skip those
	// rather than push a heartbeat the store would have to drop on
	// the type filter (RecordTransportHeartbeat early-returns on any
	// non-p2p type, but routing here saves the redis round-trip).
	if snap.Type != "" {
		if err := a.sink.RecordTransportHeartbeat(ctx, id, snap.Type, snap.SampledAt); err != nil {
			a.log.WithError(err).WithField("transport", id).Debug("CXO aggregator: RecordTransportHeartbeat failed")
		}
	}
}

// parseTransportTimelinePath returns the transport UUID and date for
// paths shaped "transports/<uuid>/<YYYY-MM-DD>/timeline", or false
// otherwise. Date format is validated as fixed-width 10 chars with
// dashes at positions 4 and 7 — same shape MarshalBinary on a UTC
// date emits via time.Format("2006-01-02").
func parseTransportTimelinePath(path string) (uuid.UUID, string, bool) {
	const prefix = "transports/"
	const suffix = "/timeline"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return uuid.UUID{}, "", false
	}
	mid := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	parts := strings.Split(mid, "/")
	if len(parts) != 2 {
		return uuid.UUID{}, "", false
	}
	id, err := uuid.Parse(parts[0])
	if err != nil {
		return uuid.UUID{}, "", false
	}
	date := parts[1]
	if len(date) != len("2006-01-02") || date[4] != '-' || date[7] != '-' {
		return uuid.UUID{}, "", false
	}
	for i, c := range date {
		if i == 4 || i == 7 {
			continue
		}
		if c < '0' || c > '9' {
			return uuid.UUID{}, "", false
		}
	}
	return id, date, true
}

// parseCurrentTransportPath returns the transport UUID for paths
// shaped "transports/<uuid>/current", or false otherwise.
func parseCurrentTransportPath(path string) (uuid.UUID, bool) {
	return parseTransportLeafByName(path, "/current")
}

// parseTransportEntryPath returns the transport UUID for paths shaped
// "transports/<uuid>/entry", or false otherwise.
func parseTransportEntryPath(path string) (uuid.UUID, bool) {
	return parseTransportLeafByName(path, "/entry")
}

// parseTransportTombstonePath returns the transport UUID for paths
// shaped "transports/<uuid>/tombstone", or false otherwise.
func parseTransportTombstonePath(path string) (uuid.UUID, bool) {
	return parseTransportLeafByName(path, "/tombstone")
}

// parseTransportLeafByName extracts the transport UUID from any path
// shaped "transports/<uuid><suffix>" with no extra slashes between.
func parseTransportLeafByName(path, suffix string) (uuid.UUID, bool) {
	const prefix = "transports/"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return uuid.UUID{}, false
	}
	mid := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	if strings.Contains(mid, "/") {
		return uuid.UUID{}, false
	}
	id, err := uuid.Parse(mid)
	if err != nil {
		return uuid.UUID{}, false
	}
	return id, true
}
