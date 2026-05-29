package clirewards

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/logging"
)

const (
	tpdCacheFile   = "/tmp/tpd.json"
	tpdCacheMaxAge = 5 * time.Minute
	minTransports  = 2
)

// tpdPerKeyStatsURL returns the transport discovery per-key stats URL from the deployment config.
func tpdPerKeyStatsURL() string {
	base := deployment.Prod.TransportDiscovery
	if base == "" {
		base = "https://tpd.skywire.skycoin.com"
	}
	return strings.TrimRight(base, "/") + "/all-transports/per-key-stats"
}

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
	bwCollectCmd.Flags().IntVar(&bwDays, "days", 1, "TPD ?days= window to fetch (max 35); only the entries whose Date matches --date are counted")
	bwCollectCmd.Flags().StringVar(&bwDate, "date", "", "UTC date the output file is named for and the only Daily entry counted (default: today)")

	tpCollectCmd.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr, "RPC server address (env: SKYWIRE_RPC)")
	bwCollectCmd.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr, "RPC server address (env: SKYWIRE_RPC)")
	clirpc.RegisterFetchFlags(tpCollectCmd)
	clirpc.RegisterFetchFlags(bwCollectCmd)
}

const (
	defaultMinBandwidth = 64 // minimum bytes to qualify; real TPD data shows P5=796B for 2-transport visors
	// Bump the cache filename whenever the on-disk shape changes so a
	// v2 binary doesn't deserialize a leftover v1 cache as garbage.
	bwCacheFile = "/tmp/tpd_bandwidth_v2.json"
)

