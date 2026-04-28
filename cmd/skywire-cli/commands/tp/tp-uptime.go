// Package clitp cmd/skywire-cli/commands/tp/tp-uptime.go
//
// `skywire cli tp uptime` — queries the transport-level uptime endpoints
// exposed by transport-discovery. Caching mirrors the pattern used by
// `ut sd` / `ut mdisc` / `ut tpd` via clirpc.FetchCachedServiceURL.
package clitp

import (
	"crypto/sha1" //nolint:gosec
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/uptimestats"
)

var (
	tpUptimeBaseURL  string
	tpUptimeVersion  string
	tpUptimeMetrics  bool
	tpUptimeIDs      []string
	tpUptimeVisors   []string
	tpUptimeOnline   bool
	tpUptimeType     string
	tpUptimeTimeout  time.Duration
	tpUptimeCacheDir string
	tpUptimeCacheAge int
)

func defaultTpUptimeCacheDir() string {
	u, err := url.Parse(deployment.Prod.TransportDiscovery)
	if err != nil || u.Host == "" {
		return ""
	}
	return filepath.Join(os.TempDir(), u.Host)
}

func init() {
	tpUptimeCmd.Flags().StringVar(&tpUptimeBaseURL, "url", deployment.Prod.TransportDiscovery, "transport-discovery base URL")
	tpUptimeCmd.Flags().StringVarP(&tpUptimeVersion, "v", "v", "v2", "response version (v1|v2|v3)")
	tpUptimeCmd.Flags().BoolVarP(&tpUptimeMetrics, "metrics", "m", false, "fetch network-wide /metrics/uptime aggregate instead of per-transport rows")
	tpUptimeCmd.Flags().StringSliceVar(&tpUptimeIDs, "ids", nil, "filter to these transport IDs (comma-separated UUIDs) — uses /metrics/uptime/{ids}")
	tpUptimeCmd.Flags().StringSliceVar(&tpUptimeVisors, "visors", nil, "filter to transports touching these visor PKs (comma-separated) — uses /metrics/uptime/visor/{pks}")
	tpUptimeCmd.Flags().BoolVarP(&tpUptimeOnline, "on", "o", false, "only include currently online transports")
	tpUptimeCmd.Flags().StringVarP(&tpUptimeType, "type", "t", "", "filter by transport type (stcpr / sudph / dmsg / stcp)")
	tpUptimeCmd.Flags().Bool("json", false, "emit raw JSON")
	tpUptimeCmd.Flags().DurationVar(&tpUptimeTimeout, "timeout", 30*time.Second, "HTTP timeout")
	tpUptimeCmd.Flags().StringVar(&tpUptimeCacheDir, "cache-dir", defaultTpUptimeCacheDir(), "cache directory (\"\" disables)")
	tpUptimeCmd.Flags().IntVar(&tpUptimeCacheAge, "cache-age", 5, "re-fetch if cache is older than N minutes (0 disables)")
	clirpc.RegisterFetchFlags(tpUptimeCmd)
	tpCmd.AddCommand(tpUptimeCmd)
}

var tpUptimeCmd = &cobra.Command{
	Use:   "uptime",
	Short: "query TPD integrated transport-level uptime",
	Long: `Transport-level uptime from transport-discovery.

Default (no flags): GET /uptimes/transports — every known transport, with
v1 (id+on), v2 (+ daily %), or v3 (+ timeline bitmap) fields.

Filtered modes (mutually exclusive):
  --ids a,b,c       /metrics/uptime/{ids}       — specific transports
  --visors pk1,pk2  /metrics/uptime/visor/{pks} — transports touching these visors
  --metrics         /metrics/uptime             — network-wide aggregate only

Combine with --on to show only currently-online transports, --type to filter by
transport type, or --json to dump raw JSON for piping.`,
	Run: func(cmd *cobra.Command, _ []string) {
		set := 0
		if tpUptimeMetrics {
			set++
		}
		if len(tpUptimeIDs) > 0 {
			set++
		}
		if len(tpUptimeVisors) > 0 {
			set++
		}
		if set > 1 {
			fatal(cmd, fmt.Errorf("--metrics, --ids and --visors are mutually exclusive"))
		}

		switch {
		case tpUptimeMetrics:
			raw := cachedFetch(cmd, "/metrics/uptime", "metrics-uptime", nil)
			var nu uptimestats.NetworkUptime
			if err := json.Unmarshal([]byte(raw), &nu); err != nil {
				fatal(cmd, fmt.Errorf("parse /metrics/uptime: %w", err))
			}
			internal.PrintOutput(cmd.Flags(), nu, formatNetworkUptime(nu))
		case len(tpUptimeIDs) > 0:
			ids, err := parseUUIDs(tpUptimeIDs)
			if err != nil {
				fatal(cmd, err)
			}
			parts := make([]string, len(ids))
			for i, id := range ids {
				parts[i] = id.String()
			}
			path := "/metrics/uptime/" + strings.Join(parts, ",")
			raw := cachedFetch(cmd, path, "metrics-uptime-ids", ids)
			var entries []uptimestats.TransportSummary
			if err := json.Unmarshal([]byte(raw), &entries); err != nil {
				fatal(cmd, fmt.Errorf("parse %s: %w", path, err))
			}
			emitTransports(cmd, filterTransports(entries))
		case len(tpUptimeVisors) > 0:
			pks, err := parsePubKeys(tpUptimeVisors)
			if err != nil {
				fatal(cmd, err)
			}
			parts := make([]string, len(pks))
			for i, pk := range pks {
				parts[i] = pk.Hex()
			}
			path := "/metrics/uptime/visor/" + strings.Join(parts, ",")
			raw := cachedFetch(cmd, path, "metrics-uptime-visor", pks)
			var entries []uptimestats.TransportSummary
			if err := json.Unmarshal([]byte(raw), &entries); err != nil {
				fatal(cmd, fmt.Errorf("parse %s: %w", path, err))
			}
			emitTransports(cmd, filterTransports(entries))
		default:
			q := ""
			if tpUptimeVersion != "" && tpUptimeVersion != "v1" {
				q = "?v=" + tpUptimeVersion
			}
			raw := cachedFetch(cmd, "/uptimes/transports"+q, "uptimes-transports-"+tpUptimeVersion, nil)
			var entries []uptimestats.TransportSummary
			if err := json.Unmarshal([]byte(raw), &entries); err != nil {
				fatal(cmd, fmt.Errorf("parse /uptimes/transports: %w", err))
			}
			emitTransports(cmd, filterTransports(entries))
		}
	},
}

