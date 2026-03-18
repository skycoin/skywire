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

	"github.com/skycoin/skywire/deployment"
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
		bwCollectCmd,
	)
	tpCollectCmd.Flags().SortFlags = false
	tpCollectCmd.Flags().StringVarP(&logLvl, "loglvl", "s", "info", "[ debug | warn | error | fatal | panic | trace ]")
	tpCollectCmd.Flags().IntVarP(&minTp, "min", "m", minTransports, "minimum transports required")
	tpCollectCmd.Flags().BoolVarP(&showAll, "all", "a", false, "show all visors with transports (not just those meeting minimum)")
	tpCollectCmd.Flags().BoolVarP(&noCache, "no-cache", "n", false, "bypass cache and fetch fresh data")
	tpCollectCmd.Flags().StringVarP(&histPath, "hist", "p", "hist", "path to history directory for daily files")

	bwCollectCmd.Flags().SortFlags = false
	bwCollectCmd.Flags().StringVarP(&logLvl, "loglvl", "s", "info", "[ debug | warn | error | fatal | panic | trace ]")
	bwCollectCmd.Flags().BoolVarP(&noCache, "no-cache", "n", false, "bypass cache and fetch fresh data")
	bwCollectCmd.Flags().StringVarP(&histPath, "hist", "p", "hist", "path to history directory for daily files")
	bwCollectCmd.Flags().Uint64VarP(&minBandwidth, "min-bw", "b", defaultMinBandwidth, "minimum bandwidth in bytes to qualify")
	bwCollectCmd.Flags().StringVarP(&bwSurveyPath, "lpath", "l", "log_collecting", "path to hardware surveys (for same-LAN detection)")
}

const (
	defaultMinBandwidth = 64 // minimum bytes to qualify; real TPD data shows P5=796B for 2-transport visors
	bwCacheFile         = "/tmp/tpd_bandwidth.json"
)

