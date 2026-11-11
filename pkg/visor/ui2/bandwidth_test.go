package ui2

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/stats"
)

func TestBandwidthOf(t *testing.T) {
	me, _ := cipher.GenerateKeyPair()
	peer, _ := cipher.GenerateKeyPair()
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	tps := []*stats.TransportRecord{
		{Edges: []cipher.PubKey{me, peer}, Current: &stats.LiveSnapshot{SentBytes: 1, RecvBytes: 2},
			Daily: []stats.DailyRollup{{Date: "2026-10-01", SentBytes: 1000}, {Date: "2026-11-10", SentBytes: 10, RecvBytes: 20}}},
		{Edges: []cipher.PubKey{peer, me}, Daily: []stats.DailyRollup{{Date: "2026-11-09", RecvBytes: 500}}},
	}
	b := BandwidthOf(me.Hex(), 7, tps, now)
	require.Equal(t, peer.Hex(), b.Rows[0].Remote)
	require.Equal(t, uint64(500), b.Rows[0].Recv, "busiest first")
	require.Equal(t, uint64(530), b.Total, "only days since the cutoff count")
	b = BandwidthOf(me.Hex(), 1, tps, now)
	require.Equal(t, uint64(3), b.Total, "today is the live snapshot")
}
