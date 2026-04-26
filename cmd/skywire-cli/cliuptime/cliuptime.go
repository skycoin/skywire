// Package cliuptime provides reusable cobra subcommands for querying
// the discovery-integrated /uptimes endpoints. Each consumer CLI
// (skywire cli ut {sd,mdisc,tpd}) wires it in with a per-service
// default URL; filter semantics and output shapes are identical.
//
// Two verbs are provided:
//
//	<parent>          — per-day percentage table (textual)
//	<parent> graph    — shaded-block timeline graph (visual)
//
// Keeping them separate lets the table command stay minimal (just
// percentages) while the graph command concentrates all the
// display-mode flags (per-day / single-line / rolling window).
//
// Keep this thin — the HTTP fetch and filter logic live in
// pkg/uptimestats so the hypervisor UI / RPC layer can eventually
// render the same data without going through a CLI subprocess.
package cliuptime

import (
	"crypto/sha1" //nolint:gosec // hash used only as cache-filename key
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/uptimestats"
)

// Config parameterises the subcommands for a specific discovery.
type Config struct {
	// Use is the cobra command verb (e.g. "sd", "mdisc", "tpd").
	Use string
	// DefaultURL is the base URL used when the --url flag is not set.
	DefaultURL string
	// Short / Long help text used for the generated command.
	Short string
	Long  string
}

// dateRange describes which daily columns the table mode should show.
// Date strings use the server's YYYY-MM-DD format throughout; no
// timezone rewriting happens client-side.
type dateRange struct {
	days  int    // 0 = only the single latest date per visor
	since string // inclusive lower bound (YYYY-MM-DD)
	until string // inclusive upper bound (YYYY-MM-DD)
	all   bool   // include every date the server returned
}

// NewCmd returns a configured uptime subcommand plus its `graph`
// child. Mount it under whichever parent fits the consumer.
func NewCmd(cfg Config) *cobra.Command {
	if cfg.Use == "" {
		cfg.Use = "uptime"
	}
	cmd := newTableCmd(cfg)
	cmd.AddCommand(newGraphCmd(cfg))
	return cmd
}

