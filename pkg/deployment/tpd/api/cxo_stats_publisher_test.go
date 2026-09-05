// Package api pkg/deployment/tpd/api/cxo_stats_publisher_test.go c4-net-discovery
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// capturedPut is one leaf the publisher wrote.
type capturedPut struct {
	path string
	body []byte
}

// newTestStatsPublisher builds a publisher with no DMSG/CXO node behind
// it: putFn captures leaves instead of writing them, which is the whole
// publish path minus the network.
func newTestStatsPublisher(t *testing.T, api *API, started time.Time) (*StatsCXOPublisher, *[]capturedPut) {
	t.Helper()
	var puts []capturedPut
	sp := &StatsCXOPublisher{
		api:                  api,
		log:                  logging.MustGetLogger("tpd-cxo-stats-pub-test"),
		transports:           newCompletenessTracker(started),
		visors:               newCompletenessTracker(started),
		heldSince:            make(map[string]time.Time),
		lastTransportVerdict: completenessVerdict{confidence: ConfidenceWarmup},
	}
	sp.putFn = func(path string, body []byte) error {
		puts = append(puts, capturedPut{path: path, body: append([]byte(nil), body...)})
		return nil
	}
	return sp, &puts
}

// pkFromByte builds a distinct (invalid but distinct) PubKey per index,
// which is all the aggregate counting needs.
func pkFromByte(b byte) cipher.PubKey {
	var pk cipher.PubKey
	pk[0] = 0x02
	pk[1] = b
	return pk
}

// testEntries builds n transports across two types over n+1 visors.
func testEntries(n int) []*transport.Entry {
	out := make([]*transport.Entry, 0, n)
	for i := 0; i < n; i++ {
		tp := types.Type("stcpr")
		if i%3 == 0 {
			tp = types.Type("sudph")
		}
		out = append(out, &transport.Entry{
			Edges: [2]cipher.PubKey{pkFromByte(byte(i)), pkFromByte(byte(i + 1))},
			Type:  tp,
		})
	}
	return out
}

func testUptimes() []store.VisorSummary {
	return []store.VisorSummary{
		{PK: pkFromByte(1), Online: true, Version: "v1.3.93"},
		{PK: pkFromByte(2), Online: true, Version: "v1.3.93"},
		{PK: pkFromByte(3), Online: false, Version: "v1.3.91"},
		{PK: pkFromByte(4), Online: true, Version: ""}, // → "unknown"
	}
}

