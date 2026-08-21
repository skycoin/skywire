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
	t.Cleanup(func() { _ = n.Close() })
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
	t.Cleanup(func() { _ = pub.Close() })

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
		snap, _ := json.Marshal(liveSnapshot{SentBytes: uint64(i), RecvBytes: uint64(i), SampledAt: time.Now().UTC(), Type: "stcpr"})
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
	t.Cleanup(func() { _ = pub.Close() })
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