// newTableCmd builds the textual per-day percentage table command.
// Intentionally free of any graph-rendering flags so the flag set
// stays readable — visual modes live on `graph`.
func newTableCmd(cfg Config) *cobra.Command {
	var (
		baseURL     string
		versionStr  string
		pkFilter    string
		visorFilter []string
		onlineOnly  bool
		statsOnly   bool
		versionEq   string
		minVersion  string
		listVers    bool
		minDaily    int
		jsonOut     bool
		timeout     time.Duration
		cacheDir    string
		cacheAge    int
		dr          dateRange
	)
	cmd := &cobra.Command{
		Use:   cfg.Use,
		Short: cfg.Short,
		Long:  cfg.Long,
		Run: func(c *cobra.Command, _ []string) {
			if (dr.days > 0 || dr.since != "" || dr.until != "" || dr.all) && versionStr == "v1" {
				versionStr = "v2"
			}
			entries, err := fetchEntries(c, baseURL, versionStr, visorFilter, cacheDir, cacheAge)
			if err != nil {
				fatal(c, err)
			}
			filtered := applyFilters(entries, onlineOnly, pkFilter, versionEq, minVersion, minDaily)
			if jsonOut {
				_ = json.NewEncoder(os.Stdout).Encode(filtered) //nolint:errcheck,gosec
				return
			}
			switch {
			case statsOnly:
				label := "visors"
				if onlineOnly {
					label = "online visors"
				}
				fmt.Printf("%d %s\n", len(filtered), label)
			case listVers:
				printVersions(filtered)
			default:
				printTable(filtered, versionStr, dr)
			}
		},
	}

	cmd.Flags().StringVar(&baseURL, "url", cfg.DefaultURL, "discovery base URL")
	cmd.Flags().StringVarP(&versionStr, "v", "v", "v2", "response version (v1|v2)")
	cmd.Flags().StringVarP(&pkFilter, "pk", "k", "", "only show PKs matching this substring")
	cmd.Flags().StringSliceVar(&visorFilter, "visors", nil, "server-side filter: only return these PKs (comma-separated)")
	cmd.Flags().BoolVarP(&onlineOnly, "on", "o", false, "only include online visors")
	cmd.Flags().BoolVarP(&statsOnly, "stats", "s", false, "print count of matching visors only")
	cmd.Flags().StringVar(&versionEq, "version", "", "filter visors by exact version")
	cmd.Flags().StringVar(&minVersion, "min-version", "", "filter visors with version >= this (e.g. v1.3.40)")
	cmd.Flags().BoolVarP(&listVers, "list-versions", "l", false, "list version distribution (with --stats) or pk+version pairs")
	cmd.Flags().IntVarP(&minDaily, "min-daily", "n", 0, "only visors whose worst daily-uptime is >= this percent")
	cmd.Flags().IntVarP(&dr.days, "days", "d", 0, "number of most-recent days to include (0 = latest day only)")
	cmd.Flags().StringVar(&dr.since, "since", "", "include days on or after this date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&dr.until, "until", "", "include days on or before this date (YYYY-MM-DD)")
	cmd.Flags().BoolVarP(&dr.all, "all", "a", false, "include every day the server returned")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit raw JSON")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "HTTP timeout")
	cmd.Flags().StringVar(&cacheDir, "cache-dir", defaultCacheDir(cfg.DefaultURL), "cache directory (\"\" disables cache)")
	cmd.Flags().IntVarP(&cacheAge, "cache-age", "m", 5, "re-fetch if cache is older than N minutes (0 disables)")
	clirpc.RegisterFetchFlags(cmd)
	return cmd
}

// newGraphCmd builds the `<parent> graph` subcommand. Always fetches
// v3 since only v3 carries the per-5-minute bitmaps. Three modes:
//
//	(default)     single-line bar per visor across the selected days
//	--per-day     one row per day per visor (old -T default)
//	--hours N     rolling window crossing day boundaries
//
// Output is deliberately compact by default: `<pk>  <graph>`. `-v`
// expands it with version / online state / range. Flags that apply
// only to the graph view (hours, per-day, single-line) live here.
func newGraphCmd(cfg Config) *cobra.Command {
	var (
		baseURL     string
		pkFilter    string
		visorFilter []string
		onlineOnly  bool
		versionEq   string
		minVersion  string
		perDay      bool
		hoursBack   int
		verbose     bool
		jsonOut     bool
		timeout     time.Duration
		cacheDir    string
		cacheAge    int
		dr          dateRange
	)
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "render an uptime timeline graph (compact shaded-block bars)",
		Long: `Render the v3 per-5-minute timeline as shaded 1-hour blocks.

Default: one line per visor, all days available server-side, compact "<pk> <bar>" format.

Modes (mutually exclusive after flags):
  (default)   single-line bar per visor, spans the selected day range
  --per-day   break each visor's bar onto one row per day
  --hours N   rolling window of the last N hours ending at now

Date range flags (--days/--since/--until/--all) apply to the default
and --per-day modes; --hours ignores them.`,
		Run: func(c *cobra.Command, _ []string) {
			entries, err := fetchEntries(c, baseURL, "v3", visorFilter, cacheDir, cacheAge)
			if err != nil {
				fatal(c, err)
			}
			filtered := applyFilters(entries, onlineOnly, pkFilter, versionEq, minVersion, 0)
			if jsonOut {
				_ = json.NewEncoder(os.Stdout).Encode(filtered) //nolint:errcheck,gosec
				return
			}
			switch {
			case hoursBack > 0:
				printRollingTimelines(filtered, hoursBack, verbose)
			case perDay:
				printTimelines(filtered, dr, verbose)
			default:
				printSingleLineTimelines(filtered, dr, verbose)
			}
		},
	}

	cmd.Flags().StringVar(&baseURL, "url", cfg.DefaultURL, "discovery base URL")
	cmd.Flags().StringVarP(&pkFilter, "pk", "k", "", "only show PKs matching this substring")
	cmd.Flags().StringSliceVar(&visorFilter, "visors", nil, "server-side filter: only return these PKs (comma-separated)")
	cmd.Flags().BoolVarP(&onlineOnly, "on", "o", false, "only include online visors")
	cmd.Flags().StringVar(&versionEq, "version", "", "filter visors by exact version")
	cmd.Flags().StringVar(&minVersion, "min-version", "", "filter visors with version >= this")
	cmd.Flags().BoolVar(&perDay, "per-day", false, "one row per day instead of a single concatenated bar")
	cmd.Flags().IntVar(&hoursBack, "hours", 0, "rolling-window mode: show last N hours ending at now")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "include version / online state / range header per visor")
	cmd.Flags().IntVarP(&dr.days, "days", "d", 0, "number of most-recent days to include (0 = all available; ignored with --hours)")
	cmd.Flags().StringVar(&dr.since, "since", "", "include days on or after this date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&dr.until, "until", "", "include days on or before this date (YYYY-MM-DD)")
	cmd.Flags().BoolVarP(&dr.all, "all", "a", false, "include every day the server returned (default when --days/--since/--until not set)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit raw JSON instead of rendering")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "HTTP timeout")
	cmd.Flags().StringVar(&cacheDir, "cache-dir", defaultCacheDir(cfg.DefaultURL), "cache directory (\"\" disables cache)")
	cmd.Flags().IntVarP(&cacheAge, "cache-age", "m", 5, "re-fetch if cache is older than N minutes (0 disables)")
	clirpc.RegisterFetchFlags(cmd)
	// Graph's default date range differs from table's: the user
	// typically wants the full available history when drawing a
	// trend. Flip the default to --all when the daily-scope flags
	// are otherwise untouched, via cobra's PreRun.
	cmd.PreRun = func(_ *cobra.Command, _ []string) {
		if dr.days == 0 && dr.since == "" && dr.until == "" && !dr.all {
			dr.all = true
		}
	}
	return cmd
}