var (
	minTp        int
	showAll      bool
	noCache      bool
	histPath     string
	minBandwidth uint64
	bwSurveyPath string
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

// oldFormatEntry represents the old TPD per-key-stats format
type oldFormatEntry struct {
	PK     string         `json:"pk"`
	Total  int            `json:"total"`
	ByType map[string]int `json:"by_type"`
}

// parsePerKeyStats tries to parse data as new format, falling back to old format
func parsePerKeyStats(data []byte) (PerKeyStats, error) {
	// Try new format first: {"pk1": {"total": N, "type": N}, ...}
	var stats PerKeyStats
	if err := json.Unmarshal(data, &stats); err == nil {
		return stats, nil
	}

	// Fallback to old format: [{"pk":"...", "total":N, "by_type":{...}}, ...]
	var oldStats []oldFormatEntry
	if err := json.Unmarshal(data, &oldStats); err != nil {
		return nil, fmt.Errorf("failed to parse as new or old format: %w", err)
	}

	// Convert old format to new format
	stats = make(PerKeyStats, len(oldStats))
	for _, entry := range oldStats {
		counts := make(map[string]int, len(entry.ByType)+1)
		counts["total"] = entry.Total
		for tpType, count := range entry.ByType {
			counts[tpType] = count
		}
		stats[entry.PK] = counts
	}

	return stats, nil
}

// VisorBandwidthResult maps public key hex to daily bandwidth in bytes
type VisorBandwidthResult map[string]uint64

// tpdTransport represents a transport from TPD /metrics endpoint with edge info
type tpdTransport struct {
	ID    string   `json:"id"`
	Type  string   `json:"type"`
	Live  bool     `json:"live"`
	Edges []string `json:"edges"`
	Daily []struct {
		Date string `json:"date"`
		A    *struct {
			Sent uint64 `json:"sent"`
			Recv uint64 `json:"recv"`
		} `json:"a,omitempty"`
		B *struct {
			Sent uint64 `json:"sent"`
			Recv uint64 `json:"recv"`
		} `json:"b,omitempty"`
	} `json:"daily"`
}

var bwCollectCmd = &cobra.Command{
	Use:   "bw-collect",
	Short: "collect bandwidth data from TPD for reward calculation",
	Long: `Fetches per-visor bandwidth data from TPD and records daily bandwidth.

This command:
1. Fetches all transport metrics from TPD /metrics?days=1&bandwidth=true&edges=true
2. Builds a PK→IP map from hardware surveys to detect same-LAN transports
3. Excludes bandwidth from transports where both edges share the same external IP
4. Aggregates remaining bandwidth per visor
5. Writes hist/YYYY-MM-DD_bandwidth.json as map[string]uint64 (pk → daily bytes)
6. Caches results in /tmp/tpd_bandwidth.json with 5-min TTL

Visors below the minimum bandwidth threshold are excluded.
Designed to be run hourly by the reward service.`,
	Run: func(_ *cobra.Command, _ []string) {
		bwLog := logging.MustGetLogger("bw-collect")
		if logLvl != "" {
			if lvl, err := logging.LevelFromString(logLvl); err == nil {
				logging.SetLevel(lvl)
			}
		}

		// Ensure hist directory exists
		if err := os.MkdirAll(histPath, 0750); err != nil {
			bwLog.Fatal("Failed to create hist directory: ", err)
		}

		today := time.Now().UTC().Format("2006-01-02")

		// Build PK→IP map from hardware surveys for same-LAN detection
		pkIPMap := buildPKtoIPMap(bwLog, bwSurveyPath)
		bwLog.Infof("Built PK→IP map with %d entries from %s", len(pkIPMap), bwSurveyPath)

		// Fetch bandwidth data from TPD with same-LAN filtering
		bwData, err := fetchVisorBandwidthFromTPD(bwLog, pkIPMap, !noCache)
		if err != nil {
			bwLog.Fatal("Failed to fetch bandwidth data: ", err)
		}

		// Filter by minimum bandwidth threshold
		qualifying := make(VisorBandwidthResult)
		for pk, bw := range bwData {
			if bw >= minBandwidth {
				qualifying[pk] = bw
			}
		}

		bwLog.Infof("Total visors with bandwidth: %d, qualifying (>= %d bytes): %d",
			len(bwData), minBandwidth, len(qualifying))

		// Write bandwidth JSON
		dailyFile := fmt.Sprintf("%s/%s_bandwidth.json", histPath, today)
		jsonData, err := json.MarshalIndent(qualifying, "", "  ")
		if err != nil {
			bwLog.Fatal("Failed to marshal bandwidth data: ", err)
		}
		if err := os.WriteFile(dailyFile, jsonData, 0600); err != nil {
			bwLog.Fatal("Failed to write bandwidth file: ", err)
		}

		// Print summary stats
		var totalBW uint64
		for _, bw := range qualifying {
			totalBW += bw
		}
		fmt.Printf("Bandwidth collection complete: %d qualifying visors, total bandwidth: %s, written to %s\n",
			len(qualifying), formatBytes(totalBW), dailyFile)
	},
}

// buildPKtoIPMap reads hardware surveys and returns a map of PK hex → external IP
func buildPKtoIPMap(bwLog *logging.Logger, surveyPath string) map[string]string {
	pkIPMap := make(map[string]string)

	entries, err := os.ReadDir(surveyPath)
	if err != nil {
		bwLog.Warnf("Cannot read survey directory %s: %v", surveyPath, err)
		return pkIPMap
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pk := entry.Name()
		if len(pk) != 66 { // standard PK hex length
			continue
		}
		nodeInfoPath := fmt.Sprintf("%s/%s/node-info.json", surveyPath, pk)
		data, err := os.ReadFile(nodeInfoPath) //nolint:gosec
		if err != nil {
			continue
		}
		var nodeInfo struct {
			IPAddress string `json:"ip_address"`
		}
		if err := json.Unmarshal(data, &nodeInfo); err != nil {
			continue
		}
		ip := strings.TrimSpace(nodeInfo.IPAddress)
		if ip != "" && strings.Count(ip, ".") == 3 {
			pkIPMap[pk] = ip
		}
	}

	return pkIPMap
}

// fetchVisorBandwidthFromTPD fetches all transport metrics from TPD,
// filters out same-LAN transports (both edges share an IP), and aggregates per visor
func fetchVisorBandwidthFromTPD(bwLog *logging.Logger, pkIPMap map[string]string, useCache bool) (VisorBandwidthResult, error) {
	// Check cache
	if useCache {
		if info, err := os.Stat(bwCacheFile); err == nil {
			if time.Since(info.ModTime()) < tpdCacheMaxAge {
				bwLog.Debug("Using cached bandwidth data")
				data, err := os.ReadFile(bwCacheFile)
				if err == nil {
					var cached VisorBandwidthResult
					if err := json.Unmarshal(data, &cached); err == nil {
						return cached, nil
					}
					bwLog.WithError(err).Debug("Failed to parse cached bandwidth data, fetching fresh")
				}
			}
		}
	}

	// Fetch all transport metrics with edge info
	tpdURL := strings.TrimSuffix(deployment.Prod.TransportDiscovery, "/")
	url := fmt.Sprintf("%s/metrics?days=1&bandwidth=true&latency=false&edges=true", tpdURL)

	bwLog.Info("Fetching all transport metrics from TPD: ", url)

	//nolint:gosec
	resp, err := http.Get(url)
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

	var transports []tpdTransport
	if err := json.Unmarshal(body, &transports); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	bwLog.Infof("Fetched %d transports from TPD", len(transports))

	// Aggregate bandwidth per visor, excluding same-LAN transports
	result := make(VisorBandwidthResult)
	sameLANCount := 0

	for _, tp := range transports {
		if len(tp.Edges) != 2 {
			continue
		}

		// Calculate total bandwidth for this transport
		var tpBW uint64
		for _, d := range tp.Daily {
			if d.A != nil {
				tpBW += d.A.Sent + d.A.Recv
			}
			if d.B != nil {
				tpBW += d.B.Sent + d.B.Recv
			}
		}
		if tpBW == 0 {
			continue
		}

		edgeA := tp.Edges[0]
		edgeB := tp.Edges[1]

		// Check if both edges are on the same LAN (same external IP)
		ipA, hasA := pkIPMap[edgeA]
		ipB, hasB := pkIPMap[edgeB]
		if hasA && hasB && ipA == ipB {
			sameLANCount++
			bwLog.Debugf("Excluding same-LAN transport %s: %s and %s both on %s (%d bytes)",
				tp.ID[:8], edgeA[:12], edgeB[:12], ipA, tpBW)
			continue
		}

		// Credit bandwidth to both edges
		result[edgeA] += tpBW
		result[edgeB] += tpBW
	}

	bwLog.Infof("Excluded %d same-LAN transports, %d visors with bandwidth remaining",
		sameLANCount, len(result))

	// Update cache
	cacheData, err := json.Marshal(result)
	if err == nil {
		if err := os.WriteFile(bwCacheFile, cacheData, 0600); err != nil {
			bwLog.WithError(err).Warn("Failed to update bandwidth cache file")
		}
	}

	return result, nil
}

// formatBytes converts bytes to a human-readable string
func formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
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
					stats, err := parsePerKeyStats(data)
					if err == nil {
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

	stats, err := parsePerKeyStats(body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Update cache
	if err := os.WriteFile(tpdCacheFile, body, 0600); err != nil {
		tpLog.WithError(err).Warn("Failed to update cache file")
	}

	return stats, nil
}
