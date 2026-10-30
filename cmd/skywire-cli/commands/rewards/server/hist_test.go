// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/hist_test.go
package clirewardsserver

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestHistRewardDatesSkipsArchives(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{
		"2026-10-21_rewardtxn0.csv",
		"2026-10-20_rewardtxn0.csv",
		"2026-10-20.txt",
		"2026-10-20_pool1_rewardtxn0.csv",
		"2026-10-20_stats.txt",
		"pre-bwfix-20261023/2026-10-19_rewardtxn0.csv",
		"x2026-10-18_rewardtxn0.csv",
	} {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := histRewardDates(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"2026-10-20", "2026-10-21"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