// cachedFetch builds the full URL and hands it to FetchCachedServiceURL
// so we share the RPC → DMSG → HTTP fallback chain + disk cache with
// every other CLI command. The cacheKey is hashed when filterArgs is
// non-nil so different filter selections get separate cache files.
func cachedFetch(cmd *cobra.Command, path, cacheKey string, filterArgs interface{}) string {
	fullURL := strings.TrimRight(tpUptimeBaseURL, "/") + path
	cf := tpUptimeCacheFile(cacheKey, filterArgs)
	raw := clirpc.FetchCachedServiceURL(cmd.Flags(), cf, fullURL, tpUptimeCacheAge)
	if raw == "" {
		fatal(cmd, fmt.Errorf("empty response from %s", fullURL))
	}
	return raw
}

func tpUptimeCacheFile(key string, filterArgs interface{}) string {
	if tpUptimeCacheDir == "" {
		return ""
	}
	if err := os.MkdirAll(tpUptimeCacheDir, 0o750); err != nil {
		return ""
	}
	name := key
	if filterArgs != nil {
		h := sha1.New()                     //nolint:gosec
		data, _ := json.Marshal(filterArgs) //nolint:errcheck
		h.Write(data)                       //nolint:errcheck
		name += "-" + hex.EncodeToString(h.Sum(nil))[:12]
	}
	return filepath.Join(tpUptimeCacheDir, name+".json")
}

func filterTransports(in []uptimestats.TransportSummary) []uptimestats.TransportSummary {
	if !tpUptimeOnline && tpUptimeType == "" {
		return in
	}
	out := make([]uptimestats.TransportSummary, 0, len(in))
	for _, t := range in {
		if tpUptimeOnline && !t.Online {
			continue
		}
		if tpUptimeType != "" && t.Type != tpUptimeType {
			continue
		}
		out = append(out, t)
	}
	return out
}

func emitTransports(cmd *cobra.Command, entries []uptimestats.TransportSummary) {
	var buf strings.Builder
	for _, t := range entries {
		state := "off"
		if t.Online {
			state = "on "
		}
		typ := t.Type
		if typ == "" {
			typ = "-"
		}
		fmt.Fprintf(&buf, "%s %s %-6s %s <-> %s\n", state, t.ID, typ, t.EdgeA, t.EdgeB)
	}
	internal.PrintOutput(cmd.Flags(), entries, buf.String())
}

func formatNetworkUptime(nu uptimestats.NetworkUptime) string {
	var buf strings.Builder
	pct := 0.0
	if nu.TotalTransports > 0 {
		pct = 100 * float64(nu.Online) / float64(nu.TotalTransports)
	}
	fmt.Fprintf(&buf, "transports: %d total, %d online (%.1f%%)\n", nu.TotalTransports, nu.Online, pct)
	if len(nu.ByType) > 0 {
		buf.WriteString("by type:\n")
		keys := make([]string, 0, len(nu.ByType))
		for k := range nu.ByType {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b := nu.ByType[k]
			typePct := 0.0
			if b.Total > 0 {
				typePct = 100 * float64(b.Online) / float64(b.Total)
			}
			fmt.Fprintf(&buf, "  %-6s %5d / %5d online (%.1f%%)\n", k, b.Online, b.Total, typePct)
		}
	}
	return buf.String()
}

func parseUUIDs(in []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("invalid --ids entry %q: %w", s, err)
		}
		out = append(out, id)
	}
	return out, nil
}

func parsePubKeys(in []string) ([]cipher.PubKey, error) {
	out := make([]cipher.PubKey, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		var pk cipher.PubKey
		if err := pk.Set(s); err != nil {
			return nil, fmt.Errorf("invalid --visors entry %q: %w", s, err)
		}
		out = append(out, pk)
	}
	return out, nil
}

func fatal(cmd *cobra.Command, err error) {
	fmt.Fprintf(cmd.ErrOrStderr(), "error: %v\n", err) //nolint:errcheck,gosec
	os.Exit(1)
}
