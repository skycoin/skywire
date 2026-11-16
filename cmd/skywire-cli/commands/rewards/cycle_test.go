package clirewards

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
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
	wd := t.TempDir()
	f := &cycleFixture{
		utBody: fmt.Sprintf("%s %s 100\n", cycleTestPK, cycleTestDate),
		bwBody: fmt.Sprintf(`{%q: 4096}`, cycleTestPK),
	}
	f.o = runOpts
	f.o.date = cycleTestDate
	f.o.surveyPath = filepath.Join(wd, "log_backups")
	f.o.histDir = filepath.Join(wd, "hist")
	f.surveyPath = filepath.Join(f.o.surveyPath, cycleTestPK, "node-info.json")
	f.utTxt = filepath.Join(f.o.histDir, cycleTestDate+"_ut.txt")
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
	if err := os.MkdirAll(filepath.Dir(f.surveyPath), 0o750); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"public_key":%q,"skycoin_address":%q,"go_os":"linux","go_arch":"amd64","uuid":"u-1",`+
		`"ip_address":"1.2.3.4","ip_addr":[{"ifname":"eth0","address":"aa:bb:cc:dd:ee:ff"}],"skywire_version":"v1.3.112"}`, cycleTestPK, addr)
	if err := os.WriteFile(f.surveyPath, []byte(body), 0o600); err != nil {
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
	b, err := os.ReadFile(filepath.Join(f.o.histDir, cycleTestDate+"_rewardtxn0.csv"))
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
