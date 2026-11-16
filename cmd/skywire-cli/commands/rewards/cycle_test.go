package clirewards

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/pflag"

	deprewards "github.com/skycoin/skywire/pkg/deployment/rewards"
	"github.com/skycoin/skywire/pkg/visor/rewardconfig"
)

const (
	cycleTestDate = "2026-11-10" // before the single address cutoff, so no node is asked
	cycleTestPK   = "02aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cycleTestAddr = "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6"
	cycleTestAlt  = "2iVtHS5ye99Km5PonsB42No3pQRGEURmxyc"
)

// cycleFixture is a work dir with one eligible survey and fetches that
// write the day's uptimes and bandwidth and count their calls.
type cycleFixture struct {
	o                 rewardOpts
	fetches, closes   int
	closeErr          error
	utBody, bwBody    string
	surveyPath, utTxt string
}

func newCycleFixture(t *testing.T) *cycleFixture {
	t.Helper()
	return newCycleFixtureAt(t, cycleTestDate)
}

func newCycleFixtureAt(t *testing.T, date string) *cycleFixture {
	t.Helper()
	wd := t.TempDir()
	f := &cycleFixture{
		utBody: fmt.Sprintf("%s %s 100\n", cycleTestPK, date),
		bwBody: fmt.Sprintf(`{%q: 4096}`, cycleTestPK),
	}
	f.o = runOpts
	f.o.date = date
	f.o.surveyPath = filepath.Join(wd, "log_backups")
	f.o.histDir = filepath.Join(wd, "hist")
	f.surveyPath = filepath.Join(f.o.surveyPath, cycleTestPK, "node-info.json")
	f.utTxt = filepath.Join(f.o.histDir, date+"_ut.txt")
	if err := os.MkdirAll(f.o.histDir, 0o750); err != nil {
		t.Fatal(err)
	}
	f.writeSurvey(t, cycleTestAddr)

	oldUT, oldBW, oldIndex := fetchUT, closeBW, xpubIndexPath
	t.Cleanup(func() { fetchUT, closeBW, xpubIndexPath = oldUT, oldBW, oldIndex })
	xpubIndexPath = filepath.Join(wd, "xpub_index.json")
	fetchUT = func(_ *pflag.FlagSet, date, histDir string) error {
		f.fetches++
		return os.WriteFile(filepath.Join(histDir, date+"_ut.txt"), []byte(f.utBody), 0o600)
	}
	closeBW = func(o rewardOpts, _ time.Time) error {
		f.closes++
		if f.closeErr != nil {
			return f.closeErr
		}
		return os.WriteFile(filepath.Join(o.histDir, o.date+"_bandwidth.json"), []byte(f.bwBody), 0o600)
	}
	return f
}

func (f *cycleFixture) writeSurvey(t *testing.T, addr string) {
	t.Helper()
	f.writeSurveyFor(t, cycleTestPK, addr)
}

