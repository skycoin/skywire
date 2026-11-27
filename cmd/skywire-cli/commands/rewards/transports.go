// Package clirewards cmd/skywire-cli/commands/rewards/transports.go c4-vis-cli
package clirewards

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/cmdutil"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cliout"
	"github.com/skycoin/skywire/pkg/cliout/clirewards"
	tpdstore "github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/logging"
)

const (
	tpdCacheFile   = "/tmp/tpd.json"
	tpdCacheMaxAge = 5 * time.Minute
	minTransports  = 2
)

// tpdPerKeyStatsURL returns the transport discovery per-key stats URL from the deployment config.
func tpdPerKeyStatsURL() string {
	// No hardcoded clearnet fallback — services-config.json (via deployment.Prod)
	// is the single source of truth for the transport-discovery URL.
	return strings.TrimRight(deployment.Prod.TransportDiscovery, "/") + "/all-transports/per-key-stats"
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
	bwCollectCmd.Flags().StringVarP(&bwSurveyPath, "lpath", "l", "log_backups", "path to hardware surveys (for same-LAN detection)")
	bwCollectCmd.Flags().IntVar(&bwDays, "days", 1, "TPD ?days= window to fetch (max 35); only the entries whose Date matches --date are counted")
	bwCollectCmd.Flags().StringVar(&bwDate, "date", "", "UTC date the output file is named for and the only Daily entry counted (default: today)")

	tpCollectCmd.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr, "RPC server address (env: SKYWIRE_RPC)")
	bwCollectCmd.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr, "RPC server address (env: SKYWIRE_RPC)")
	clirpc.RegisterFetchFlags(tpCollectCmd)
	clirpc.RegisterFetchFlags(bwCollectCmd)
}

const defaultMinBandwidth = 64 // minimum bytes to qualify; real TPD data shows P5=796B for 2-transport visors

