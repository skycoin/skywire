package cxoaggregator

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/node"
	"github.com/skycoin/skywire/pkg/cxo/skyobject"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// newInMemNode builds a CXO Node with an in-memory container and no
// network listeners — sufficient for building a Root (publisher side) or
// hosting an empty container (aggregator side) in unit tests.
func newInMemNode(t *testing.T) *node.Node {
	t.Helper()
	cfg := node.NewConfig()
	cfg.TCP.Listen = ""
	cfg.UDP.Listen = ""
	cfg.RPC = ""
	cfg.Config.InMemoryDB = true
	n, err := node.NewNode(cfg)
	if err != nil {
		t.Fatalf("NewNode: %v", err)
	}
	t.Cleanup(func() {
		if err := n.Close(); err != nil {
			t.Logf("node close: %v", err)
		}
	})
	return n
}

// buildBigRoot publishes a Root that mirrors a busy visor's shape: a
// single top-level "tp-list" discovery leaf carrying nTransports compact
// entries, PLUS a bulky per-transport telemetry subtree
// (transports/<uuid>/current) whose whole-fill is exactly what can't
// complete over a short-lived conn in production. Returns the publisher's
// container (source of objects), the latest Root, and the reporter PK.
func buildBigRoot(t *testing.T, nTransports, nTelemetry int) (*skyobject.Container, *registry.Root, cipher.PubKey) {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	pub, err := treestore.New(newInMemNode(t), sk, treestore.Config{BatchWindow: 5 * time.Millisecond})
	if err != nil {
		t.Fatalf("treestore.New: %v", err)
	}
	t.Cleanup(func() {
		if err := pub.Close(); err != nil {
			t.Logf("publisher close: %v", err)
		}
	})

	// tp-list: the full transport set as one inlined snapshot leaf, compact form.
	compact := make([]transport.CompactEntry, 0, nTransports)
	for i := 0; i < nTransports; i++ {
		remote, _ := cipher.GenerateKeyPair()
		compact = append(compact, transport.CompactEntry{Remote: remote, Type: types.Type("stcpr")})
	}
	leaf, err := json.Marshal(transportListLeaf{Version: "v-test", Compact: compact})
	if err != nil {
		t.Fatalf("marshal tp-list: %v", err)
	}
	if err := pub.Put(tpdListLeafName, leaf); err != nil {
		t.Fatalf("Put tp-list: %v", err)
	}

	// A bulky telemetry subtree: this is the part whose whole-Root fill
	// times out in production. The targeted fetch must NOT need any of it.
	for i := 0; i < nTelemetry; i++ {
		snap, err := json.Marshal(liveSnapshot{SentBytes: uint64(i), RecvBytes: uint64(i), SampledAt: time.Now().UTC(), Type: "stcpr"})
		if err != nil {
			t.Fatalf("marshal telemetry: %v", err)
		}
		path := fmt.Sprintf("transports/%040x-telemetry-%d/current", i, i)
		if err := pub.Put(path, snap); err != nil {
			t.Fatalf("Put telemetry: %v", err)
		}
	}

	if err := pub.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	c := pub.Node().Container()
	skyPK := skycipher.PubKey(pk)
	nonce := c.ActiveHead(skyPK)
	if nonce == 0 {
		nonce = 1
	}
	r, err := c.LastRoot(skyPK, nonce)
	if err != nil {
		t.Fatalf("LastRoot: %v", err)
	}
	if r == nil || len(r.Refs) == 0 {
		t.Fatal("expected a non-empty Root after Flush")
	}
	return c, r, pk
}

// servingGetter serves objects out of a source container by hash and
// records every key requested. It optionally restricts service to an
// allow-set of keys (to simulate a fill that can only fetch the tp-list
// path, not the bulky telemetry), returning errNotServed otherwise.
type servingGetter struct {
	src       *skyobject.Container
	allow     map[skycipher.SHA256]struct{} // nil => serve anything present
	requested []skycipher.SHA256
}

var errNotServed = errors.New("object not served (simulated missing telemetry)")

