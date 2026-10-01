package clirewardsserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cxo/cxosub"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	tpdstore "github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/logging"
)

type fakeVisorBWFeed struct {
	synced time.Time
	leaves map[string][]byte
}

func (f fakeVisorBWFeed) LastSync(cxosub.Feed) time.Time { return f.synced }
func (f fakeVisorBWFeed) Walk(_ cxosub.Feed, _ string, fn func(string, []byte) bool) bool {
	for p, b := range f.leaves {
		if !fn(p, b) {
			return false
		}
	}
	return true
}

func leaf(t *testing.T, d tpdstore.VisorBWDay) []byte {
	b, err := json.Marshal(d)
	require.NoError(t, err)
	return cxoutils.Gzip(b)
}

func TestWriteSettledVisorBW(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 3, 1, 0, 0, 0, time.UTC)
	good := func(date string) tpdstore.VisorBWDay {
		return tpdstore.VisorBWDay{Version: tpdstore.VisorBWVersion, Date: date, ClassesAt: now,
			Visors: map[string]map[string]uint64{"a": {"stcpr": 5}}}
	}
	feed := fakeVisorBWFeed{leaves: map[string][]byte{
		tpdstore.VisorBWDayPath("2026-10-02"): leaf(t, good("2026-10-02")),                                                            // yesterday
		tpdstore.VisorBWDayPath("2026-10-01"): leaf(t, good("2026-10-01")),                                                            // already rewarded
		tpdstore.VisorBWDayPath("2026-09-30"): leaf(t, good("2026-09-30")),                                                            // not rewarded yet
		tpdstore.VisorBWDayPath("2026-09-29"): leaf(t, tpdstore.VisorBWDay{Version: 3, Date: "2026-09-29", ClassesAt: now}),           // empty
		tpdstore.VisorBWDayPath("2026-09-28"): leaf(t, tpdstore.VisorBWDay{Version: 3, Date: "2026-09-28", Visors: good("x").Visors}), // no classes
	}}
	// Yesterday and 10-01 were already calculated from bw-collect's files.
	for _, d := range []string{"2026-10-02", "2026-10-01"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, d+"_stats.txt"), nil, 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, d+"_bandwidth.json"), []byte(`{"version":2,"transports":[]}`), 0o600))
	}
	log := logging.MustGetLogger("t")

	writeSettledVisorBW(feed, dir, now, log)
	require.False(t, haveVisorBW(filepath.Join(dir, "2026-09-30_bandwidth.json")), "nothing is read before the first sync completes")

	feed.synced = now
	writeSettledVisorBW(feed, dir, now, log)
	require.True(t, haveVisorBW(filepath.Join(dir, "2026-10-02_bandwidth.json")), "yesterday is still recalculated, so it gets the settled day")
	require.False(t, haveVisorBW(filepath.Join(dir, "2026-10-01_bandwidth.json")), "an already-rewarded day keeps its numbers")
	require.True(t, haveVisorBW(filepath.Join(dir, "2026-09-30_bandwidth.json")))
	for _, d := range []string{"2026-09-29", "2026-09-28"} {
		_, err := os.Stat(filepath.Join(dir, d+"_bandwidth.json"))
		require.True(t, os.IsNotExist(err), "%s: an empty day, or one computed without IP classes, is never written", d)
	}
}
