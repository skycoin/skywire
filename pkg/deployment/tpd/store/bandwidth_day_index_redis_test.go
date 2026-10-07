package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// A request canceled while it waits on the index scan must not leave a
// partial index behind. Settled days read through a partial index came out
// empty and were saved that way, which emptied rewards pool 2.
func TestBandwidthIndexIgnoresACancelledRequest(t *testing.T) {
	s := newTestRedisStore(t)
	bg := context.Background()
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	id := uuid.New()
	day := time.Now().UTC().AddDate(0, 0, -3)
	require.NoError(t, s.client.HSet(bg, s.bandwidthDailyKey(id.String(), day), a.Hex()+":sent", "700", b.Hex()+":sent", "300").Err())
	require.NoError(t, s.client.Set(bg, s.bandwidthEdgesKey(id.String()), a.Hex()+","+b.Hex(), 0).Err())

	canceled, cancel := context.WithCancel(bg)
	cancel()
	ix, err := s.bandwidthIndex(canceled)
	require.NoError(t, err)
	require.NotZero(t, ix.byID[id], "the scan finished despite the canceled request")
	cached, ok := s.bwIndex.get()
	require.True(t, ok)
	require.Equal(t, ix, cached)

	metrics, err := s.GetAllTransportMetrics(bg, MetricsQuery{Days: 5, Live: "all", Edges: true, Bandwidth: true})
	require.NoError(t, err)
	require.Len(t, metrics, 1)
	require.Len(t, metrics[0].Daily, 1)
	require.Equal(t, day.Format(MetricsDateFormat), metrics[0].Daily[0].Date)
}