func (g *servingGetter) Get(key skycipher.SHA256) ([]byte, error) {
	g.requested = append(g.requested, key)
	if g.allow != nil {
		if _, ok := g.allow[key]; !ok {
			return nil, errNotServed
		}
	}
	if v, _, err := g.src.Get(key, 0); err == nil {
		return v, nil
	}
	// The registry body is not a plain CXDS object; serve the fixed
	// treestore registry by its reference.
	if key == skycipher.SHA256(treestore.Registry.Reference()) {
		return treestore.Registry.Encode(), nil
	}
	return nil, errNotServed
}

// TestTargetedFetchLandsFullListDespiteTelemetry is the core proof: a Root
// whose whole-fill would time out (a large telemetry subtree) but whose
// tp-list leaf is present reconciles ALL of its transports via the targeted
// path — not the ~10% a truncated whole-Root fill lands. It further proves
// the fetch is BOUNDED to the tp-list path: replaying it against a getter
// that serves ONLY the path objects (telemetry withheld) still lands the
// full list.
func TestTargetedFetchLandsFullListDespiteTelemetry(t *testing.T) {
	const nTransports = 200
	const nTelemetry = 200
	src, r, reporter := buildBigRoot(t, nTransports, nTelemetry)

	sink := &recordingSink{}
	a := &Aggregator{
		cxoNode:  newInMemNode(t),
		sink:     sink,
		log:      logging.MustGetLogger("test"),
		lastList: make(map[skycipher.PubKey]cachedList),
		fetching: make(map[skycipher.PubKey]struct{}),
	}

	// Phase 1: full getter serves everything. The targeted walk fetches
	// only the objects it touches; record that set.
	full := &servingGetter{src: src}
	entries, version, ok := a.fetchDiscoveryLeafWithGetter(full, r)
	if !ok {
		t.Fatal("phase 1: targeted fetch did not land the tp-list leaf")
	}
	if version != "v-test" {
		t.Errorf("phase 1: version = %q, want v-test", version)
	}
	if len(entries) != nTransports {
		t.Fatalf("phase 1: reconciled %d transports, want all %d", len(entries), nTransports)
	}

	// The path is a handful of objects — far fewer than the telemetry tree,
	// and within the per-fetch budget. This is what lets it land over a
	// conn that a whole-tree fill can't finish.
	if len(full.requested) >= maxTargetedFetchObjects {
		t.Fatalf("phase 1: targeted fetch requested %d objects (>= budget %d) — not bounded to the path",
			len(full.requested), maxTargetedFetchObjects)
	}
	if len(full.requested) > nTelemetry {
		t.Fatalf("phase 1: targeted fetch requested %d objects — it is walking the telemetry subtree",
			len(full.requested))
	}

	// Phase 2: restrict the getter to ONLY the objects the path touched;
	// every telemetry object now errors (simulating the fill that can't
	// complete). The full list must STILL land.
	allow := make(map[skycipher.SHA256]struct{}, len(full.requested))
	for _, k := range full.requested {
		allow[k] = struct{}{}
	}
	restricted := &servingGetter{src: src, allow: allow}
	a2 := &Aggregator{
		cxoNode:  newInMemNode(t),
		sink:     sink,
		log:      logging.MustGetLogger("test"),
		lastList: make(map[skycipher.PubKey]cachedList),
		fetching: make(map[skycipher.PubKey]struct{}),
	}
	entries2, _, ok2 := a2.fetchDiscoveryLeafWithGetter(restricted, r)
	if !ok2 {
		t.Fatal("phase 2: targeted fetch failed with telemetry withheld — it depends on the whole-Root fill")
	}
	if len(entries2) != nTransports {
		t.Fatalf("phase 2: reconciled %d transports with telemetry withheld, want all %d", len(entries2), nTransports)
	}

	// applyReconcile hands the full set to the sink declaratively.
	a.applyReconcile(entries, reporter, version)
	if len(sink.reconciles) != 1 {
		t.Fatalf("expected 1 ReconcileTransportsFromCXO call, got %d", len(sink.reconciles))
	}
	if got := len(sink.reconciles[0].entries); got != nTransports {
		t.Fatalf("sink reconcile received %d entries, want %d", got, nTransports)
	}
	if sink.reconciles[0].reporter != reporter {
		t.Fatalf("sink reconcile reporter = %v, want %v", sink.reconciles[0].reporter, reporter)
	}
}

