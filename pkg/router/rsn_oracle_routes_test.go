//go:build !tinygo || (js && wasm)

package router

import (
	"sort"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// mkLocal builds one of the source's own transports to peer over tpType.
func mkLocal(src, peer cipher.PubKey, tpType tptypes.Type) oracleLocalTp {
	return oracleLocalTp{
		id:       transport.MakeTransportID(src, peer, tpType),
		remotePK: peer,
		tpType:   tpType,
	}
}

// mkDstEntry builds a destination-side transport entry (dst<->peer).
func mkDstEntry(dst, peer cipher.PubKey, tpType tptypes.Type) *transport.Entry {
	e := transport.MakeEntry(dst, peer, tpType, transport.LabelAutomatic)
	return &e
}

func intermediatesOf(legs []twoHopLeg) []string {
	out := make([]string, 0, len(legs))
	for _, l := range legs {
		out = append(out, l.Intermediate.String())
	}
	sort.Strings(out)
	return out
}

func TestComputeDisjoint2HopRoutes_Intersection(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	i1, _ := cipher.GenerateKeyPair()
	i2, _ := cipher.GenerateKeyPair()
	i3, _ := cipher.GenerateKeyPair() // only src-side → excluded
	i4, _ := cipher.GenerateKeyPair() // only dst-side → excluded
	i5, _ := cipher.GenerateKeyPair() // DMSG on both → excluded

	localTps := []oracleLocalTp{
		mkLocal(src, i1, tptypes.STCPR),
		mkLocal(src, i2, tptypes.SUDPH),
		mkLocal(src, i3, tptypes.STCPR),
		mkLocal(src, i5, tptypes.DMSG),
	}
	dstEntries := []*transport.Entry{
		mkDstEntry(dst, i1, tptypes.STCPR),
		mkDstEntry(dst, i2, tptypes.STCPR),
		mkDstEntry(dst, i4, tptypes.STCPR),
		mkDstEntry(dst, i5, tptypes.DMSG),
	}

	legs, err := computeDisjoint2HopRoutes(src, dst, localTps, dstEntries, nil, 0)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	got := intermediatesOf(legs)
	want := []string{i1.String(), i2.String()}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("intermediates = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("intermediates = %v, want %v", got, want)
		}
	}

	// Verify hop shape for one leg (find i1's leg).
	var leg *twoHopLeg
	for idx := range legs {
		if legs[idx].Intermediate == i1 {
			leg = &legs[idx]
		}
	}
	if leg == nil {
		t.Fatal("expected a leg via i1")
	}
	if len(leg.Forward) != 2 {
		t.Fatalf("forward hops = %d, want 2", len(leg.Forward))
	}
	if leg.Forward[0].From != src || leg.Forward[0].To != i1 {
		t.Fatalf("forward[0] = %v->%v, want src->i1", leg.Forward[0].From, leg.Forward[0].To)
	}
	if leg.Forward[1].From != i1 || leg.Forward[1].To != dst {
		t.Fatalf("forward[1] = %v->%v, want i1->dst", leg.Forward[1].From, leg.Forward[1].To)
	}
	// The i1->dst transport ID must be the canonical (deterministic) ID both
	// endpoints agree on.
	wantID := transport.MakeTransportID(i1, dst, tptypes.STCPR)
	if leg.Forward[1].TpID != wantID {
		t.Fatalf("forward[1] TpID = %s, want canonical %s", leg.Forward[1].TpID, wantID)
	}
	// Reverse mirrors forward.
	if len(leg.Reverse) != 2 || leg.Reverse[0].From != dst || leg.Reverse[1].To != src {
		t.Fatalf("reverse hops malformed: %+v", leg.Reverse)
	}
}

