// Package visor pkg/visor/hypervisor_attached_graph_test.go c3-vis-api
package visor

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

func summaryWith(tps ...*TransportSummary) *Summary {
	return &Summary{Overview: &Overview{Transports: tps}}
}

func TestAttachedEntriesFromSummaries(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	c, _ := cipher.GenerateKeyPair()
	d, _ := cipher.GenerateKeyPair()
	rsn, _ := cipher.GenerateKeyPair()
	now := time.Now()
	abID := transport.MakeTransportID(a, b, tptypes.STCPR)

	cache := map[cipher.PubKey]cachedSummary{
		a: {seenAt: now, sum: summaryWith(
			&TransportSummary{ID: abID, Local: a, Remote: b, Type: tptypes.STCPR, LatencyMS: 12},
			&TransportSummary{Local: a, Remote: c, Type: tptypes.SUDPH}, // zero ID: derived
			&TransportSummary{Local: a, Remote: rsn, Type: tptypes.DMSG, IsSetup: true},
		)},
		b: {seenAt: now.Add(-time.Second), sum: summaryWith(
			&TransportSummary{ID: abID, Local: b, Remote: a, Type: tptypes.STCPR, ThroughputBps: 5000}, // other end
		)},
		d: {seenAt: now.Add(-attachedGraphStaleAfter - time.Second), sum: summaryWith(
			&TransportSummary{Local: d, Remote: c, Type: tptypes.STCPR},
		)},
	}

	entries, sum := attachedEntriesFromSummaries(cache, now)
	if len(entries) != 2 {
		t.Fatalf("want 2 entries (a-b, a-c), got %d", len(entries))
	}
	var ab *transport.Entry
	for _, e := range entries {
		if e.ID == abID {
			ab = e
		}
		if e.Label == transport.LabelSetup || e.Edges[0] == rsn || e.Edges[1] == rsn {
			t.Fatalf("setup transport leaked: %+v", e)
		}
		if e.Edges[0] == d || e.Edges[1] == d {
			t.Fatalf("stale visor leaked: %+v", e)
		}
	}
	if ab == nil {
		t.Fatal("a-b entry missing")
	}
	if ab.Latency != 12 || ab.ThroughputBps != 5000 {
		t.Fatalf("metrics not merged across both ends: %+v", ab)
	}

	// Same set, different order/metrics → same hash; a new edge → new hash.
	_, sum2 := attachedEntriesFromSummaries(cache, now)
	if sum != sum2 {
		t.Fatal("hash not stable for an unchanged set")
	}
	cache[b] = cachedSummary{seenAt: now, sum: summaryWith(
		&TransportSummary{ID: abID, Local: b, Remote: a, Type: tptypes.STCPR},
		&TransportSummary{Local: b, Remote: c, Type: tptypes.STCPR},
	)}
	_, sum3 := attachedEntriesFromSummaries(cache, now)
	if sum == sum3 {
		t.Fatal("hash unchanged after a new edge")
	}
}

type fakeAllTransports struct {
	transport.DiscoveryClient
	entries []*transport.Entry
}

func (f fakeAllTransports) GetAllTransports(context.Context) ([]*transport.Entry, error) {
	return f.entries, nil
}

func TestCXOAwareTPD_MergesLocalGraph(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	c, _ := cipher.GenerateKeyPair()
	shared := transport.MakeEntry(a, b, tptypes.STCPR, transport.LabelUser)
	shared.Latency = 7
	localOnly := transport.MakeEntry(b, c, tptypes.SUDPH, transport.LabelUser)
	localDup := transport.MakeEntry(a, b, tptypes.STCPR, transport.LabelUser) // same ID as shared

	v := &Visor{initLock: new(sync.RWMutex)}
	at := time.Now()
	v.SetLocalGraphSource(func() ([]*transport.Entry, time.Time) {
		return []*transport.Entry{&localDup, &localOnly}, at
	})
	dc := &cxoAwareTPD{DiscoveryClient: fakeAllTransports{entries: []*transport.Entry{&shared}}, v: v}

	got, err := dc.GetAllTransports(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 (shared + local-only), got %d", len(got))
	}
	if got[0].ID != shared.ID || got[0].Latency != 7 {
		t.Fatalf("network view must win on a duplicate: %+v", got[0])
	}
	if got[1].ID != localOnly.ID {
		t.Fatalf("local-only entry missing: %+v", got[1])
	}
	if !v.localGraphHasPeer(c) || v.localGraphHasPeer(cipher.PubKey{}) {
		t.Fatal("localGraphHasPeer wrong")
	}
	// No CXO feed primed → no version, whatever the local graph says.
	if _, ok := dc.AllTransportsSyncedAt(); ok {
		t.Fatal("version must stay unreported without a primed CXO feed")
	}
	v.SetLocalGraphSource(nil)
	got, err = dc.GetAllTransports(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("source removed but %d entries", len(got))
	}
	_ = uuid.Nil
}
