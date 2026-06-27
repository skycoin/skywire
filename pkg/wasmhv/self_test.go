package wasmhv

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
)

// fakeSelf is a SelfProvider stub for a known local visor.
type fakeSelf struct {
	pk  cipher.PubKey
	tps []*TransportSummary
}

func (f fakeSelf) SelfPK() cipher.PubKey { return f.pk }
func (f fakeSelf) SelfOverview() Overview {
	return Overview{PubKey: f.pk, RoutesCount: 3}
}
func (f fakeSelf) SelfSummary() Summary {
	ov := f.SelfOverview()
	return Summary{Overview: &ov, Online: true, IsHypervisor: true}
}
func (f fakeSelf) SelfTransports() []*TransportSummary { return f.tps }
func (f fakeSelf) SelfRoutes() []byte                  { return []byte("[]") }

func newSelfPK(t *testing.T) cipher.PubKey {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	return pk
}

// TestSelf_ListedAndRoutedLocally verifies that once a SelfProvider is attached,
// the local visor appears in the summary list and its /visors/<pk> routes are
// served from the provider (not a gob RPC dial), with no remote visors connected.
func TestSelf_ListedAndRoutedLocally(t *testing.T) {
	pk := newSelfPK(t)
	tid := uuid.New()
	core := NewCore(pk, nil)
	core.SetSelf(fakeSelf{
		pk:  pk,
		tps: []*TransportSummary{{ID: tid, Local: pk, Remote: newSelfPK(t), Type: "wt", Label: "user"}},
	})

	// /visors-summary includes the self visor (the only one).
	status, body := core.ServeHTTP("GET", "/api/visors-summary", nil)
	if status != 200 {
		t.Fatalf("visors-summary status = %d", status)
	}
	var sums []Summary
	if err := json.Unmarshal(body, &sums); err != nil {
		t.Fatalf("unmarshal summaries: %v", err)
	}
	if len(sums) != 1 || sums[0].Overview == nil || sums[0].Overview.PubKey != pk {
		t.Fatalf("self visor not first in summaries: %+v", sums)
	}
	if !sums[0].IsHypervisor || !sums[0].Online {
		t.Fatalf("self summary flags wrong: %+v", sums[0])
	}

	// /visors/<selfPK> is served locally (overview with the route count).
	status, body = core.ServeHTTP("GET", "/api/visors/"+pk.Hex(), nil)
	if status != 200 {
		t.Fatalf("self overview status = %d", status)
	}
	var ov Overview
	if err := json.Unmarshal(body, &ov); err != nil {
		t.Fatalf("unmarshal overview: %v", err)
	}
	if ov.PubKey != pk || ov.RoutesCount != 3 {
		t.Fatalf("self overview wrong: %+v", ov)
	}

	// /visors/<selfPK>/transports returns the local transports.
	status, body = core.ServeHTTP("GET", "/api/visors/"+pk.Hex()+"/transports", nil)
	if status != 200 {
		t.Fatalf("self transports status = %d", status)
	}
	var tps []*TransportSummary
	if err := json.Unmarshal(body, &tps); err != nil {
		t.Fatalf("unmarshal transports: %v", err)
	}
	if len(tps) != 1 || tps[0].ID != tid || tps[0].Type != "wt" {
		t.Fatalf("self transports wrong: %+v", tps)
	}
}

// TestSelf_AbsentWhenNotSet verifies a plain standalone HV (no SelfProvider)
// lists no visors when none have dialed in.
func TestSelf_AbsentWhenNotSet(t *testing.T) {
	core := NewCore(newSelfPK(t), nil)
	status, body := core.ServeHTTP("GET", "/api/visors-summary", nil)
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	var sums []Summary
	if err := json.Unmarshal(body, &sums); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(sums) != 0 {
		t.Fatalf("expected no visors without a self provider, got %d", len(sums))
	}
}