// bwCacheFile and fetchServiceURL are variables so tests can replace them.
var (
	// Bump the cache filename whenever the on-disk shape changes so a
	// v2 binary doesn't deserialize a leftover v1 cache as garbage.
	bwCacheFile     = "/tmp/tpd_bandwidth_v2.json"
	fetchServiceURL = clirpc.FetchServiceURL
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

		slices.Sort(qualifying)

		tpLog.Infof("Found %d visors with >= %d transports", len(qualifying), minTp)

		if showAll {
			internal.Catch(cmd.Flags(), cliout.Print(cmd, clirewards.QualifyingVisors{
				MinTransports: minTp, PubKeys: qualifying,
			}))
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
			for line := range strings.SplitSeq(string(data), "\n") {
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
		internal.Catch(cmd.Flags(), cliout.Print(cmd, clirewards.QualifyingVisors{
			MinTransports: minTp, PubKeys: qualifying,
			Added: newCount, Path: dailyFile,
		}))
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
		maps.Copy(counts, entry.ByType)
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
	ID    string                        `json:"id"`
	Type  string                        `json:"type"`
	Live  bool                          `json:"live"`
	Edges []string                      `json:"edges"`
	Daily []tpdstore.DailyEdgeBandwidth `json:"daily"`
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
2. Reads each visor's dmsg-attested IP and machine keys from the hardware surveys
3. Excludes transports whose edges share an IP or a machine (same-host sybil)
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
		if err := collectBandwidth(cmd.Flags(), bwLog, nil); err != nil {
			bwLog.Fatal(err)
		}
	},
}

// collectBandwidth writes hist/<date>_bandwidth.json. `rewards run` keeps
// going when it fails, and the calculation then has pool 2 repeat pool 1.
func collectBandwidth(flags *pflag.FlagSet, bwLog *logging.Logger, local tpdMetricsLookup) error {
	if err := os.MkdirAll(histPath, 0750); err != nil {
		return fmt.Errorf("create hist directory: %w", err)
	}

	targetDate := bwDate
	if targetDate == "" {
		targetDate = time.Now().UTC().Format("2006-01-02")
	}

	// A transport between two visors on one host is excluded whether or
	// not either side ends up eligible, so this runs before the calculation.
	day, err := time.Parse("2006-01-02", targetDate)
	if err != nil {
		return fmt.Errorf("parse date %q: %w", targetDate, err)
	}
	hostKeys := hostKeysByPK(bwLog, bwSurveyPath, day)
	bwLog.Infof("Read host keys for %d visors from %s", len(hostKeys), bwSurveyPath)

	transports, confirmed, err := fetchTransportBandwidthFromTPD(flags, bwLog, hostKeys, !noCache, bwDays, targetDate, local.reader())
	if err != nil {
		return fmt.Errorf("fetch bandwidth data: %w", err)
	}
	if len(confirmed) > 0 {
		if err := transportsConfirmedWrite(targetDate, confirmed); err != nil {
			return err
		}
	}

	// An empty answer is never a real day: TPD always has transports
	// moving bytes. It comes from a source with nothing to serve yet,
	// such as a CXO cache that has not synced, and written out it
	// stands as that day's data. Keep whatever file the day already
	// has and exit cleanly, so the reward run that follows still goes
	// ahead on it, and a later run can collect again.
	dailyFile := fmt.Sprintf("%s/%s_bandwidth.json", histPath, targetDate)
	if len(transports) == 0 {
		bwLog.Warnf("TPD returned no transports with bandwidth for %s; not writing %s", targetDate, dailyFile)
		return nil
	}

	// Write the v2 bandwidth JSON
	out := BandwidthData{
		Version:    BandwidthDataVersion,
		Transports: transports,
	}
	if err := writeAtomic(dailyFile, 0600, func(w io.Writer) error { return writeBandwidthData(w, out) }); err != nil {
		return fmt.Errorf("write bandwidth file: %w", err)
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
	cmdutil.Printf("Bandwidth collection complete: %d transports across %d unique visors, total sender-side bandwidth: %s, written to %s\n",
		len(transports), len(uniqueEdges), formatBytes(totalSent), dailyFile)
	return nil
}

// hostKeysByPK reads the hardware surveys and returns, per PK hex, the keys
// of the host the visor runs on: its dmsg-attested IP and its machine keys.
func hostKeysByPK(bwLog *logging.Logger, surveyPath string, day time.Time) map[string][]string {
	hostKeys := make(map[string][]string)

	entries, err := os.ReadDir(surveyPath)
	if err != nil {
		bwLog.Warnf("Cannot read survey directory %s: %v", surveyPath, err)
		return hostKeys
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pk := entry.Name()
		if len(pk) != 66 { // standard PK hex length
			continue
		}
		survey, err := parseSurvey(fmt.Sprintf("%s/%s/node-info.json", surveyPath, pk))
		if err != nil {
			continue
		}
		var keys []string
		ip, ipErr := surveyIP(survey, pk, day)
		ip = strings.TrimSpace(ip)
		if ipErr == nil && ip != "" {
			keys = append(keys, "ip:"+ip)
		}
		_, macs := extractInterfaces(survey)
		mac := ""
		if len(macs) > 0 {
			mac = macs[0]
		}
		for _, k := range machineKeys(survey, ip, mac) {
			if k != "mac:" {
				keys = append(keys, k)
			}
		}
		if len(keys) > 0 {
			hostKeys[pk] = keys
		}
	}

	return hostKeys
}

// transportsConfirmedMin is rule 9 of rewards/mainnet_rules.md: at least 2 transports.
const transportsConfirmedMin = 2

// transportsConfirmedPath is the file listing the visors whose day had
// transportsConfirmedMin transports that the other edge reported too.
func transportsConfirmedPath(dir, date string) string {
	return fmt.Sprintf("%s/%s_transports_confirmed.txt", dir, date)
}

// transportsConfirmedWrite writes the PKs with enough confirmed transports, sorted.
func transportsConfirmedWrite(date string, confirmed map[string]int) error {
	pks := make([]string, 0, len(confirmed))
	for pk, n := range confirmed {
		if n >= transportsConfirmedMin {
			pks = append(pks, pk)
		}
	}
	slices.Sort(pks)
	path := transportsConfirmedPath(histPath, date)
	if err := writeAtomic(path, 0600, func(w io.Writer) error {
		_, err := io.WriteString(w, strings.Join(pks, "\n")+"\n")
		return err
	}); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// transportsConfirmedRead reads the PKs transportsConfirmedWrite wrote, nil
// when the file is missing.
func transportsConfirmedRead(path string) map[string]struct{} {
	data, err := os.ReadFile(path) //nolint:gosec // a path of the reward work dir
	if err != nil {
		return nil
	}
	pks := make(map[string]struct{})
	for line := range strings.SplitSeq(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			pks[line] = struct{}{}
		}
	}
	return pks
}

// hostShared returns a key both visors' hosts have, or "" when none.
func hostShared(a, b []string) string {
	for _, k := range a {
		if slices.Contains(b, k) {
			return k
		}
	}
	return ""
}

// fetchTransportBandwidthFromTPD fetches per-transport metrics from TPD,
// filters out same-host transports (both edges share an IP or machine), and
// returns the per-edge sender-side daily totals. Each entry's SentA is
// what edge A claims sent capped by what B confirmed receiving (and
// mirror for SentB) so a one-sided .sent inflation in TPD cannot pump
// a visor's credit.
//
// No counterparty-eligibility filter here — bw-collect doesn't have the
// pool 1 eligibility set at run time (it's recomputed during calc using
// the day's surveys + uptime). The calculator does that filter after
// reading this list.
func fetchTransportBandwidthFromTPD(cmdFlags *pflag.FlagSet, bwLog *logging.Logger, hostKeys map[string][]string, useCache bool, days int, dateFilter string, local tpdMetricsReader) ([]TransportBW, map[string]int, error) {
	// Check cache
	if useCache {
		if info, err := os.Stat(bwCacheFile); err == nil {
			if time.Since(info.ModTime()) < tpdCacheMaxAge {
				bwLog.Debug("Using cached bandwidth data")
				data, err := os.ReadFile(bwCacheFile)
				if err == nil {
					var cached []TransportBW
					if err := json.Unmarshal(data, &cached); err == nil {
						return cached, nil, nil
					}
					bwLog.WithError(err).Debug("Failed to parse cached bandwidth data, fetching fresh")
				}
			}
		}
	}

	if days <= 0 {
		days = 1
	}

	out := []TransportBW{}
	confirmed := make(map[string]int)
	sameLANCount := 0
	reduce := func(tp *tpdTransport) {
		if len(tp.Edges) != 2 {
			return
		}

		// Verified sender-side per-edge totals, by the rule TPD's settled
		// day and the reward server use (tpdstore.SenderBytes): each edge's
		// sent capped by what the other edge received, and nothing for a
		// transport only one edge reported. Both edges report on 90 to 97%
		// of transports now, so the B2 condition that removed the cap holds.
		var sentA, sentB uint64
		bothReported := false
		for _, d := range tp.Daily {
			if dateFilter != "" && d.Date != dateFilter {
				continue
			}
			a, b := tpdstore.SenderBytes(d)
			sentA += a
			sentB += b
			bothReported = bothReported || tpdstore.BothReported(d)
		}

		edgeA := tp.Edges[0]
		edgeB := tp.Edges[1]
		sameHost := hostShared(hostKeys[edgeA], hostKeys[edgeB])

		// A transport counts toward the transport requirement only when the
		// visor at the other end, on another host, reported it too.
		if bothReported && tp.Type != "dmsg" && sameHost == "" {
			confirmed[edgeA]++
			confirmed[edgeB]++
		}
		if sentA == 0 && sentB == 0 {
			return
		}

		// Both edges on one IP or one machine earn nothing for the transport.
		if k := sameHost; k != "" {
			sameLANCount++
			bwLog.Debugf("Excluding same-host transport %s: %s and %s share %s (sent_a=%d sent_b=%d)",
				tp.ID, edgeA, edgeB, k, sentA, sentB)
			return
		}

		out = append(out, TransportBW{
			A:     edgeA,
			B:     edgeB,
			SentA: sentA,
			SentB: sentB,
		})
	}

	fetched := -1
	if local != nil {
		n, err := eachLocalTPDTransport(context.Background(), local, days, reduce)
		if err == nil {
			fetched = n
			bwLog.Infof("Read %d transports from transport discovery in this process", fetched)
		} else {
			bwLog.WithError(err).Warn("Could not read transport discovery in this process; fetching over dmsg")
		}
	}
	if fetched < 0 {
		// Fetch all transport metrics with edge info via visor RPC → DMSG → HTTP chain.
		tpdURL := strings.TrimSuffix(deployment.Prod.TransportDiscovery, "/")
		url := fmt.Sprintf("%s/metrics?days=%d&bandwidth=true&latency=false&edges=true", tpdURL, days)
		bwLog.Info("Fetching all transport metrics from TPD: ", url)
		body, err := fetchServiceURL(cmdFlags, url)
		if err != nil {
			return nil, nil, fmt.Errorf("TPD fetch failed: %w", err)
		}
		// The body holds every transport of the day, so each is decoded and
		// reduced on its own rather than held as one slice.
		if fetched, err = eachTPDTransport(body, reduce); err != nil {
			return nil, nil, fmt.Errorf("failed to parse JSON: %w", err)
		}
		bwLog.Infof("Fetched %d transports from TPD", fetched)
	}
	// TPD lists transports in no fixed order. Sorted, the same day's figures
	// give the same file, and the reward cycle's input hash does not change.
	slices.SortFunc(out, compareTransportBW)

	bwLog.Infof("Excluded %d same-LAN transports, %d remaining in per-edge sender-side dataset",
		sameLANCount, len(out))

	// Update cache
	if err := writeAtomic(bwCacheFile, 0600, func(w io.Writer) error { return writeTransportBWList(w, out) }); err != nil {
		bwLog.WithError(err).Warn("Failed to update bandwidth cache file")
	}

	return out, confirmed, nil
}

// minUint64 is the receiver-side anti-inflation cap previously used in
// fetchTransportBandwidthFromTPD's both-reported branch. Retained so
// the restore on v1.3.63 minimum is a single-line diff at the call
// site. Marked unused-ok until then.
//
//nolint:unused
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

// eachTPDTransport decodes a /metrics body one transport at a time, passing
// each to fn, and returns how many it decoded. It refuses what unmarshalling
// the body into a []tpdTransport would.
func eachTPDTransport(body []byte, fn func(*tpdTransport)) (int, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	tok, err := dec.Token()
	if err != nil {
		return 0, err
	}
	n := 0
	if tok != nil { // null is an empty list
		if tok != json.Delim('[') {
			return 0, fmt.Errorf("want a list of transports, got %v", tok)
		}
		for dec.More() {
			var tp tpdTransport
			if err := dec.Decode(&tp); err != nil {
				return n, err
			}
			fn(&tp)
			n++
		}
		if _, err := dec.Token(); err != nil {
			return n, err
		}
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("data after the list of transports")
	}
	return n, nil
}

// writeBandwidthData writes d as json.MarshalIndent(d, "", "  ") does, one
// transport at a time, so the whole document is never held in memory.
func writeBandwidthData(w io.Writer, d BandwidthData) error {
	if len(d.Transports) == 0 {
		b, err := json.MarshalIndent(d, "", "  ")
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	}
	bw := bufio.NewWriter(w)
	fmt.Fprintf(bw, "{\n  \"version\": %d,\n  \"transports\": [", d.Version) //nolint:errcheck // Flush reports it
	for i := range d.Transports {
		if i > 0 {
			bw.WriteByte(',') //nolint:errcheck,gosec // Flush reports it
		}
		bw.WriteString("\n    ") //nolint:errcheck,gosec // Flush reports it
		b, err := json.MarshalIndent(&d.Transports[i], "    ", "  ")
		if err != nil {
			return err
		}
		bw.Write(b) //nolint:errcheck,gosec // Flush reports it
	}
	bw.WriteString("\n  ]\n}") //nolint:errcheck,gosec // Flush reports it
	return bw.Flush()
}

// writeTransportBWList writes ts as a JSON array one transport at a time.
func writeTransportBWList(w io.Writer, ts []TransportBW) error {
	bw := bufio.NewWriter(w)
	bw.WriteByte('[') //nolint:errcheck,gosec // Flush reports it
	for i := range ts {
		if i > 0 {
			bw.WriteByte(',') //nolint:errcheck,gosec // Flush reports it
		}
		b, err := json.Marshal(&ts[i])
		if err != nil {
			return err
		}
		bw.Write(b) //nolint:errcheck,gosec // Flush reports it
	}
	bw.WriteByte(']') //nolint:errcheck,gosec // Flush reports it
	return bw.Flush()
}
