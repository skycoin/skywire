// Package api — cxo_publisher_batch_test.go proves the clients-by-server
// publisher batches to ONE leaf per server (object count O(#servers), not
// O(#pairs)) and that a batched leaf round-trips through the exported
// version-framed encoding.
package api

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

// newTestPub builds a publisher with only the in-memory state map wired,
// so stateSet / encodeClientsBatch can be exercised without a live
// treestore.Publisher (pub stays nil; these helpers never touch it).
func newTestPub() *ClientsByServerCXOPublisher {
	return &ClientsByServerCXOPublisher{
		state:        make(map[cipher.PubKey]map[cipher.PubKey][]byte),
		pendingDirty: make(map[cipher.PubKey]struct{}),
	}
}

func mustEntry(t *testing.T, clientPK cipher.PubKey, servers []cipher.PubKey) []byte {
	t.Helper()
	e := &disc.Entry{
		Version:    "0.0.1",
		Static:     clientPK,
		Client:     &disc.Client{DelegatedServers: servers},
		ClientType: "visor",
		Signature:  "sig",
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	return b
}

// TestBatchLeafCountIsPerServer feeds a large client population across a
// small server set and asserts the number of batch leaves (== non-empty
// servers in state) is bounded by #servers, NOT #(server,client) pairs.
func TestBatchLeafCountIsPerServer(t *testing.T) {
	p := newTestPub()

	const nServers = 8
	const nClients = 500
	servers := make([]cipher.PubKey, nServers)
	for i := range servers {
		servers[i], _ = cipher.GenerateKeyPair()
	}

	pairs := 0
	for i := 0; i < nClients; i++ {
		clientPK, _ := cipher.GenerateKeyPair()
		// Each client is delegated to 3 servers → 3 pairs.
		deleg := []cipher.PubKey{servers[i%nServers], servers[(i+1)%nServers], servers[(i+3)%nServers]}
		body := mustEntry(t, clientPK, deleg)
		for _, srv := range deleg {
			p.stateSet(srv, clientPK, body)
			pairs++
		}
	}

	// The OLD wire shape produced one leaf per pair; the batched shape
	// produces one leaf per server. Prove the collapse.
	if got := len(p.state); got > nServers {
		t.Fatalf("batch leaf count = %d, want <= %d servers", got, nServers)
	}
	if pairs <= nServers {
		t.Fatalf("test is trivial: pairs=%d not >> servers=%d", pairs, nServers)
	}
	t.Logf("pairs=%d collapsed to %d server leaves", pairs, len(p.state))

	// Every server leaf must decode back to exactly its client set.
	total := 0
	for srv, clients := range p.state {
		blob := encodeClientsBatch(clients)
		version, payload, ok := cxoutils.UnframeGzip(blob)
		if !ok || version != clientsByServerBatchVersion {
			t.Fatalf("unframe server %s: ok=%v version=%d", srv.Hex(), ok, version)
		}
		var entries []*disc.Entry
		if err := json.Unmarshal(payload, &entries); err != nil {
			t.Fatalf("decode server %s: %v", srv.Hex(), err)
		}
		if len(entries) != len(clients) {
			t.Fatalf("server %s: decoded %d entries, want %d", srv.Hex(), len(entries), len(clients))
		}
		total += len(entries)
	}
	if total != pairs {
		t.Fatalf("sum of decoded entries = %d, want %d pairs", total, pairs)
	}
}

// TestBatchEncodeDeterministic pins that an unchanged client set
// re-encodes to identical bytes (sorted by client PK), so a re-Put is a
// CXO wire no-op.
func TestBatchEncodeDeterministic(t *testing.T) {
	p := newTestPub()
	srv, _ := cipher.GenerateKeyPair()
	for i := 0; i < 20; i++ {
		clientPK, _ := cipher.GenerateKeyPair()
		p.stateSet(srv, clientPK, mustEntry(t, clientPK, []cipher.PubKey{srv}))
	}
	a := encodeClientsBatch(p.state[srv])
	b := encodeClientsBatch(p.state[srv])
	if string(a) != string(b) {
		t.Fatal("encodeClientsBatch not deterministic for an unchanged set")
	}
}

// TestBatchDelEmptiesServer proves a server with no clients left is
// dropped from state (its leaf would be Deleted, not left as a tombstone).
func TestBatchDelEmptiesServer(t *testing.T) {
	p := newTestPub()
	srv, _ := cipher.GenerateKeyPair()
	clientPK, _ := cipher.GenerateKeyPair()
	p.stateSet(srv, clientPK, mustEntry(t, clientPK, []cipher.PubKey{srv}))
	if len(p.state[srv]) != 1 {
		t.Fatalf("setup: want 1 client, got %d", len(p.state[srv]))
	}
	p.stateDel(srv, clientPK)
	if got := len(p.state[srv]); got != 0 {
		t.Fatalf("after del: want 0 clients, got %d", got)
	}
}

// TestMarkDirtyCoalesces proves the per-server re-encode is coalesced: repeated
// markDirty of the same server accumulates a de-duplicated pending set (so the
// flush ticker re-gzips each server at most once per window instead of per
// mutation), and flushDirty on an empty set is a safe no-op.
func TestMarkDirtyCoalesces(t *testing.T) {
	p := newTestPub()
	var s1, s2, s3 cipher.PubKey
	s1[0], s2[0], s3[0] = 1, 2, 3

	// Empty flush is a no-op (must not touch the nil publisher).
	p.flushDirty()
	if len(p.pendingDirty) != 0 {
		t.Fatalf("pendingDirty non-empty after empty flushDirty: %d", len(p.pendingDirty))
	}

	// Three mutations touching overlapping servers coalesce to the distinct set.
	p.markDirty(map[cipher.PubKey]struct{}{s1: {}, s2: {}})
	p.markDirty(map[cipher.PubKey]struct{}{s2: {}}) // s2 again — no duplicate
	p.markDirty(map[cipher.PubKey]struct{}{s3: {}})
	if len(p.pendingDirty) != 3 {
		t.Fatalf("pendingDirty = %d servers, want 3 (deduped)", len(p.pendingDirty))
	}
	for _, s := range []cipher.PubKey{s1, s2, s3} {
		if _, ok := p.pendingDirty[s]; !ok {
			t.Fatalf("server %x missing from pendingDirty", s[0])
		}
	}
}