var (
	minTp        int
	showAll      bool
	noCache      bool
	histPath     string
	minBandwidth uint64
	bwSurveyPath string
	bwDays       int
	bwDate       string
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
		stats, err := fetchTPDData(cmd.Flags(), tpLog, noCache)
		if err != nil {
			tpLog.Fatal("Failed to fetch TPD data: ", err)
		}

		tpLog.Infof("Fetched transport stats for %d visors", len(stats))

		// Filter visors with sufficient p2p transports (excluding dmsg)
		// Only count transport types that represent direct peer-to-peer connections
		var qualifying []string
		for pk, counts := range stats {
			p2pCount := 0
			for tpType, count := range counts {
				if tpType != "total" && tpType != "dmsg" {
					p2pCount += count
				}
			}
			if p2pCount >= minTp {
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

// VisorBandwidthResult maps public key hex to daily bandwidth in bytes.
// This is the pre-v2 bandwidth.json shape — retained for backward
// compatibility when the reward calculator encounters a file written
// by an older bw-collect.
type VisorBandwidthResult map[string]uint64

// BandwidthDataVersion is the current bw-collect output schema version.
// v1 = bare {pk:bytes} map (no per-transport detail; full sent+recv
//
//	credited to BOTH edges; no counterparty eligibility filter
//	possible at calc time).
//
// v2 = {version:2, transports:[{a,b,sent_a,sent_b}]} — preserves
//
//	per-transport detail so the calculator can (a) drop transports
//	where either edge is reward-ineligible and (b) credit each edge
//	only for the bytes it actually sent (sender-pays model).
const BandwidthDataVersion = 2

// TransportBW is one bidirectional transport's per-edge sent bytes,
// each capped by the counterparty's reported recv (so a one-sided
// inflation of .sent can't pump the credit). sent_a is the bytes A
// sent that B confirmed receiving; sent_b is the mirror.
type TransportBW struct {
	A     string `json:"a"`
	B     string `json:"b"`
	SentA uint64 `json:"sent_a"`
	SentB uint64 `json:"sent_b"`
}

// BandwidthData is the v2 wire format for hist/<date>_bandwidth.json.
// See comment on BandwidthDataVersion for the migration story.
type BandwidthData struct {
	Version    int           `json:"version"`
	Transports []TransportBW `json:"transports"`
}

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
	Long: `Fetches per-transport bandwidth data from TPD and records the daily
per-edge sent totals so the reward calculator can credit each visor
only for the bytes it actually sent — and only when the sender itself
is reward-eligible. Counterparty eligibility is irrelevant under the
sender-pays model: an eligible visor's transport to an ineligible peer
still earns the eligible side credit for what it sent.

This command:
1. Fetches all transport metrics from TPD /metrics?days=1&bandwidth=true&edges=true
2. Builds a PK→IP map from hardware surveys to detect same-LAN transports
3. Excludes transports where both edges share the same external IP (same-LAN sybil)
4. For each remaining transport, records (a, b, sent_a, sent_b) where
   sent_a = min(A.Sent, B.Recv) and sent_b = min(B.Sent, A.Recv)
   — sender-pays, capped by the receiver's confirmation so a one-sided
   .sent inflation cannot pump credit
5. Writes hist/YYYY-MM-DD_bandwidth.json as the v2 BandwidthData schema
6. Caches the raw transport list in /tmp/tpd_bandwidth_v2.json (5-min TTL)

The minimum-bandwidth filter and counterparty-eligibility filter are
both applied later by the reward calculator — bw-collect does not have
the eligibility set at run time and the data is needed pre-filter for
sender-side aggregation. Designed to be run hourly by the reward service.`,
	Run: func(cmd *cobra.Command, _ []string) {
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

		targetDate := bwDate
		if targetDate == "" {
			targetDate = time.Now().UTC().Format("2006-01-02")
		}

		// Build PK→IP map from hardware surveys for same-LAN detection.
		// Same-LAN filtering belongs at this stage rather than calc time
		// because it depends on visor IP from the survey, not on reward
		// eligibility — a transport between two PKs on one host is
		// excluded regardless of whether either side ends up eligible.
		pkIPMap := buildPKtoIPMap(bwLog, bwSurveyPath)
		bwLog.Infof("Built PK→IP map with %d entries from %s", len(pkIPMap), bwSurveyPath)

		// Fetch per-transport sender-side bandwidth from TPD (same-LAN
		// already filtered out; only Daily entries matching targetDate
		// counted when targetDate is non-empty).
		transports, err := fetchTransportBandwidthFromTPD(cmd.Flags(), bwLog, pkIPMap, !noCache, bwDays, targetDate)
		if err != nil {
			bwLog.Fatal("Failed to fetch bandwidth data: ", err)
		}

		// Write the v2 bandwidth JSON
		out := BandwidthData{
			Version:    BandwidthDataVersion,
			Transports: transports,
		}
		dailyFile := fmt.Sprintf("%s/%s_bandwidth.json", histPath, targetDate)
		jsonData, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			bwLog.Fatal("Failed to marshal bandwidth data: ", err)
		}
		if err := os.WriteFile(dailyFile, jsonData, 0600); err != nil {
			bwLog.Fatal("Failed to write bandwidth file: ", err)
		}

		// Print summary stats. The "qualifying visors" count under the
		// old aggregated format is no longer meaningful here — eligibility
		// and min-bandwidth are now applied at calc time — so report the
		// raw transport count and total sender-side bytes instead.
		var totalSent uint64
		uniqueEdges := make(map[string]struct{})
		for _, tp := range transports {
			totalSent += tp.SentA + tp.SentB
			uniqueEdges[tp.A] = struct{}{}
			uniqueEdges[tp.B] = struct{}{}
		}
		fmt.Printf("Bandwidth collection complete: %d transports across %d unique visors, total sender-side bandwidth: %s, written to %s\n",
			len(transports), len(uniqueEdges), formatBytes(totalSent), dailyFile)
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

// fetchTransportBandwidthFromTPD fetches per-transport metrics from TPD,
// filters out same-LAN transports (both edges share an external IP), and
// returns the per-edge sender-side daily totals. Each entry's SentA is
// what edge A claims sent capped by what B confirmed receiving (and
// mirror for SentB) so a one-sided .sent inflation in TPD cannot pump
// a visor's credit.
//
// No counterparty-eligibility filter here — bw-collect doesn't have the
// pool 1 eligibility set at run time (it's recomputed during calc using
// the day's surveys + uptime). The calculator does that filter after
// reading this list.
func fetchTransportBandwidthFromTPD(cmdFlags *pflag.FlagSet, bwLog *logging.Logger, pkIPMap map[string]string, useCache bool, days int, dateFilter string) ([]TransportBW, error) {
	// Check cache
	if useCache {
		if info, err := os.Stat(bwCacheFile); err == nil {
			if time.Since(info.ModTime()) < tpdCacheMaxAge {
				bwLog.Debug("Using cached bandwidth data")
				data, err := os.ReadFile(bwCacheFile)
				if err == nil {
					var cached []TransportBW
					if err := json.Unmarshal(data, &cached); err == nil {
						return cached, nil
					}
					bwLog.WithError(err).Debug("Failed to parse cached bandwidth data, fetching fresh")
				}
			}
		}
	}

	// Fetch all transport metrics with edge info via visor RPC → DMSG → HTTP chain.
	tpdURL := strings.TrimSuffix(deployment.Prod.TransportDiscovery, "/")
	if days <= 0 {
		days = 1
	}
	url := fmt.Sprintf("%s/metrics?days=%d&bandwidth=true&latency=false&edges=true", tpdURL, days)

	bwLog.Info("Fetching all transport metrics from TPD: ", url)

	body, err := clirpc.FetchServiceURL(cmdFlags, url)
	if err != nil {
		return nil, fmt.Errorf("TPD fetch failed: %w", err)
	}

	var rawTransports []tpdTransport
	if err := json.Unmarshal(body, &rawTransports); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	bwLog.Infof("Fetched %d transports from TPD", len(rawTransports))

	out := make([]TransportBW, 0, len(rawTransports))
	sameLANCount := 0

	for _, tp := range rawTransports {
		if len(tp.Edges) != 2 {
			continue
		}

		// Sender-side per-edge totals. Mirrors the same three-branch
		// trust model `skywire cli tp metrics` uses (see
		// cmd/skywire-cli/commands/tp/tp-metrics.go:verifiedBandwidth):
		// when both edges report on a day, cap each side's sent by the
		// counterparty's recv; when only one side has reported (the
		// common case — edges re-register on different schedules), trust
		// that side's own counters. Without the unilateral-trust
		// branches, virtually every transport was discarded because at
		// any given moment only one edge has a fresh daily record.
		//
		// An edge whose record is present but {sent:0, recv:0} is
		// treated as "hasn't reported real data yet" rather than
		// "verified zero traffic" — same rationale as the cli.
		var sentA, sentB uint64
		for _, d := range tp.Daily {
			if dateFilter != "" && d.Date != dateFilter {
				continue
			}
			aReported := d.A != nil && (d.A.Sent > 0 || d.A.Recv > 0)
			bReported := d.B != nil && (d.B.Sent > 0 || d.B.Recv > 0)
			switch {
			case aReported && bReported:
				sentA += minUint64(d.A.Sent, d.B.Recv)
				sentB += minUint64(d.B.Sent, d.A.Recv)
			case aReported:
				sentA += d.A.Sent
				sentB += d.A.Recv
			case bReported:
				sentA += d.B.Recv
				sentB += d.B.Sent
			}
		}
		if sentA == 0 && sentB == 0 {
			continue
		}

		edgeA := tp.Edges[0]
		edgeB := tp.Edges[1]

		// Same-LAN: both edges report the same external IP from their
		// surveys. Drop the whole transport — neither side gets credit.
		ipA, hasA := pkIPMap[edgeA]
		ipB, hasB := pkIPMap[edgeB]
		if hasA && hasB && ipA == ipB {
			sameLANCount++
			bwLog.Debugf("Excluding same-LAN transport %s: %s and %s both on %s (sent_a=%d sent_b=%d)",
				tp.ID[:8], edgeA[:12], edgeB[:12], ipA, sentA, sentB)
			continue
		}

		out = append(out, TransportBW{
			A:     edgeA,
			B:     edgeB,
			SentA: sentA,
			SentB: sentB,
		})
	}

	bwLog.Infof("Excluded %d same-LAN transports, %d remaining in per-edge sender-side dataset",
		sameLANCount, len(out))

	// Update cache
	cacheData, err := json.Marshal(out)
	if err == nil {
		if err := os.WriteFile(bwCacheFile, cacheData, 0600); err != nil {
			bwLog.WithError(err).Warn("Failed to update bandwidth cache file")
		}
	}

	return out, nil
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
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
func fetchTPDData(cmdFlags *pflag.FlagSet, tpLog *logging.Logger, bypassCache bool) (PerKeyStats, error) {
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

	// Fetch fresh data via visor RPC → DMSG → HTTP chain.
	tpLog.Info("Fetching fresh TPD data from ", tpdPerKeyStatsURL())

	body, err := clirpc.FetchServiceURL(cmdFlags, tpdPerKeyStatsURL())
	if err != nil {
		return nil, fmt.Errorf("TPD fetch failed: %w", err)
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