// TestTargetedFetchMissingLeafCleanMiss: a Root with NO tp-list leaf must
// return ok=false cleanly (no panic, no partial), so the caller falls back
// to the cached snapshot rather than reconciling an empty set.
func TestTargetedFetchMissingLeafCleanMiss(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	_ = pk
	pub, err := treestore.New(newInMemNode(t), sk, treestore.Config{BatchWindow: 5 * time.Millisecond})
	if err != nil {
		t.Fatalf("treestore.New: %v", err)
	}
	t.Cleanup(func() {
		if err := pub.Close(); err != nil {
			t.Logf("publisher close: %v", err)
		}
	})
	// Only telemetry, no tp-list leaf.
	if err := pub.Put("transports/deadbeef/current", []byte(`{"sent_bytes":1,"recv_bytes":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := pub.Flush(); err != nil {
		t.Fatal(err)
	}
	c := pub.Node().Container()
	skyPK := skycipher.PubKey(pk)
	nonce := c.ActiveHead(skyPK)
	if nonce == 0 {
		nonce = 1
	}
	r, err := c.LastRoot(skyPK, nonce)
	if err != nil {
		t.Fatal(err)
	}

	a := &Aggregator{
		cxoNode:  newInMemNode(t),
		sink:     &recordingSink{},
		log:      logging.MustGetLogger("test"),
		lastList: make(map[skycipher.PubKey]cachedList),
		fetching: make(map[skycipher.PubKey]struct{}),
	}
	full := &servingGetter{src: c}
	if _, _, ok := a.fetchDiscoveryLeafWithGetter(full, r); ok {
		t.Fatal("expected ok=false for a Root without a tp-list leaf")
	}
}

// TestBoundedGetterBudget checks the object-count and deadline caps: once
// exceeded every Get errors (so a hostile/oversized Root can't drive
// unbounded requests or hang the walk).
func TestBoundedGetterBudget(t *testing.T) {
	// count cap
	served := 0
	inner := getterFunc(func(_ skycipher.SHA256) ([]byte, error) { served++; return []byte("x"), nil })
	bg := newBoundedGetter(inner, 3, time.Minute)
	for i := 0; i < 3; i++ {
		if _, err := bg.Get(skycipher.SHA256{}); err != nil {
			t.Fatalf("get %d before cap should succeed: %v", i, err)
		}
	}
	if _, err := bg.Get(skycipher.SHA256{}); !errors.Is(err, errTargetedFetchBudget) {
		t.Fatalf("get past count cap: err = %v, want budget error", err)
	}

	// deadline cap
	bg2 := newBoundedGetter(inner, 1000, -1*time.Second) // already expired
	if _, err := bg2.Get(skycipher.SHA256{}); !errors.Is(err, errTargetedFetchBudget) {
		t.Fatalf("get past deadline: err = %v, want budget error", err)
	}
}

type getterFunc func(skycipher.SHA256) ([]byte, error)

func (f getterFunc) Get(key skycipher.SHA256) ([]byte, error) { return f(key) }

// walkAllObjects recursively reads every TreeEntry in the Root's tree
// through a Preview pack backed by g, forcing g to serve (and record)
// every object the whole tree references — i.e. the full object footprint
// a subscriber must fill to receive the feed. Returns the count of
// DISTINCT object hashes requested.
func walkAllObjects(t *testing.T, aggNode *node.Node, g *servingGetter, r *registry.Root) int {
	t.Helper()
	pack, err := aggNode.Container().Preview(r, g)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	var rootNode treestore.TreeNode
	if err := r.Refs[0].Value(pack, &rootNode); err != nil {
		t.Fatalf("root TreeNode: %v", err)
	}
	var walk func(n *treestore.TreeNode)
	walk = func(n *treestore.TreeNode) {
		count, err := n.Children.Len(pack)
		if err != nil {
			return
		}
		for i := 0; i < count; i++ {
			var entry treestore.TreeEntry
			if _, err := n.Children.ValueByIndex(pack, i, &entry); err != nil {
				continue
			}
			if entry.Sub.Hash != (skycipher.SHA256{}) {
				var sub treestore.TreeNode
				if err := entry.Sub.Value(pack, &sub); err == nil {
					walk(&sub)
				}
			}
		}
	}
	walk(&rootNode)
	distinct := make(map[skycipher.SHA256]struct{}, len(g.requested))
	for _, k := range g.requested {
		distinct[k] = struct{}{}
	}
	return len(distinct)
}

// TestDedicatedTPListFeedFillsCompletely is the structural proof behind
// moving the tp-list onto its OWN CXO feed (skyenv.DmsgVisorTPListCXOPort):
// a feed that carries ONLY the compact transport-list snapshot leaf (no
// telemetry) has a tiny object footprint that is INDEPENDENT of the
// transport count, so its whole Root fills in ~1 round-trip — where the
// combined telemetry Root's footprint scales with the transport count and
// routinely can't finish its fill over the short announce conn on a busy
// hub (the ~10% under-report this fixes). Both are built from the SAME
// buildBigRoot helper; the only difference is whether the telemetry subtree
// rides along.
func TestDedicatedTPListFeedFillsCompletely(t *testing.T) {
	const nTransports = 500

	// Dedicated feed: tp-list only, NO telemetry — the shape published on
	// DmsgVisorTPListCXOPort.
	dedSrc, dedRoot, _ := buildBigRoot(t, nTransports, 0)
	dedObjs := walkAllObjects(t, newInMemNode(t), &servingGetter{src: dedSrc}, dedRoot)

	// Combined feed: same tp-list PLUS a bulky telemetry tree — the shape on
	// DmsgCXOPort whose whole-Root fill breaks on a busy hub.
	combSrc, combRoot, _ := buildBigRoot(t, nTransports, 3000)
	combObjs := walkAllObjects(t, newInMemNode(t), &servingGetter{src: combSrc}, combRoot)

	t.Logf("Root object footprint at %d transports: dedicated tp-list feed = %d objects, combined telemetry feed (3000 leaves) = %d objects",
		nTransports, dedObjs, combObjs)

	// The dedicated Root's footprint is a handful of objects and does not
	// grow with the transport count (the tp-list is one inlined leaf), so it
	// fits well inside a single fill. The combined Root drags in an object
	// per telemetry leaf.
	if dedObjs > maxTargetedFetchObjects {
		t.Fatalf("dedicated feed footprint = %d objects (> %d): not a one-round-trip fill",
			dedObjs, maxTargetedFetchObjects)
	}
	if combObjs <= dedObjs*10 {
		t.Fatalf("combined feed footprint = %d vs dedicated %d: expected the telemetry tree to dominate",
			combObjs, dedObjs)
	}

	// The dedicated feed lands ALL transports: serving ONLY the objects its
	// whole tree references (i.e. the entire dedicated feed, telemetry
	// absent by construction) still reconciles the full list.
	a := &Aggregator{
		cxoNode:  newInMemNode(t),
		sink:     &recordingSink{},
		log:      logging.MustGetLogger("test"),
		lastList: make(map[skycipher.PubKey]cachedList),
		fetching: make(map[skycipher.PubKey]struct{}),
	}
	entries, version, ok := a.fetchDiscoveryLeafWithGetter(&servingGetter{src: dedSrc}, dedRoot)
	if !ok {
		t.Fatal("dedicated feed: discovery leaf did not land")
	}
	if version != "v-test" {
		t.Errorf("dedicated feed: version = %q, want v-test", version)
	}
	if len(entries) != nTransports {
		t.Fatalf("dedicated feed reconciled %d transports, want all %d", len(entries), nTransports)
	}
}
