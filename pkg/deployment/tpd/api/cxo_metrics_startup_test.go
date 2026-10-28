package api

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/logging"
)

type windowFake struct {
	leafFake
	metrics []store.TransportMetric
}

func (f *windowFake) GetAllTransportMetrics(context.Context, store.MetricsQuery) ([]store.TransportMetric, error) {
	return f.metrics, nil
}

// The startup cycle publishes the saved settled days as they were saved and
// builds the rest from the rows, to the same bytes as pivoting the whole window.
func TestMetricsStartupPublishesSavedAndRebuiltDays(t *testing.T) {
	now := time.Now().UTC()
	window := store.MetricsWindowDates(now, metricsWindowDays)
	metrics := metricsFixture(60, 1)
	for i := range metrics {
		for d := range metrics[i].Daily {
			metrics[i].Daily[d].Date = window[(i+d*3)%len(window)]
		}
		metrics[i].Daily = append(metrics[i].Daily, metrics[i].Daily[0])
		metrics[i].Daily[1].Date = window[(i*7)%len(window)]
	}
	saved := map[string][][]byte{}
	for _, d := range window[1:] {
		if d == window[5] || d == window[9] {
			continue
		}
		saved[d] = savedDay(t, []store.TransportMetric{{ID: "saved-" + d, Type: "stcpr"}})
	}
	fake := &windowFake{leafFake: leafFake{saved: saved}, metrics: metrics}

	_, sk := cipher.GenerateKeyPair()
	pub, err := treestore.NewWithTCP("127.0.0.1:0", sk, treestore.PubConfig{InMemoryDB: true, BatchWindow: 5 * time.Millisecond, DropPublishedLeaves: true})
	require.NoError(t, err)
	defer pub.Close() //nolint:errcheck
	m := &MetricsCXOPublisher{api: &API{store: fake}, pub: pub, log: logging.MustGetLogger("t"),
		parts: map[string]int{}, unsaved: map[string][][]byte{}}
	m.publish(context.Background(), true)
	require.NoError(t, pub.Flush())

	pivoted := store.PivotDailyMetrics(metrics, window)
	want := map[string][]byte{}
	for _, d := range window {
		parts, ok := saved[d]
		if !ok {
			var err error
			parts, err = gzipPartsFrom(pivoted[d], maxPublishBody, 0)
			require.NoError(t, err)
		}
		for i, p := range parts {
			want[store.MetricsDayPartPath(d, i)] = p
		}
	}
	got := map[string][]byte{}
	pub.Walk("metrics/day", func(p string, v []byte) bool { got[p] = v; return true })
	require.Equal(t, want, got)
	require.Len(t, m.parts, len(window))
	require.NotContains(t, fake.saved, window[0])
	require.Contains(t, fake.saved, window[5])
}
