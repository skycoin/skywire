// Package router pkg/router/router_dial_dmsg_multihop_test.go —
// pins the "no DMSG in multihop routes" invariant. DMSG transports
// relay through an opaque dmsg server (potentially a chain of
// servers via server-to-server forwarding), so a DMSG hop in a
// multihop path makes the route unaccountable — traffic could
// transit the same server multiple times with no way to detect it.
// Single-hop DMSG dials stay allowed; both endpoints are explicit.
package router

import (
	"testing"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

func TestRejectDMSGMultihop_PreservesSingleHopDMSG(t *testing.T) {
	tpID := uuid.New()
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	paths := [][]routing.Hop{
		{{TpID: tpID, From: src, To: dst}}, // 1-hop direct DMSG
	}
	typeFor := func(uuid.UUID) string { return "dmsg" }
	out := rejectDMSGMultihop(paths, typeFor)
	if len(out) != 1 {
		t.Errorf("len(out)=%d, want 1 (1-hop DMSG must survive)", len(out))
	}
}

func TestRejectDMSGMultihop_DropsMultihopWithDMSG(t *testing.T) {
	tpA := uuid.New()
	tpB := uuid.New()
	src, _ := cipher.GenerateKeyPair()
	mid, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	paths := [][]routing.Hop{
		{ // 2-hop with DMSG first leg — must be rejected
			{TpID: tpA, From: src, To: mid},
			{TpID: tpB, From: mid, To: dst},
		},
	}
	typeFor := func(id uuid.UUID) string {
		if id == tpA {
			return "dmsg"
		}
		return "stcpr"
	}
	out := rejectDMSGMultihop(paths, typeFor)
	if len(out) != 0 {
		t.Errorf("len(out)=%d, want 0 (DMSG-bearing multihop must be dropped)", len(out))
	}
}

func TestRejectDMSGMultihop_KeepsNonDMSGMultihop(t *testing.T) {
	tpA := uuid.New()
	tpB := uuid.New()
	src, _ := cipher.GenerateKeyPair()
	mid, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	paths := [][]routing.Hop{
		{
			{TpID: tpA, From: src, To: mid},
			{TpID: tpB, From: mid, To: dst},
		},
	}
	typeFor := func(uuid.UUID) string { return "stcpr" }
	out := rejectDMSGMultihop(paths, typeFor)
	if len(out) != 1 {
		t.Errorf("len(out)=%d, want 1 (non-DMSG multihop must survive)", len(out))
	}
}

func TestRejectDMSGMultihop_StableOrder(t *testing.T) {
	// Survivors must keep their original index so downstream
	// ranking (which assumes paths[0] is "first") isn't perturbed.
	tpA := uuid.New() // DMSG
	tpB := uuid.New() // stcpr
	tpC := uuid.New() // stcpr
	src, _ := cipher.GenerateKeyPair()
	mid, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	paths := [][]routing.Hop{
		{{TpID: tpB, From: src, To: mid}, {TpID: tpC, From: mid, To: dst}}, // stcpr+stcpr → keep
		{{TpID: tpA, From: src, To: mid}, {TpID: tpB, From: mid, To: dst}}, // dmsg+stcpr → drop
		{{TpID: tpC, From: src, To: dst}},                                  // 1-hop stcpr → keep
	}
	typeFor := func(id uuid.UUID) string {
		if id == tpA {
			return "dmsg"
		}
		return "stcpr"
	}
	out := rejectDMSGMultihop(paths, typeFor)
	if len(out) != 2 {
		t.Fatalf("len(out)=%d, want 2", len(out))
	}
	if out[0][0].TpID != tpB {
		t.Errorf("first survivor TpID=%v, want %v (order must be preserved)", out[0][0].TpID, tpB)
	}
	if out[1][0].TpID != tpC {
		t.Errorf("second survivor TpID=%v, want %v", out[1][0].TpID, tpC)
	}
}

func TestRejectDMSGMultihop_EmptyAndNilSafe(t *testing.T) {
	out := rejectDMSGMultihop(nil, func(uuid.UUID) string { return "dmsg" })
	if out != nil {
		t.Errorf("nil input → non-nil output %v", out)
	}
	out = rejectDMSGMultihop([][]routing.Hop{}, func(uuid.UUID) string { return "dmsg" })
	if len(out) != 0 {
		t.Errorf("empty input → non-empty output (len=%d)", len(out))
	}
	out = rejectDMSGMultihop([][]routing.Hop{{{TpID: uuid.New()}}}, nil)
	if len(out) != 1 {
		t.Errorf("nil typeFor → input not returned (len=%d)", len(out))
	}
}

func TestRejectDMSGMultihop_UnknownTypeTreatedAsAllowed(t *testing.T) {
	// "Unknown" type (typeFor returns "") must not cause a multihop
	// to be dropped — we can't prove it's bad, the handshake will
	// fail loudly if it actually is.
	tpA := uuid.New()
	tpB := uuid.New()
	src, _ := cipher.GenerateKeyPair()
	mid, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	paths := [][]routing.Hop{
		{{TpID: tpA, From: src, To: mid}, {TpID: tpB, From: mid, To: dst}},
	}
	typeFor := func(uuid.UUID) string { return "" }
	out := rejectDMSGMultihop(paths, typeFor)
	if len(out) != 1 {
		t.Errorf("unknown type treated as DMSG; len(out)=%d, want 1", len(out))
	}
}