func TestComputeDisjoint2HopRoutes_ExcludeIntermediate(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	i1, _ := cipher.GenerateKeyPair()
	i2, _ := cipher.GenerateKeyPair()

	localTps := []oracleLocalTp{
		mkLocal(src, i1, tptypes.STCPR),
		mkLocal(src, i2, tptypes.STCPR),
	}
	dstEntries := []*transport.Entry{
		mkDstEntry(dst, i1, tptypes.STCPR),
		mkDstEntry(dst, i2, tptypes.STCPR),
	}

	opts := &DialOptions{ExcludeIntermediatePKs: []cipher.PubKey{i1}}
	legs, err := computeDisjoint2HopRoutes(src, dst, localTps, dstEntries, opts, 0)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if len(legs) != 1 || legs[0].Intermediate != i2 {
		t.Fatalf("with i1 excluded, want single leg via i2, got %v", intermediatesOf(legs))
	}
}

func TestComputeDisjoint2HopRoutes_MaxCap(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	i1, _ := cipher.GenerateKeyPair()
	i2, _ := cipher.GenerateKeyPair()
	i3, _ := cipher.GenerateKeyPair()

	var localTps []oracleLocalTp
	var dstEntries []*transport.Entry
	for _, i := range []cipher.PubKey{i1, i2, i3} {
		localTps = append(localTps, mkLocal(src, i, tptypes.STCPR))
		dstEntries = append(dstEntries, mkDstEntry(dst, i, tptypes.STCPR))
	}

	legs, err := computeDisjoint2HopRoutes(src, dst, localTps, dstEntries, nil, 2)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if len(legs) != 2 {
		t.Fatalf("max=2 → want 2 legs, got %d", len(legs))
	}
}

func TestComputeDisjoint2HopRoutes_NoSharedIntermediate(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	i1, _ := cipher.GenerateKeyPair()
	i2, _ := cipher.GenerateKeyPair()

	localTps := []oracleLocalTp{mkLocal(src, i1, tptypes.STCPR)}
	dstEntries := []*transport.Entry{mkDstEntry(dst, i2, tptypes.STCPR)}

	if _, err := computeDisjoint2HopRoutes(src, dst, localTps, dstEntries, nil, 0); err == nil {
		t.Fatal("want error when src and dst share no intermediate")
	}
}

func TestComputeDisjoint2HopRoutes_PrefersDirectTypeOverDMSG(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	i1, _ := cipher.GenerateKeyPair()

	// src has BOTH a DMSG and an STCPR transport to i1; dst has STCPR to i1.
	localTps := []oracleLocalTp{
		mkLocal(src, i1, tptypes.DMSG),
		mkLocal(src, i1, tptypes.STCPR),
	}
	dstEntries := []*transport.Entry{mkDstEntry(dst, i1, tptypes.STCPR)}

	legs, err := computeDisjoint2HopRoutes(src, dst, localTps, dstEntries, nil, 0)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if len(legs) != 1 {
		t.Fatalf("want 1 leg, got %d", len(legs))
	}
	// The src->i1 hop must use the STCPR transport, never DMSG.
	wantID := transport.MakeTransportID(src, i1, tptypes.STCPR)
	if legs[0].Forward[0].TpID != wantID {
		t.Fatalf("src->i1 leg used %s, want STCPR %s (DMSG must be skipped)", legs[0].Forward[0].TpID, wantID)
	}
}

func TestReconstructDstEntries(t *testing.T) {
	dst, _ := cipher.GenerateKeyPair()
	peer, _ := cipher.GenerateKeyPair()

	resp := &TransportQueryResponse{
		TargetPK: dst,
		Entries:  []transport.CompactEntry{{Remote: peer, Type: tptypes.STCPR}},
	}
	entries := reconstructDstEntries(dst, resp)
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	if !entries[0].HasEdge(dst) || !entries[0].HasEdge(peer) {
		t.Fatalf("reconstructed entry missing edges: %v", entries[0].Edges)
	}
	if entries[0].ID != transport.MakeTransportID(dst, peer, tptypes.STCPR) {
		t.Fatalf("reconstructed ID mismatch")
	}
}