func (f *cycleFixture) writeSurveyFor(t *testing.T, pk, addr string) {
	t.Helper()
	path := filepath.Join(f.o.surveyPath, pk, "node-info.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"public_key":%q,"skycoin_address":%q,"go_os":"linux","go_arch":"amd64","uuid":"u-1",`+
		`"ip_address":"1.2.3.4","ip_addr":[{"ifname":"eth0","address":"aa:bb:cc:dd:ee:ff"}],"skywire_version":"v1.3.112"}`, pk, addr)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *cycleFixture) calc(t *testing.T, d *dayCycle) cycleResult {
	t.Helper()
	cycleMu.Lock()
	defer cycleMu.Unlock()
	res, err := d.calcDay(f.o)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (f *cycleFixture) txn(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.o.histDir, f.o.date+"_rewardtxn0.csv"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A finished day's uptimes and bandwidth are fetched once, and unchanged
// inputs leave the outputs alone.
func TestCalcDayFetchesFinishedDayOnce(t *testing.T) {
	f := newCycleFixture(t)
	d := newDayCycle(nil, cycleConfig{})

	if res := f.calc(t, d); res.skipped || f.fetches != 1 || f.closes != 1 || res.hash == "" {
		t.Fatalf("first run %+v, %d fetches, %d closes", res, f.fetches, f.closes)
	}
	if !strings.Contains(f.txn(t), cycleTestAddr) {
		t.Fatalf("rewardtxn0.csv:\n%s", f.txn(t))
	}
	shares := filepath.Join(f.o.histDir, cycleTestDate+"_shares.csv")
	old := time.Date(2026, 11, 11, 1, 0, 0, 0, time.UTC)
	if err := os.Chtimes(shares, old, old); err != nil {
		t.Fatal(err)
	}

	res := f.calc(t, d)
	if !res.skipped || res.note != "inputs unchanged" || f.fetches != 1 || f.closes != 1 {
		t.Fatalf("second run %+v, %d fetches, %d closes", res, f.fetches, f.closes)
	}
	if fi, err := os.Stat(shares); err != nil || !fi.ModTime().Equal(old) {
		t.Fatalf("shares.csv was written again: %v %v", fi.ModTime(), err)
	}

	// An uptime file from before the day closed is fetched again.
	if err := os.Chtimes(f.utTxt, old.Add(-2*time.Hour), old.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if res := f.calc(t, d); !res.skipped || f.fetches != 2 {
		t.Fatalf("stale uptimes: %+v, %d fetches", res, f.fetches)
	}
}

// Before the day closed, the uptimes on disk are used and the bandwidth is not closed.
func TestCalcDayBeforeClose(t *testing.T) {
	f := newCycleFixture(t)
	if err := os.WriteFile(f.utTxt, []byte(f.utBody), 0o600); err != nil {
		t.Fatal(err)
	}
	d := newDayCycle(nil, cycleConfig{})
	d.now = func() time.Time { return d.closedAt(cycleTestDate).Add(-time.Minute) }
	if res := f.calc(t, d); res.skipped || f.fetches != 0 || f.closes != 0 {
		t.Fatalf("%+v, %d fetches, %d closes", res, f.fetches, f.closes)
	}
}

// A failed final bandwidth collection is tried again after closeRetry, not on every run.
func TestCalcDayCloseRetry(t *testing.T) {
	f := newCycleFixture(t)
	f.closeErr = errors.New("tpd down")
	d := newDayCycle(nil, cycleConfig{})
	now := d.closedAt(cycleTestDate).Add(time.Hour)
	d.now = func() time.Time { return now }
	f.calc(t, d)
	now = now.Add(time.Minute)
	f.calc(t, d)
	if f.closes != 1 {
		t.Fatalf("%d closes within the retry delay", f.closes)
	}
	now = now.Add(closeRetry)
	f.closeErr = nil
	f.calc(t, d)
	if f.closes != 2 {
		t.Fatalf("%d closes after the retry delay", f.closes)
	}
}

func TestCalcDayRecalcsOnChangedAddress(t *testing.T) {
	f := newCycleFixture(t)
	d := newDayCycle(nil, cycleConfig{})
	first := f.calc(t, d)
	f.writeSurvey(t, cycleTestAlt)
	res := f.calc(t, d)
	if res.skipped || res.hash == first.hash || !strings.Contains(f.txn(t), cycleTestAlt) {
		t.Fatalf("%+v after the address changed:\n%s", res, f.txn(t))
	}
}

func TestCalcDayPaid(t *testing.T) {
	f := newCycleFixture(t)
	if err := os.WriteFile(filepath.Join(f.o.histDir, cycleTestDate+".txt"), []byte("txid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := newDayCycle(nil, cycleConfig{})
	if res := f.calc(t, d); !res.skipped || res.note != "paid" || f.fetches != 0 || f.closes != 0 {
		t.Fatalf("%+v, %d fetches, %d closes", res, f.fetches, f.closes)
	}
}

// A payout address that sends coins is seen by the watch on the next block,
// and the recalculation that follows leaves it out. Until then the hash
// reads the cache and asks the node nothing.
func TestSpentAddressTriggersRecalc(t *testing.T) {
	const otherPK = "02bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	date := deprewards.SingleAddressCutoff.Format("2006-01-02")
	f := newCycleFixtureAt(t, date)
	f.utBody += fmt.Sprintf("%s %s 100\n", otherPK, date)
	f.writeSurveyFor(t, otherPK, cycleTestAlt)

	var spent atomic.Bool
	var queries atomic.Int32
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries.Add(1)
		if r.URL.Query().Get("address") == cycleTestAddr && spent.Load() {
			w.Write([]byte(`[{"coins":1000000,"spent_block_seq":9}]`)) //nolint:errcheck,gosec
			return
		}
		w.Write([]byte(`[{"coins":1000000,"spent_block_seq":0}]`)) //nolint:errcheck,gosec
	}))
	defer node.Close()
	oldURL, oldHead := skycoinNodeURL, chainHead
	t.Cleanup(func() { skycoinNodeURL, chainHead = oldURL, oldHead })
	skycoinNodeURL = node.URL
	var head atomic.Uint64
	head.Store(100)
	chainHead = func(context.Context, string) (uint64, error) { return head.Load(), nil }
	spentCache.Lock()
	spentCache.m = map[string]bool{}
	spentCache.Unlock()

	d := newDayCycle(nil, cycleConfig{})
	d.watching = true
	if res := f.calc(t, d); res.skipped || !strings.Contains(f.txn(t), cycleTestAddr) {
		t.Fatalf("first run %+v:\n%s", res, f.txn(t))
	}
	if changed, err := d.watchSpent(context.Background(), node.URL); changed || err != nil {
		t.Fatalf("first watch: %v %v", changed, err)
	}
	n := queries.Load()
	if res := f.calc(t, d); !res.skipped || queries.Load() != n {
		t.Fatalf("unchanged run %+v, %d node queries", res, queries.Load()-n)
	}
	// The same block: nothing to look at.
	if changed, err := d.watchSpent(context.Background(), node.URL); changed || err != nil || queries.Load() != n {
		t.Fatalf("same block: %v %v, %d node queries", changed, err, queries.Load()-n)
	}

	spent.Store(true)
	head.Store(101)
	if changed, err := d.watchSpent(context.Background(), node.URL); !changed || err != nil {
		t.Fatalf("new block with a spend: %v %v", changed, err)
	}
	res := f.calc(t, d)
	if res.skipped || strings.Contains(f.txn(t), cycleTestAddr) || !strings.Contains(f.txn(t), cycleTestAlt) {
		t.Fatalf("after the spend %+v:\n%s", res, f.txn(t))
	}
	// The spent address is no longer watched.
	if d.watch.addrs[0] != cycleTestAlt || len(d.watch.addrs) != 1 {
		t.Fatalf("watched %q", d.watch.addrs)
	}
}

// When the node does not tell its last block, the addresses are checked
// every watchFallback.
func TestWatchSpentWithoutHead(t *testing.T) {
	oldHead, oldExp := chainHead, addressExposure
	t.Cleanup(func() { chainHead, addressExposure = oldHead, oldExp })
	chainHead = func(context.Context, string) (uint64, error) { return 0, errors.New("no metadata") }
	var checks int
	addressExposure = func(_, a string) (rewardconfig.AddressExposure, error) {
		checks++
		return rewardconfig.AddressExposure{Address: a}, nil
	}
	spentCache.Lock()
	spentCache.m = map[string]bool{}
	spentCache.Unlock()
	d := newDayCycle(nil, cycleConfig{})
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return now }
	d.setWatch("2026-11-15", []string{cycleTestAddr})
	d.watchSpent(context.Background(), "") //nolint:errcheck,gosec
	now = now.Add(time.Minute)
	d.watchSpent(context.Background(), "") //nolint:errcheck,gosec
	if checks != 1 {
		t.Fatalf("%d checks within the fallback", checks)
	}
	now = now.Add(watchFallback)
	d.watchSpent(context.Background(), "") //nolint:errcheck,gosec
	if checks != 2 {
		t.Fatalf("%d checks after the fallback", checks)
	}
}

func TestSurveyChanged(t *testing.T) {
	base := `{"public_key":"pk","skycoin_address":"%s","ip_address":"1.2.3.4","skywire_version":"%s","timestamp":"%s"}`
	s := func(addr, ver, ts string) []byte { return fmt.Appendf(nil, base, addr, ver, ts) }
	old := s(cycleTestAddr, "v1.3.112", "t1")
	for name, c := range map[string]struct {
		old, new []byte
		want     bool
	}{
		"first survey":     {nil, old, true},
		"timestamp only":   {old, s(cycleTestAddr, "v1.3.112", "t2"), false},
		"reward address":   {old, s(cycleTestAlt, "v1.3.112", "t1"), true},
		"skywire version":  {old, s(cycleTestAddr, "v1.3.113", "t1"), true},
		"ip address":       {old, []byte(strings.Replace(string(old), "1.2.3.4", "1.2.3.5", 1)), true},
		"unreadable again": {[]byte("x"), []byte("y"), false},
	} {
		if got := surveyChanged(c.old, c.new); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}
