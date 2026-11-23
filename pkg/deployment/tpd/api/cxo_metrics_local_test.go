// Package api pkg/deployment/tpd/api/cxo_metrics_local_test.go c3-net-discovery
package api

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/logging"
)

// TestMetricsPublishedDaysMatchSubscriber checks that what a reader in this
// process gets from the metrics publisher (PublishedDays, PublishedDayParts)
// is what a subscriber to the feed holds, byte for byte, including days whose
// leaves the publisher dropped from memory after publishing.
func TestMetricsPublishedDaysMatchSubscriber(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	pub, err := treestore.NewWithTCP("127.0.0.1:0", sk, treestore.PubConfig{
		InMemoryDB:          true,
		BatchWindow:         20 * time.Millisecond,
		DropPublishedLeaves: true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck
	pub.SetAllowlist(nil)
	m := &MetricsCXOPublisher{pub: pub, log: logging.MustGetLogger("t-metrics-local"), parts: map[string]int{}}

	dates := []string{"2026-11-22", "2026-11-21", "2026-11-20"}
	for i, date := range dates {
		var recs []store.TransportMetric
		for j := range 40 * (i + 1) {
			recs = append(recs, store.TransportMetric{
				ID: fmt.Sprintf("tp-%d", j), Type: "stcpr", Edges: []string{fmt.Sprintf("a%d", j), fmt.Sprintf("b%d", j)},
				Daily: []store.DailyEdgeBandwidth{{Date: date, A: &store.EdgeBandwidth{Sent: uint64(j), Recv: 1}, B: &store.EdgeBandwidth{Sent: 1, Recv: uint64(j)}}},
			})
		}
		// A small cap splits the older days into several parts.
		parts, err := gzipParts(recs, 600)
		require.NoError(t, err)
		ops, next := planDayOps(map[string][][]byte{date: parts}, dates, m.parts, false)
		require.NoError(t, pub.PutBatch(ops))
		m.setParts(next)
		require.NoError(t, pub.Flush())
	}
	require.Equal(t, dates, m.PublishedDays())

	sub, err := treestore.NewSubscriberTCP("", pk, treestore.SubConfig{InMemoryDB: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Close() }) //nolint:errcheck
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	require.NoError(t, sub.ConnectTCP(ctx, pub.Node().TCP().Address()))

	feed := func() map[string][][]byte {
		byDate := map[string][][]byte{}
		sub.Walk(store.MetricsDayPrefix, func(path string, body []byte) bool {
			if date, ok := store.MetricsDayDate(path); ok {
				byDate[date] = append(byDate[date], body)
			}
			return true
		})
		return byDate
	}
	want := 0
	for _, date := range dates {
		p, ok := m.PublishedDayParts(date)
		require.True(t, ok, date)
		want += len(p)
	}
	require.Greater(t, want, len(dates), "some day must be split into parts")
	require.Eventually(t, func() bool {
		n := 0
		for _, p := range feed() {
			n += len(p)
		}
		return n == want
	}, time.Minute, 50*time.Millisecond)

	got := feed()
	require.Len(t, got, len(dates))
	for _, date := range dates {
		p, ok := m.PublishedDayParts(date)
		require.True(t, ok)
		// A subscriber walks a day's parts in no fixed order.
		slices.SortFunc(p, bytes.Compare)
		slices.SortFunc(got[date], bytes.Compare)
		require.Equal(t, got[date], p, date)
	}
	_, ok := m.PublishedDayParts("2026-01-01")
	require.False(t, ok)
}
