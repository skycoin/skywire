package clirewards

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

const (
	tpdCacheFile      = "/tmp/tpd.json"
	tpdCacheMaxAge    = 5 * time.Minute
	tpdPerKeyStatsURL = "https://tpd.skywire.skycoin.com/all-transports/per-key-stats"
	minTransports     = 2
)

// PerKeyStats is a map from PK hex to transport counts by type (includes "total" key)
// Format: {"pk1": {"total": 15, "stcpr": 1, "sudph": 14}, ...}
type PerKeyStats map[string]map[string]int

func init() {
	RootCmd.AddCommand(
		tpCollectCmd,
	)
	tpCollectCmd.Flags().SortFlags = false
	tpCollectCmd.Flags().StringVarP(&logLvl, "loglvl", "s", "info", "[ debug | warn | error | fatal | panic | trace ]")
	tpCollectCmd.Flags().IntVarP(&minTp, "min", "m", minTransports, "minimum transports required")
	tpCollectCmd.Flags().BoolVarP(&showAll, "all", "a", false, "show all visors with transports (not just those meeting minimum)")
	tpCollectCmd.Flags().BoolVarP(&noCache, "no-cache", "n", false, "bypass cache and fetch fresh data")
	tpCollectCmd.Flags().StringVarP(&histPath, "hist", "p", "hist", "path to history directory for daily files")
}

var (
	minTp    int
	showAll  bool
	noCache  bool
	histPath string
)

var tpCollectCmd = &cobra.Command{
	Use:   "tp-collect",
	Short: "collect transport data and track visors with sufficient transports",
	Long: `Fetches transport data from TPD and tracks which visors have sufficient transports.

This command:
1. Fetches per-key transport stats from TPD (cached for 5 minutes in /tmp/tpd.json)
2. Identifies visors with at least the minimum required transports (default: 2)
3. Appends qualifying public keys to a daily file in the hist/ directory

The daily file format is: hist/YYYY-MM-DD_transports.txt
Each line contains a public key that had sufficient transports at the time of collection.

This is designed to be run hourly by the reward service.`,
	Run: func(cmd *cobra.Command, _ []string) {
		tpLog := logging.MustGetLogger("tp-collect")
		if logLvl != "" {
			if lvl, err := logging.LevelFromString(logLvl); err == nil {
				logging.SetLevel(lvl)
			}
		}

		// Fetch TPD data (with caching)
		stats, err := fetchTPDData(tpLog, noCache)
		if err != nil {
			tpLog.Fatal("Failed to fetch TPD data: ", err)
		}

		tpLog.Infof("Fetched transport stats for %d visors", len(stats))

		// Filter visors with sufficient transports
		var qualifying []string
		for pk, counts := range stats {
			if total, ok := counts["total"]; ok && total >= minTp {
				qualifying = append(qualifying, pk)
			}
		}

		sort.Strings(qualifying)

		tpLog.Infof("Found %d visors with >= %d transports", len(qualifying), minTp)

		if showAll {
			fmt.Println("Public Keys with sufficient transports:")
			for _, pk := range qualifying {
				fmt.Println(pk)
			}
			return
		}

		// Ensure hist directory exists
		if err := os.MkdirAll(histPath, 0750); err != nil {
			tpLog.Fatal("Failed to create hist directory: ", err)
		}

		// Write to daily file
		today := time.Now().UTC().Format("2006-01-02")
		dailyFile := fmt.Sprintf("%s/%s_transports.txt", histPath, today)

		// Read existing entries to avoid duplicates
		existing := make(map[string]struct{})
		if data, err := os.ReadFile(dailyFile); err == nil { //nolint:gosec
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if line != "" {
					existing[line] = struct{}{}
				}
			}
		}

		// Append new qualifying PKs
		file, err := os.OpenFile(dailyFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600) //nolint:gosec
		if err != nil {
			tpLog.Fatal("Failed to open daily file: ", err)
		}
		defer file.Close() //nolint:errcheck

		newCount := 0
		for _, pk := range qualifying {
			if _, exists := existing[pk]; !exists {
				if _, err := file.WriteString(pk + "\n"); err != nil {
					tpLog.WithError(err).Error("Failed to write PK to daily file")
					continue
				}
				newCount++
			}
		}

		tpLog.Infof("Added %d new entries to %s (total unique: %d)", newCount, dailyFile, len(existing)+newCount)
		fmt.Printf("Transport collection complete: %d qualifying visors, %d new entries added to %s\n",
			len(qualifying), newCount, dailyFile)
	},
}

// fetchTPDData fetches transport per-key stats from TPD, using cache if valid
func fetchTPDData(tpLog *logging.Logger, bypassCache bool) (PerKeyStats, error) {
	// Check cache
	if !bypassCache {
		if info, err := os.Stat(tpdCacheFile); err == nil {
			if time.Since(info.ModTime()) < tpdCacheMaxAge {
				tpLog.Debug("Using cached TPD data")
				data, err := os.ReadFile(tpdCacheFile)
				if err == nil {
					var stats PerKeyStats
					if err := json.Unmarshal(data, &stats); err == nil {
						return stats, nil
					}
					tpLog.WithError(err).Debug("Failed to parse cached data, fetching fresh")
				}
			}
		}
	}

	// Fetch fresh data
	tpLog.Info("Fetching fresh TPD data from ", tpdPerKeyStatsURL)

	//nolint:gosec
	resp, err := http.Get(tpdPerKeyStatsURL)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("TPD returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var stats PerKeyStats
	if err := json.Unmarshal(body, &stats); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Update cache
	if err := os.WriteFile(tpdCacheFile, body, 0600); err != nil {
		tpLog.WithError(err).Warn("Failed to update cache file")
	}

	return stats, nil
}
