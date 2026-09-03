// Package visor pkg/visor/api_tpd_uptime_subscriber_test.go c3-vis-core
package visor

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
)

// fakeUptimeMgr is a minimal uptimeCXOSubMgr for exercising readUptimeWindow's
// dual-parse without a live CXO subscription manager.
type fakeUptimeMgr struct {
	snap map[string][]byte
	ts   time.Time
}

func (f *fakeUptimeMgr) Get(_ CXOFeed, path string) ([]byte, time.Time, bool) {
	b, ok := f.snap[path]
	if !ok || len(b) == 0 {
		return nil, time.Time{}, false
	}
	return b, f.ts, true
}

func (f *fakeUptimeMgr) Walk(_ CXOFeed, prefix string, fn func(path string, body []byte) bool) bool {
	for p, b := range f.snap {
		if len(p) <= len(prefix) || p[:len(prefix)] != prefix {
			continue
		}
		if !fn(p, b) {
			break
		}
	}
	return true
}

func mkTimeline(setSlot int) string {
	b := make([]byte, 288)
	for i := range b {
		b[i] = ' '
	}
	b[setSlot] = '.'
	return string(b)
}

// TestReadUptimeWindowGzippedCompactLeaf exercises the current wire shape: one
// gzipped []compactVisorSummary leaf at exactly "uptimes/days/<n>". The reader
// must gunzip, reconstruct the v3 store.VisorSummary shape, and render each
// bitmap back to the 288-char timeline string.
func TestReadUptimeWindowGzippedCompactLeaf(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	compact := []compactVisorSummary{{
		PK:       pk,
		Online:   true,
		Version:  "v1.3.99",
		Timeline: map[string][]byte{"2026-08-19": timelineBitmap(mkTimeline(5))},
	}}
	raw, _ := json.Marshal(compact) //nolint:errcheck
	mgr := &fakeUptimeMgr{
		snap: map[string][]byte{"uptimes/days/30": cxoutils.Gzip(raw)},
		ts:   time.Unix(1000, 0),
	}
	got, ts, ok := readUptimeWindow(mgr, "uptimes/days/30")
	if !ok {
		t.Fatal("expected gzipped-compact-leaf hit")
	}
	if !ts.Equal(time.Unix(1000, 0)) {
		t.Fatalf("ts = %v, want 1000", ts)
	}
	var summaries []store.VisorSummary
	if err := json.Unmarshal(got, &summaries); err != nil {
		t.Fatalf("unmarshal reconstructed body: %v", err)
	}
	if len(summaries) != 1 || summaries[0].PK != pk || !summaries[0].Online {
		t.Fatalf("summary not reconstructed: %+v", summaries)
	}
	tl := summaries[0].Timeline["2026-08-19"]
	if len(tl) != 288 || tl[5] != '.' {
		t.Fatalf("timeline not rendered: len=%d slot5=%q", len(tl), string(tl[5]))
	}
}

func TestReadUptimeWindowReassemblesPerVisorLeaves(t *testing.T) {
	// Real keys: cipher.PubKey.UnmarshalText validates the point, so the leaves
	// must carry canonical PKs to survive the reader's json.Unmarshal.
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	if pkA.Hex() > pkB.Hex() {
		pkA, pkB = pkB, pkA // the reader sorts by PK hex; fix the expected order
	}

	leaf := func(pk cipher.PubKey, slot int) []byte {
		c := compactVisorSummary{
			PK:       pk,
			Online:   true,
			Timeline: map[string][]byte{"2026-08-19": timelineBitmap(mkTimeline(slot))},
		}
		b, _ := json.Marshal(c) //nolint:errcheck
		return b
	}
	mgr := &fakeUptimeMgr{
		snap: map[string][]byte{
			"uptimes/days/30/" + pkA.Hex(): leaf(pkA, 3),
			"uptimes/days/30/" + pkB.Hex(): leaf(pkB, 9),
			"uptimes/days/1/" + pkA.Hex():  leaf(pkA, 0), // different window; must be ignored
		},
		ts: time.Unix(2000, 0),
	}

	body, ts, ok := readUptimeWindow(mgr, "uptimes/days/30")
	if !ok {
		t.Fatal("expected reassembly hit")
	}
	if !ts.Equal(time.Unix(2000, 0)) {
		t.Fatalf("ts = %v, want 2000", ts)
	}
	var got []store.VisorSummary
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal reassembled body: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d summaries, want 2 (window-30 only)", len(got))
	}
	// Sorted by PK hex; pkA (0x00aa..) sorts before pkB (0x00bb..).
	if got[0].PK != pkA || got[1].PK != pkB {
		t.Fatalf("summaries not sorted by PK: %v", []cipher.PubKey{got[0].PK, got[1].PK})
	}
	// Timeline rendered back to the 288-char v3 string with the right slot set.
	tl := got[0].Timeline["2026-08-19"]
	if len(tl) != 288 || tl[3] != '.' {
		t.Fatalf("pkA timeline not rendered: len=%d slot3=%q", len(tl), string(tl[3]))
	}
}

// timelineBitmap mirrors the publisher's packing for the test fixtures.
func timelineBitmap(line string) []byte {
	bm := make([]byte, 36)
	for i := 0; i < len(line) && i < 288; i++ {
		if line[i] == '.' {
			bm[i/8] |= 1 << uint(7-i%8)
		}
	}
	return bm
}
