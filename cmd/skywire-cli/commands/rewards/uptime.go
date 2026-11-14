// Package clirewards cmd/skywire-cli/commands/rewards/uptime.go c4-vis-cli
package clirewards

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// minDailyUptime is the daily uptime percentage a visor needs for a reward.
// Source: rewards/mainnet_rules.md, "75% uptime per day minimum".
const minDailyUptime = 75.0

// uptimeBelowReason is the ineligibility reason for a visor under the minimum.
var uptimeBelowReason = fmt.Sprintf("Uptime below %g%%", minDailyUptime)

// uptimeKeys reads the "pk date pct" lines for date from an uptime file and
// splits the keys by whether they met minDailyUptime. An unreadable pct fails.
func uptimeKeys(path, date string) (met, below []string, err error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }() //nolint:errcheck

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 || fields[1] != date {
			continue
		}
		pct, perr := strconv.ParseFloat(strings.TrimSuffix(fields[2], "%"), 64)
		if perr != nil || pct < minDailyUptime {
			below = append(below, fields[0])
			continue
		}
		met = append(met, fields[0])
	}
	return met, below, sc.Err()
}
