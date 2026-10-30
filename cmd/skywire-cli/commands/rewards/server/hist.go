// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/hist.go
package clirewardsserver

import (
	"os"
	"regexp"
)

// rewardTxnFile is a day's combined reward transaction, the broadcast input.
// The per-pool _pool1_rewardtxn0.csv and _pool2_rewardtxn0.csv do not match.
var rewardTxnFile = regexp.MustCompile(`^([0-9]{4}-[0-9]{2}-[0-9]{2})_rewardtxn0\.csv$`)

// histRewardDates lists the days that have a reward transaction in dir, oldest
// first. Only dir itself is read, so an archive folder inside it is never a day.
func histRewardDates(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var dates []string
	for _, e := range entries {
		if m := rewardTxnFile.FindStringSubmatch(e.Name()); m != nil && e.Type().IsRegular() {
			dates = append(dates, m[1])
		}
	}
	return dates, nil
}
