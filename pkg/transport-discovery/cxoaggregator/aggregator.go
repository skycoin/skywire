// Package cxoaggregator runs a CXO node on the Transport Discovery
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
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/node"
	cxotransport "github.com/skycoin/skywire/pkg/cxo/node/transport"
	"github.com/skycoin/skywire/pkg/cxo/skyobject"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// Sink receives per-transport telemetry updates from visor TreeStore
// feeds. The TPD's redis store satisfies this via UpdateBandwidth (per
// reporter, cumulative counters) and UpdateLatency (per transport, RTT
// min/max/avg in ms).
type Sink interface {
	UpdateBandwidth(ctx context.Context, transportID string, reporterPK cipher.PubKey, sent, recv uint64) error
	UpdateLatency(ctx context.Context, transportID string, minMS, maxMS, avgMS float64) error
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
}

// Config configures the Aggregator.
type Config struct {
	// ReconcileInterval is how often the aggregator re-scans the
	// CXO node's connection list and ensures each is subscribed to
	// the remote PK's feed. Defaults to 30s — small enough that a
	// fresh visor conn doesn't wait too long to see updates flow.
	ReconcileInterval time.Duration
	// Logger overrides the default tagged logger.
	Logger *logging.Logger
	// InMemoryDB / DataDir control the aggregator's CXO storage.
	// Default is in-memory (subscribed object cache only — TPD's
	// authoritative store is redis, the CXO cache exists just to
	// satisfy CXO's filling protocol).
	InMemoryDB bool
	DataDir    string
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
}

// New constructs an Aggregator. Sets up a CXO Node, enables DMSG so
// remote visors can dial in, and wires OnRootFilled to walk
// TreeStore Roots and dispatch to sink.
func New(dmsgC *dmsg.Client, sink Sink, conf Config) (*Aggregator, error) {
	if conf.ReconcileInterval <= 0 {
		conf.ReconcileInterval = 30 * time.Second
	}
	if conf.Logger == nil {
		conf.Logger = logging.MustGetLogger("tpd-cxo-aggregator")
	}

	cfg := node.NewConfig()
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
	factory := cxotransport.NewDMSGFactory(dmsgC, cxotransport.DefaultCXOPort)
	if err := cxoNode.EnableDMSG(factory); err != nil {
		_ = cxoNode.Close() //nolint:errcheck
		return nil, err
	}

	a := &Aggregator{
		cxoNode: cxoNode,
		sink:    sink,
		conf:    conf,
		log:     conf.Logger,
		done:    make(chan struct{}),
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
	cxoNode.Config().OnRootReceived = func(_ *node.Conn, r *registry.Root) error {
		a.log.WithField("visor", cipher.PubKey(r.Pub)).WithField("seq", r.Nonce).
			Debug("CXO aggregator: root received")
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

	a.reconcile()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.reconcile()
		}
	}
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
	a.walkAndDispatch(pack, &rootNode, "", reporter)
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

// dispatchLeaf is the path → action dispatcher. Today it only
// recognizes transports/<uuid>/current; tier and service bitmaps
// arrive into the CXO cache but aren't yet written into TPD's redis
// uptime tables.
func (a *Aggregator) dispatchLeaf(path string, leaf []byte, reporter cipher.PubKey) {
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
	if snap.LatencyAvgMS > 0 {
		if err := a.sink.UpdateLatency(ctx, id.String(), snap.LatencyMinMS, snap.LatencyMaxMS, snap.LatencyAvgMS); err != nil {
			a.log.WithError(err).WithField("transport", id).Debug("CXO aggregator: UpdateLatency failed")
		}
	}
}

// parseCurrentTransportPath returns the transport UUID for paths
// shaped "transports/<uuid>/current", or false otherwise.
func parseCurrentTransportPath(path string) (uuid.UUID, bool) {
	const prefix = "transports/"
	const suffix = "/current"
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
