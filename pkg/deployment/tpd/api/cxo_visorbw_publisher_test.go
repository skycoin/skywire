package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/logging"
)

// fixedClasses serves one IP-class leaf, or none.
type fixedClasses struct{ body []byte }

func (f fixedClasses) Get(path string) ([]byte, bool) {
	return f.body, path == store.IPClassPath && f.body != nil
}
func (fixedClasses) Close() error { return nil }

func classesLeaf(t *testing.T, c map[string]string) []byte {
	b, err := json.Marshal(store.IPClasses{Version: 1, GeneratedAt: time.Now(), Classes: c})
	require.NoError(t, err)
	return cxoutils.Gzip(b)
}

func savedDay(t *testing.T, recs []store.TransportMetric) [][]byte {
	b, err := json.Marshal(recs)
	require.NoError(t, err)
	return [][]byte{cxoutils.Gzip(b)}
}

// A settled day is published once, with same-IP transports left out; nothing
// is published before the reward system's IP classes have arrived; and a day
// leaving the window is removed.
func TestVisorBWPublisher(t *testing.T) {
	now := time.Date(2026, 10, 2, 0, 30, 0, 0, time.UTC)
	const day = "2026-10-01"
	eb := func(s, r uint64) *store.EdgeBandwidth { return &store.EdgeBandwidth{Sent: s, Recv: r} }
	fake := &leafFake{saved: map[string][][]byte{
		day: savedDay(t, []store.TransportMetric{
			{Type: "stcpr", Edges: []string{"a", "b"}, Daily: []store.DailyEdgeBandwidth{{Date: day, A: eb(100, 40)}}},
			{Type: "stcpr", Edges: []string{"a", "c"}, Daily: []store.DailyEdgeBandwidth{{Date: day, A: eb(7, 9)}}},
		}),
	}}
	var batches [][]treestore.PutOp
	p := &VisorBWCXOPublisher{
		api: &API{store: fake}, log: logging.MustGetLogger("t"), published: map[string]bool{},
		putBatch: func(ops []treestore.PutOp) error { batches = append(batches, ops); return nil },
	}

	p.classes = fixedClasses{}
	p.publishOnce(t.Context(), now)
	require.Empty(t, batches, "no day goes out before the IP classes have arrived")

	p.classes = fixedClasses{body: classesLeaf(t, map[string]string{"a": "x", "b": "y", "c": "x"})}
	p.publishOnce(t.Context(), now)
	require.Len(t, batches, 1)
	require.Len(t, batches[0], 1)
	require.Equal(t, store.VisorBWDayPath(day), batches[0][0].Path)
	var got store.VisorBWDay
	require.NoError(t, json.Unmarshal(cxoutils.Gunzip(batches[0][0].Value), &got))
	require.Equal(t, 1, got.SameIPExcluded, "a and c share an IP")
	require.Equal(t, map[string]map[string]uint64{"a": {"stcpr": 100}, "b": {"stcpr": 40}}, got.Visors)

	p.publishOnce(t.Context(), now)
	require.Len(t, batches, 1, "a published day is not recomputed")

	p.publishOnce(t.Context(), now.AddDate(0, 0, visorBWWindowDays))
	require.Len(t, batches, 2)
	require.Equal(t, []treestore.PutOp{{Path: store.VisorBWDayPath(day)}}, batches[1], "a day leaving the window is removed")
}