// decodePut gunzips one captured leaf into a generic JSON object. It
// asserts the body really was gzipped, which is the failure #4509
// caught the hard way: CXO stores bytes verbatim, so a publisher that
// forgets Gzip silently ships an uncompressed body that every reader's
// Gunzip then passes through — the feed "works" while costing several
// times what it should.
func decodePut(t *testing.T, p capturedPut) map[string]interface{} {
	t.Helper()
	if len(p.body) < 2 || p.body[0] != 0x1f || p.body[1] != 0x8b {
		t.Fatalf("%s: body is not gzipped (first bytes %x)", p.path, p.body[:min(2, len(p.body))])
	}
	raw := cxoutils.Gunzip(p.body)
	if len(raw) == 0 || len(raw) == len(p.body) {
		t.Fatalf("%s: Gunzip did not decompress the body", p.path)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s: published body is not JSON: %v", p.path, err)
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// lastPutTo returns the most recent leaf written to path.
func lastPutTo(puts []capturedPut, path string) (capturedPut, bool) {
	for i := len(puts) - 1; i >= 0; i-- {
		if puts[i].path == path {
			return puts[i], true
		}
	}
	return capturedPut{}, false
}

// TestNetworkStatsMatchesHTTPHandler pins the published body to the
// GET /all-transports/stats body. Every key the handler emits must be
// present in the published object with an equal value — the feed is
// only a drop-in for the endpoint if a consumer can unmarshal it into
// the same struct and get the same numbers.
func TestNetworkStatsMatchesHTTPHandler(t *testing.T) {
	api := &API{transportsCache: testEntries(30)}
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

	// Handler body.
	rec := httptest.NewRecorder()
	api.getAllTransportsStats(rec, httptest.NewRequest(http.MethodGet, "/all-transports/stats", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("handler status = %d, want 200", rec.Code)
	}
	var handlerBody map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &handlerBody); err != nil {
		t.Fatalf("handler body not JSON: %v", err)
	}

	// Published body. Past warmup so the sample is judged on value.
	sp, puts := newTestStatsPublisher(t, api, now.Add(-time.Hour))
	sp.publishNetwork(context.Background(), now)

	p, ok := lastPutTo(*puts, StatsPathNetwork)
	if !ok {
		t.Fatalf("nothing published to %s", StatsPathNetwork)
	}
	published := decodePut(t, p)

	for k, want := range handlerBody {
		got, present := published[k]
		if !present {
			t.Fatalf("published body is missing handler key %q", k)
		}
		if !jsonEqual(got, want) {
			t.Fatalf("key %q: published %v, handler %v", k, got, want)
		}
	}

	// And it unmarshals into the handler's own struct type.
	var summary store.TransportSummary
	raw := cxoutils.Gunzip(p.body)
	if err := json.Unmarshal(raw, &summary); err != nil {
		t.Fatalf("published body does not unmarshal into store.TransportSummary: %v", err)
	}
	if summary.Total != 30 {
		t.Fatalf("total_transports = %d, want 30", summary.Total)
	}

	// The completeness stamp #4513 requires is present and honest.
	for _, k := range []string{"observed_at", "complete", "confidence", "trailing_peak_transports", "trailing_window_seconds"} {
		if _, present := published[k]; !present {
			t.Fatalf("published body is missing completeness field %q", k)
		}
	}
	if published["confidence"] != ConfidenceSettled || published["complete"] != true {
		t.Fatalf("first post-warmup sample should be settled+complete, got %v/%v",
			published["confidence"], published["complete"])
	}
}

// TestVersionStatsMatchesHTTPHandler pins the published histogram to
// the GET /version body.
func TestVersionStatsMatchesHTTPHandler(t *testing.T) {
	api := &API{uptimesCache: testUptimes()}
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

	rec := httptest.NewRecorder()
	api.getVersionStats(rec, httptest.NewRequest(http.MethodGet, "/version", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("handler status = %d, want 200", rec.Code)
	}
	var handlerBody map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &handlerBody); err != nil {
		t.Fatalf("handler body not JSON: %v", err)
	}

	sp, puts := newTestStatsPublisher(t, api, now.Add(-time.Hour))
	sp.publishVersions(now)

	p, ok := lastPutTo(*puts, StatsPathVersions)
	if !ok {
		t.Fatalf("nothing published to %s", StatsPathVersions)
	}
	published := decodePut(t, p)

	versions, ok := published["versions"].(map[string]interface{})
	if !ok {
		t.Fatalf("published versions is %T, want object", published["versions"])
	}
	if len(versions) != len(handlerBody) {
		t.Fatalf("published %d versions, handler emitted %d", len(versions), len(handlerBody))
	}
	for k, want := range handlerBody {
		if !jsonEqual(versions[k], want) {
			t.Fatalf("version %q: published %v, handler %v", k, versions[k], want)
		}
	}
	if versions["unknown"] == nil {
		t.Fatalf("empty version should be bucketed as \"unknown\", got %v", versions)
	}
}

func jsonEqual(a, b interface{}) bool {
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return string(ab) == string(bb)
}

// TestStatsPublisherWritesOnlyFixedPaths guards the property that makes
// stale-path retirement unnecessary here: the publisher writes exactly
// two paths, overwriting them, no matter how the underlying data
// changes. A leaf set that grew with the data (per-visor leaves, say)
// would need a Delete pass to retire what fell out; a fixed pair does
// not, and this fails the moment that stops being true.
func TestStatsPublisherWritesOnlyFixedPaths(t *testing.T) {
	api := &API{transportsCache: testEntries(20), uptimesCache: testUptimes()}
	start := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	sp, puts := newTestStatsPublisher(t, api, start.Add(-time.Hour))

	now := start
	for i := 0; i < 20; i++ {
		// Vary the data so a data-dependent path would show up.
		api.transportsCache = testEntries(20 + i)
		sp.publishNetwork(context.Background(), now)
		sp.publishVersions(now)
		now = now.Add(statsPublishInterval)
	}

	seen := make(map[string]int)
	for _, p := range *puts {
		seen[p.path]++
	}
	paths := make([]string, 0, len(seen))
	for p := range seen {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	want := []string{StatsPathNetwork, StatsPathVersions}
	if len(paths) != len(want) || paths[0] != want[0] || paths[1] != want[1] {
		t.Fatalf("published paths = %v, want exactly %v", paths, want)
	}
	if seen[StatsPathNetwork] != 20 || seen[StatsPathVersions] != 20 {
		t.Fatalf("expected 20 writes per path, got %v", seen)
	}
}

// TestCompletenessTrackerVerdicts walks the tracker through the #4513
// sawtooth: warm up, settle, get reset to a partial count, climb back.
func TestCompletenessTrackerVerdicts(t *testing.T) {
	start := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	c := newCompletenessTracker(start)

	// Inside the warmup window nothing certifies itself, even at a value
	// that would otherwise look like a plateau. This is the case that
	// matters: the publisher starts WITH TPD, so its first samples are
	// the refill.
	v := c.observe(start.Add(30*time.Second), 5000)
	if v.complete || v.confidence != ConfidenceWarmup {
		t.Fatalf("in-warmup sample: got %+v, want incomplete/warmup", v)
	}

	// Past warmup, climbing toward the plateau.
	now := start.Add(statsWarmup + time.Second)
	for _, val := range []int{7045, 8119, 9959, 10647} {
		v = c.observe(now, val)
		now = now.Add(statsPublishInterval)
	}
	if !v.complete || v.confidence != ConfidenceSettled {
		t.Fatalf("plateau sample: got %+v, want complete/settled", v)
	}
	if v.peak != 10647 {
		t.Fatalf("peak = %d, want 10647", v.peak)
	}

	// Ordinary churn around the plateau stays settled.
	v = c.observe(now, 10400)
	now = now.Add(statsPublishInterval)
	if !v.complete {
		t.Fatalf("1%% dip below peak should stay complete: %+v", v)
	}

	// TPD restarts: the aggregate is readable while it refills, so the
	// count collapses. That must NOT be certified.
	v = c.observe(now, 5200)
	now = now.Add(statsPublishInterval)
	if v.complete || v.confidence != ConfidenceRefilling {
		t.Fatalf("post-restart partial: got %+v, want incomplete/refilling", v)
	}
	if v.peak != 10647 {
		t.Fatalf("peak should survive the reset, got %d", v.peak)
	}

	// Climbing back through the middle is still partial…
	v = c.observe(now, 8000)
	now = now.Add(statsPublishInterval)
	if v.complete {
		t.Fatalf("mid-refill sample should not be complete: %+v", v)
	}
	// …until it reaches the plateau again.
	v = c.observe(now, 10500)
	if !v.complete || v.confidence != ConfidenceSettled {
		t.Fatalf("refilled sample: got %+v, want complete/settled", v)
	}
}

// TestCompletenessTrackerPeakDecays checks the tracker degrades
// gracefully rather than jamming: once the old peak ages out of the
// trailing window, a genuinely smaller network settles at its new size
// instead of being called partial forever.
func TestCompletenessTrackerPeakDecays(t *testing.T) {
	start := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	c := newCompletenessTracker(start)

	now := start.Add(statsWarmup + time.Second)
	c.observe(now, 10000)

	// Half the network leaves for good.
	now = now.Add(time.Minute)
	if v := c.observe(now, 5000); v.complete {
		t.Fatalf("immediately after a halving the sample is indistinguishable from a refill, must not be complete: %+v", v)
	}

	// Past the trailing window the old peak is gone and 5000 is the network.
	now = now.Add(statsTrailingWindow + time.Minute)
	v := c.observe(now, 5000)
	if !v.complete || v.peak != 5000 {
		t.Fatalf("after the window aged out: got %+v, want complete with peak 5000", v)
	}
}

// TestStatsPublisherHoldsLastCompleteSample is the #4513 guarantee at
// the feed level: while a complete sample stands, a partial one does
// not overwrite it — so a subscriber reads the last known-good numbers
// through a TPD restart instead of a sawtooth.
func TestStatsPublisherHoldsLastCompleteSample(t *testing.T) {
	api := &API{transportsCache: testEntries(1000)}
	start := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	sp, puts := newTestStatsPublisher(t, api, start.Add(-time.Hour))

	now := start
	sp.publishNetwork(context.Background(), now)
	if len(*puts) != 1 {
		t.Fatalf("expected the settled sample to publish, got %d puts", len(*puts))
	}
	if body := decodePut(t, (*puts)[0]); body["complete"] != true {
		t.Fatalf("first sample should be complete: %v", body)
	}

	// TPD restarts; the aggregate now reads a fraction of the set.
	api.transportsCache = testEntries(300)
	for i := 0; i < 5; i++ {
		now = now.Add(statsPublishInterval)
		sp.publishNetwork(context.Background(), now)
	}
	if len(*puts) != 1 {
		t.Fatalf("partial samples must not overwrite the held complete one; got %d puts", len(*puts))
	}

	// Past the holdover bound the partial goes out — marked partial. A
	// feed that froze forever on a real network change would be worse
	// than one that publishes the change with its verdict attached.
	now = now.Add(statsIncompleteHoldover)
	sp.publishNetwork(context.Background(), now)
	if len(*puts) != 2 {
		t.Fatalf("after the holdover the partial sample should publish; got %d puts", len(*puts))
	}
	body := decodePut(t, (*puts)[1])
	if body["complete"] != false || body["confidence"] != ConfidenceRefilling {
		t.Fatalf("held-over sample must be stamped incomplete/refilling, got %v/%v",
			body["complete"], body["confidence"])
	}
	if body["total_transports"] != float64(300) {
		t.Fatalf("held-over sample should carry the observed value, got %v", body["total_transports"])
	}

	// Recovery re-arms the hold.
	api.transportsCache = testEntries(1000)
	now = now.Add(statsPublishInterval)
	sp.publishNetwork(context.Background(), now)
	if len(*puts) != 3 {
		t.Fatalf("recovered sample should publish; got %d puts", len(*puts))
	}
	if body := decodePut(t, (*puts)[2]); body["complete"] != true {
		t.Fatalf("recovered sample should be complete: %v", body)
	}
	api.transportsCache = testEntries(300)
	now = now.Add(statsPublishInterval)
	sp.publishNetwork(context.Background(), now)
	if len(*puts) != 3 {
		t.Fatalf("hold should be re-armed after recovery; got %d puts", len(*puts))
	}
}

// TestStatsBodiesAreSmall is the premise of the whole feed: these
// republish every few seconds because they are tiny. If a body grows
// past a kilobyte gzipped, the cadence needs revisiting.
func TestStatsBodiesAreSmall(t *testing.T) {
	api := &API{transportsCache: testEntries(200), uptimesCache: testUptimes()}
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	sp, puts := newTestStatsPublisher(t, api, now.Add(-time.Hour))
	sp.publishNetwork(context.Background(), now)
	sp.publishVersions(now)

	for _, p := range *puts {
		if len(p.body) > 1024 {
			t.Fatalf("%s: gzipped body is %d bytes; this feed's cadence assumes sub-kilobyte", p.path, len(p.body))
		}
	}
}
