//go:build !tinygo

package clirewards

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCycleOpts(t *testing.T) {
	wd, abs := t.TempDir(), filepath.Join(t.TempDir(), "hist")
	o := cycleOpts(wd, rewardOpts{surveyPath: "log_backups", histDir: abs})
	if o.surveyPath != filepath.Join(wd, "log_backups") || o.histDir != abs {
		t.Fatalf("paths %q %q", o.surveyPath, o.histDir)
	}
	if want := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02"); o.date != want {
		t.Fatalf("date %q, want %q", o.date, want)
	}
}

func TestContainedCycle(t *testing.T) {
	wd := t.TempDir()
	o := cycleOpts(wd, rewardOpts{surveyPath: "log_backups", histDir: "hist"})
	ut := filepath.Join(wd, "ut.txt")
	if err := os.WriteFile(ut, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	d := newDayCycle(runCmd.Flags(), cycleConfig{skipCollect: true, utFile: ut})

	// No surveys: the calculation logs a Fatal, which must come back as an error.
	_, err := containedCycle(d, o, "")
	if err == nil || !strings.Contains(err.Error(), "the path to the surveys does not exist") {
		t.Fatalf("got %v", err)
	}

	// A paid date ends the cycle before the calculation.
	if err := os.WriteFile(filepath.Join(o.histDir, o.date+".txt"), []byte("txid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if res, err := containedCycle(d, o, ""); err != nil || res.note != "paid" {
		t.Fatalf("paid date: %+v %v", res, err)
	}
}