// fetchEntries builds the /uptimes URL and hands it off to
// clirpc.FetchCachedServiceURL so we share the RPC → DMSG → HTTP
// fallback chain + disk cache with every other uptime-consuming CLI
// command. Never use uptimestats.Client.VisorUptimes from CLI — it
// bypasses the cache and can trip the deployment services' rate
// limits when users iterate on flag combos.
func fetchEntries(c *cobra.Command, baseURL, version string, visors []string, cacheDir string, cacheAge int) ([]uptimestats.VisorSummary, error) {
	pks, err := parsePKs(visors)
	if err != nil {
		return nil, err
	}
	fullURL := buildUptimesURL(baseURL, version, pks)
	cacheFile := cacheFilePath(cacheDir, baseURL, version, pks)
	raw := clirpc.FetchCachedServiceURL(c.Flags(), cacheFile, fullURL, cacheAge)
	if raw == "" {
		return nil, fmt.Errorf("empty response from %s (all fetch paths failed; see --no-rpc / --no-dmsg / --no-http flags)", fullURL)
	}
	var out []uptimestats.VisorSummary
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("parse %s response: %w", fullURL, err)
	}
	return out, nil
}

// buildUptimesURL constructs the full GET URL with v=<version> and
// optional visors=<pk1;pk2;...> query params. Shared between the
// fetch path (as the URL to request) and the cache path (as a key
// component) so the two can never drift.
func buildUptimesURL(baseURL, version string, pks []cipher.PubKey) string {
	u := strings.TrimRight(baseURL, "/") + "/uptimes"
	q := url.Values{}
	if version != "" && version != "v1" {
		q.Set("v", version)
	}
	if len(pks) > 0 {
		parts := make([]string, len(pks))
		for i, pk := range pks {
			parts[i] = pk.Hex()
		}
		q.Set("visors", strings.Join(parts, ";"))
	}
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

// cacheFilePath returns the on-disk cache path for this URL, or ""
// when caching is disabled. The filename encodes the version because
// v2 and v3 payloads are different shapes, and the visor-filter hash
// so different --visors selections get their own cache file rather
// than stomping each other.
func cacheFilePath(cacheDir, baseURL, version string, pks []cipher.PubKey) string {
	if cacheDir == "" {
		return ""
	}
	if err := os.MkdirAll(cacheDir, 0o750); err != nil {
		return ""
	}
	name := "uptimes"
	if version != "" && version != "v1" {
		name += "-" + version
	}
	if len(pks) > 0 {
		// Fixed-size hash so cache keys stay short regardless of
		// how many PKs the caller filtered on.
		h := sha1.New() //nolint:gosec // not a security boundary
		for _, pk := range pks {
			h.Write([]byte(pk.Hex())) //nolint:errcheck
			h.Write([]byte{';'})      //nolint:errcheck
		}
		name += "-" + hex.EncodeToString(h.Sum(nil))[:12]
	}
	_ = baseURL // cacheDir is already per-host; no need to include baseURL in filename
	return filepath.Join(cacheDir, name+".json")
}

// defaultCacheDir mirrors the ~/.cache-style directory helpers used
// by the pre-existing `skywire cli ut`, `cli sd`, etc. — one
// subdirectory per discovery host under os.TempDir so cache files
// from different environments don't mix.
func defaultCacheDir(baseURL string) string {
	if baseURL == "" {
		return ""
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return ""
	}
	return filepath.Join(os.TempDir(), u.Host)
}

func applyFilters(entries []uptimestats.VisorSummary, onlineOnly bool, pkSubstr, versionEq, minVersion string, minDaily int) []uptimestats.VisorSummary {
	out := uptimestats.Filter(entries, uptimestats.VisorFilter{
		OnlineOnly:    onlineOnly,
		PKSubstr:      pkSubstr,
		VersionEquals: versionEq,
		MinVersion:    minVersion,
	})
	if minDaily > 0 {
		out = uptimestats.MinDailyUptime(out, minDaily)
	}
	return out
}

func parsePKs(raw []string) ([]cipher.PubKey, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]cipher.PubKey, 0, len(raw))
	for _, s := range raw {
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

// collectDates walks every entry's Daily (or Timeline) map and
// returns the sorted union of dates, filtered by the date range
// selector.
func collectDates(entries []uptimestats.VisorSummary, dr dateRange) []string {
	seen := make(map[string]struct{})
	for _, e := range entries {
		for k := range e.Daily {
			seen[k] = struct{}{}
		}
		for k := range e.Timeline {
			seen[k] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	all := make([]string, 0, len(seen))
	for k := range seen {
		all = append(all, k)
	}
	sort.Strings(all)
	if dr.since != "" {
		out := all[:0]
		for _, d := range all {
			if d >= dr.since {
				out = append(out, d)
			}
		}
		all = out
	}
	if dr.until != "" {
		out := all[:0]
		for _, d := range all {
			if d <= dr.until {
				out = append(out, d)
			}
		}
		all = out
	}
	switch {
	case dr.all, dr.since != "" || dr.until != "":
		return all
	case dr.days > 0:
		if dr.days >= len(all) {
			return all
		}
		return all[len(all)-dr.days:]
	default:
		return all[len(all)-1:]
	}
}

// printTable prints a per-day uptime table (no graph).
func printTable(entries []uptimestats.VisorSummary, version string, dr dateRange) {
	if len(entries) == 0 {
		return
	}
	dates := collectDates(entries, dr)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	showVersion := version != "v1"

	header := []string{"pk"}
	if showVersion {
		header = append(header, "ver")
	}
	header = append(header, "on")
	header = append(header, dates...)
	fmt.Fprintln(w, strings.Join(header, "\t")) //nolint:errcheck,gosec

	sorted := make([]uptimestats.VisorSummary, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].PK.Hex() < sorted[j].PK.Hex() })

	for _, e := range sorted {
		fields := []string{e.PK.Hex()}
		if showVersion {
			fields = append(fields, stringOrDash(e.Version))
		}
		if e.Online {
			fields = append(fields, "yes")
		} else {
			fields = append(fields, "no")
		}
		for _, d := range dates {
			if v, ok := e.Daily[d]; ok {
				fields = append(fields, v)
			} else {
				fields = append(fields, "-")
			}
		}
		fmt.Fprintln(w, strings.Join(fields, "\t")) //nolint:errcheck,gosec
	}
	_ = w.Flush() //nolint:errcheck,gosec
}

func stringOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func printVersions(entries []uptimestats.VisorSummary) {
	counts := uptimestats.VersionCounts(entries)
	for _, vc := range counts {
		v := vc.Version
		if v == "" {
			v = "(none)"
		}
		fmt.Printf("%6d %s\n", vc.Count, v)
	}
}

// printTimelines renders v3 bitmaps with one row per day per visor
// (aka --per-day). Verbose prepends a per-visor header line with
// version + state; non-verbose just prints date + blocks + pct.
func printTimelines(entries []uptimestats.VisorSummary, dr dateRange, verbose bool) {
	if len(entries) == 0 {
		return
	}
	dates := collectDates(entries, dr)
	dateSet := make(map[string]struct{}, len(dates))
	for _, d := range dates {
		dateSet[d] = struct{}{}
	}
	sorted := sortByPK(entries)

	first := true
	for _, e := range sorted {
		if verbose {
			if !first {
				fmt.Println()
			}
			fmt.Printf("%s  %s  %s\n", e.PK.Hex(), stringOrDash(e.Version), onlineWord(e.Online))
			fmt.Println("  date        00 03 06 09 12 15 18 21      daily%")
		}
		first = false

		days := make([]string, 0, len(e.Timeline))
		for d := range e.Timeline {
			if _, ok := dateSet[d]; ok {
				days = append(days, d)
			}
		}
		sort.Strings(days)
		if len(days) == 0 {
			if verbose {
				fmt.Println("  (no timeline data in selected range)")
			}
			continue
		}
		for _, d := range days {
			blocks := bitmapToBlocks(e.Timeline[d])
			pct := "-"
			if v, ok := e.Daily[d]; ok {
				pct = v + "%"
			}
			if verbose {
				fmt.Printf("  %s  %s      %s\n", d, blocks, pct)
			} else {
				// Compact: pk prefix per row so each line is
				// self-identifying when grep'd.
				fmt.Printf("%s  %s  %s  %s\n", e.PK.Hex(), d, blocks, pct)
			}
		}
	}
}

// printSingleLineTimelines is the default `graph` output: each visor
// on exactly one line as "<pk> <concatenated bar>". Verbose adds a
// range header line above.
func printSingleLineTimelines(entries []uptimestats.VisorSummary, dr dateRange, verbose bool) {
	if len(entries) == 0 {
		return
	}
	dates := collectDates(entries, dr)
	if len(dates) == 0 {
		return
	}
	sorted := sortByPK(entries)

	// Build all bars first so we can compute the global leading-
	// space trim. When uptime tracking was deployed partway through
	// the earliest day in the dataset, EVERY visor's bar starts with
	// the same block of spaces. Trimming that shared prefix avoids
	// wasting a quarter of the terminal width on a period where no
	// data existed for anyone.
	bars := make([]string, len(sorted))
	for i, e := range sorted {
		var bar strings.Builder
		bar.Grow(len(dates) * 24)
		for _, d := range dates {
			bmp, ok := e.Timeline[d]
			if !ok {
				bar.WriteString(strings.Repeat(" ", 24))
				continue
			}
			bar.WriteString(bitmapToBlocks(bmp))
		}
		bars[i] = bar.String()
	}

	// Find the minimum leading spaces across all bars. This is the
	// "no one had data yet" prefix that's safe to trim globally.
	trim := len(bars[0]) // start at max possible
	for _, b := range bars {
		n := 0
		for _, r := range b {
			if r != ' ' {
				break
			}
			n++
		}
		if n < trim {
			trim = n
		}
	}
	// Also trim shared trailing spaces (partial current day ending
	// at "now" — everyone's bar ends with the same unfilled hours).
	trailTrim := len(bars[0])
	for _, b := range bars {
		n := 0
		for i := len(b) - 1; i >= 0; i-- {
			if b[i] != ' ' {
				break
			}
			n++
		}
		if n < trailTrim {
			trailTrim = n
		}
	}

	for i, e := range sorted {
		if verbose {
			fmt.Printf("%s  %s  %s  range: %s → %s (%d days)\n",
				e.PK.Hex(), stringOrDash(e.Version), onlineWord(e.Online),
				dates[0], dates[len(dates)-1], len(dates))
		}
		b := bars[i]
		end := len(b) - trailTrim
		if end < trim {
			end = trim
		}
		fmt.Printf("%s %s\n", e.PK.Hex(), b[trim:end])
	}
}

// printRollingTimelines draws the last N hours ending at now as one
// bar per visor. Verbose adds a tick-label row above; non-verbose is
// strictly `<pk> <bar>` to stay grep-friendly.
func printRollingTimelines(entries []uptimestats.VisorSummary, hoursBack int, verbose bool) {
	if len(entries) == 0 || hoursBack <= 0 {
		return
	}
	now := time.Now().UTC()
	start := now.Add(-time.Duration(hoursBack) * time.Hour)
	sorted := sortByPK(entries)

	if verbose {
		fmt.Printf("# last %dh ending %s  (tick labels below)\n",
			hoursBack, now.Format(time.RFC3339))
		// A single label row at the top so it's not repeated per
		// visor. Indent to line up under the first block column; PK
		// is 64 chars + 1 space = 65.
		fmt.Printf("%s %s\n", strings.Repeat(" ", 64), rollingLabels(hoursBack))
	}

	for _, e := range sorted {
		var bar strings.Builder
		bar.Grow(hoursBack)
		for i := hoursBack - 1; i >= 0; i-- {
			hourStart := now.Add(-time.Duration(i+1) * time.Hour)
			hourEnd := now.Add(-time.Duration(i) * time.Hour)
			count := countOnlineSlots(e.Timeline, hourStart, hourEnd)
			bar.WriteString(shadeForCount(count))
		}
		fmt.Printf("%s %s\n", e.PK.Hex(), bar.String())

		if verbose {
			// Per-window aggregate underneath the bar when verbose.
			total, online := 0, 0
			for t := start; t.Before(now); t = t.Add(5 * time.Minute) {
				total++
				day := t.UTC().Format("2006-01-02")
				if bmp, ok := e.Timeline[day]; ok {
					slot := t.Hour()*12 + t.Minute()/5
					if slot < len(bmp) && bmp[slot] == '.' {
						online++
					}
				}
			}
			pct := 0.0
			if total > 0 {
				pct = 100 * float64(online) / float64(total)
			}
			fmt.Printf("%s   window uptime: %.2f%% (%d/%d slots)\n",
				strings.Repeat(" ", 64), pct, online, total)
		}
	}
}

func sortByPK(entries []uptimestats.VisorSummary) []uptimestats.VisorSummary {
	out := make([]uptimestats.VisorSummary, len(entries))
	copy(out, entries)
	sort.Slice(out, func(i, j int) bool { return out[i].PK.Hex() < out[j].PK.Hex() })
	return out
}

func onlineWord(online bool) string {
	if online {
		return "online"
	}
	return "offline"
}

// rollingLabels builds the tick-mark row above a rolling-window bar.
// Step is max(labelWidth+1, N/8) so labels never overlap regardless
// of window size.
func rollingLabels(hoursBack int) string {
	if hoursBack <= 0 {
		return ""
	}
	widest := len(fmt.Sprintf("-%dh", hoursBack-1))
	if widest < 3 {
		widest = 3
	}
	step := hoursBack / 8
	if step < widest+1 {
		step = widest + 1
	}
	buf := make([]byte, hoursBack+widest)
	for i := range buf {
		buf[i] = ' '
	}
	write := func(col int, s string) {
		for i := 0; i < len(s) && col+i < len(buf); i++ {
			buf[col+i] = s[i]
		}
	}
	for i := 0; i < hoursBack; i += step {
		hoursAgo := hoursBack - 1 - i
		if hoursAgo == 0 {
			write(i, "now")
			continue
		}
		write(i, fmt.Sprintf("-%dh", hoursAgo))
	}
	lastCol := hoursBack - 1
	if (hoursBack-1)%step != 0 {
		write(lastCol, "now")
	}
	return strings.TrimRight(string(buf), " ")
}

// countOnlineSlots returns how many 5-minute slots between [from, to)
// are marked online across per-day bitmaps.
func countOnlineSlots(timelines map[string]string, from, to time.Time) int {
	n := 0
	for t := from; t.Before(to); t = t.Add(5 * time.Minute) {
		day := t.UTC().Format("2006-01-02")
		bmp, ok := timelines[day]
		if !ok {
			continue
		}
		slot := t.Hour()*12 + t.Minute()/5
		if slot >= 0 && slot < len(bmp) && bmp[slot] == '.' {
			n++
		}
	}
	return n
}

func shadeForCount(count int) string {
	switch {
	case count == 0:
		return " "
	case count <= 3:
		return "░"
	case count <= 6:
		return "▒"
	case count <= 9:
		return "▓"
	default:
		return "█"
	}
}

// bitmapToBlocks turns a 288-char timeline string (".  "/" ") into
// 24 hourly blocks with 5 density levels.
func bitmapToBlocks(bitmap string) string {
	const hours = 24
	const slotsPerHour = 12
	const totalSlots = hours * slotsPerHour
	counts := make([]int, hours)
	if len(bitmap) == 0 {
		return strings.Repeat(" ", hours)
	}
	step := len(bitmap)
	if step > totalSlots {
		step = totalSlots
	}
	for i := 0; i < step; i++ {
		if bitmap[i] == '.' {
			hr := i * hours / step
			if hr >= hours {
				hr = hours - 1
			}
			counts[hr]++
		}
	}
	var b strings.Builder
	b.Grow(hours)
	for _, c := range counts {
		b.WriteString(shadeForCount(c))
	}
	return b.String()
}

func fatal(c *cobra.Command, err error) {
	fmt.Fprintf(c.ErrOrStderr(), "error: %v\n", err) //nolint:errcheck,gosec
	os.Exit(1)
}
