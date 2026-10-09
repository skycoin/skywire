package api

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/logging"
)

// leafFake is a store that keeps saved leaves in memory.
type leafFake struct {
	store.Store
	saved map[string][][]byte
}

func (f *leafFake) SaveMetricsLeaf(_ context.Context, date string, parts [][]byte) error {
	f.saved[date] = parts
	return nil
}

func (f *leafFake) LoadMetricsLeaves(_ context.Context, dates []string) (map[string][][]byte, error) {
	out := map[string][][]byte{}
	for _, d := range dates {
		if p, ok := f.saved[d]; ok {
			out[d] = p
		}
	}
	return out, nil
}

// A day is saved once it is no longer open — not while it can still change —
// and the body saved is the last one published for it.
func TestMetricsSettleSavesDaysOnceSettled(t *testing.T) {
	fake := &leafFake{saved: map[string][][]byte{}}
	m := &MetricsCXOPublisher{api: &API{store: fake}, log: logging.MustGetLogger("t"), unsaved: map[string][][]byte{}}
	ctx := context.Background()
	body := func(id string) [][]byte { return savedDay(t, []store.TransportMetric{{ID: id, Type: "stcpr"}}) }

	// Just after midnight: today and yesterday open.
	m.settle(ctx, map[string][][]byte{"d2": body("today-1"), "d1": body("yday-1")}, []string{"d2", "d1"})
	require.Empty(t, fake.saved)
	m.settle(ctx, map[string][][]byte{"d2": body("today-2"), "d1": body("yday-2")}, []string{"d2", "d1"})
	require.Empty(t, fake.saved)

	// Yesterday settles: its last body is saved, today stays unsaved.
	m.settle(ctx, map[string][][]byte{"d2": body("today-3")}, []string{"d2"})
	require.Equal(t, map[string][][]byte{"d1": body("yday-2")}, fake.saved)
	require.Contains(t, m.unsaved, "d2")
	require.NotContains(t, m.unsaved, "d1")

	got := m.loadSettled(ctx, []string{"d1", "d0"})
	require.Equal(t, map[string][][]byte{"d1": body("yday-2")}, got)
}

// A day that settles with no transports is not saved, and one saved that way
// earlier loads as missing, so a start rebuilds it from its rows.
func TestMetricsSettleNeverKeepsAnEmptyDay(t *testing.T) {
	empty := savedDay(t, []store.TransportMetric{})
	full := savedDay(t, []store.TransportMetric{{ID: "x", Type: "stcpr"}})
	fake := &leafFake{saved: map[string][][]byte{"d0": empty, "d1": full}}
	m := &MetricsCXOPublisher{api: &API{store: fake}, log: logging.MustGetLogger("t"), unsaved: map[string][][]byte{}}
	ctx := context.Background()

	require.Equal(t, map[string][][]byte{"d1": full}, m.loadSettled(ctx, []string{"d0", "d1"}))

	m.settle(ctx, map[string][][]byte{"d2": empty}, nil)
	require.NotContains(t, fake.saved, "d2")
	require.Empty(t, m.unsaved)
}

// With a data path the metrics feed keeps its store on disk, not in the heap.
func TestMetricsPubConfig(t *testing.T) {
	log := logging.MustGetLogger("t")
	if c := metricsPubConfig(log, ""); !c.InMemoryDB {
		t.Fatal("no data path must keep the store in memory")
	}
	c := metricsPubConfig(log, "/var/lib/skywire/tpd/bandwidth")
	if c.InMemoryDB || c.DataDir != "/var/lib/skywire/tpd/bandwidth/cxo-metrics" {
		t.Fatalf("got InMemoryDB=%v DataDir=%q", c.InMemoryDB, c.DataDir)
	}
}
