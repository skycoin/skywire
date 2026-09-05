// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/stats.go c4-vis-cli
package clirewardsserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bitfield/script"
	"github.com/oschwald/geoip2-golang/v2"

	geoipcmd "github.com/skycoin/skywire/cmd/svc/geoip/commands"
	"github.com/skycoin/skywire/deployment"
)

var (
	tempJSONPath  = filepath.Join(os.TempDir(), "log-collection.json")
	tempStatsPath = filepath.Join(os.TempDir(), "stats")
	cacheInterval = 300 * time.Second // refresh cache every 5 minutes seconds
)

// Node represents a single node's flattened information.
type node struct {
	PK        string `json:"pk"`
	Time      string `json:"time"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	StartedAt string `json:"started_at"`
}

type nodesResponse struct {
	Nodes []node `json:"nodes"`
}

func generateAndCacheJSON() error {
	pks, err := script.ListFiles(wd + "/log_backups").Basename().Slice()
	if err != nil {
		return err
	}

	var nodes []node
	for i := range pks {
		healthPath := wd + "/log_backups/" + pks[i] + "/health.json"
		nodeInfoPath := wd + "/log_backups/" + pks[i] + "/node-info.json"

		fileInfo, err := os.Stat(healthPath)
		if err != nil {
			continue
		}
		_, err = os.Stat(nodeInfoPath)
		if err != nil {
			continue
		}

		modTime := fileInfo.ModTime().Format(time.RFC3339)

		healthData, err := script.File(healthPath).Bytes()
		if err != nil {
			continue
		}

		var temp struct {
			BuildInfo struct {
				Version string `json:"version"`
				Commit  string `json:"commit"`
				Date    string `json:"date"`
			} `json:"build_info"`
			StartedAt string `json:"started_at"`
		}

		if err := json.Unmarshal(healthData, &temp); err != nil {
			continue
		}

		nodeInfoSlc, err := script.File(nodeInfoPath).JQ(".skywire_version").Replace(`"`, "").Replace("\n", "").Slice()
		if err != nil || len(nodeInfoSlc) == 0 {
			continue
		}

		nodes = append(nodes, node{
			PK:        pks[i],
			Time:      modTime,
			Version:   nodeInfoSlc[0],
			Commit:    temp.BuildInfo.Commit,
			Date:      temp.BuildInfo.Date,
			StartedAt: temp.StartedAt,
		})
	}

	data := nodesResponse{Nodes: nodes}
	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(data); err != nil {
		return err
	}

	return os.WriteFile(tempJSONPath, buf.Bytes(), 0600)
}

func generateAndCacheStats() (err error) {
	_, err = script.Exec("mkdir -p " + tempStatsPath).String()
	if err != nil {
		return err
	}

	pks, err := script.ListFiles(wd + "/log_backups").Basename().Slice()
	if err != nil {
		return err
	}
	var surveyarches string
	var surveycpus string
	var surveyOSNames string
	var surveyProductNames string
	var totalBytes int64
	var totalramBytes int64
	for i := range pks {
		ni := wd + "/log_backups/" + pks[i] + "/node-info.json"
		surveycpu, err := script.File(ni).JQ(".zcalusic_sysinfo.cpu.model").Replace(`"`, "").String()
		if err != nil {
			continue
		}
		surveycpus += surveycpu

		surveyarch, err := script.File(ni).JQ(".go_arch").Replace(`"`, "").String()
		if err != nil {
			continue
		}
		surveyarches += surveyarch

		surveyOSName, err := script.File(ni).JQ(".zcalusic_sysinfo.os.name").Replace(`"`, "").String()
		if err != nil {
			continue
		}
		surveyOSNames += surveyOSName

		surveyProductName, err := script.File(ni).JQ(".zcalusic_sysinfo.product.name").Replace(`"`, "").Reject("null").String()
		if err == nil && surveyProductName != "" && surveyProductName != "\n" {
			surveyProductNames += surveyProductName
		}

		surveytbs, err := script.File(ni).JQ(".ghw_blockinfo.total_size_bytes").Reject("null").Replace(`"`, "").String()
		if err != nil {
			continue
		}
		if surveytbs != "\n" && surveytbs != "" {
			byteValue, err := strconv.ParseInt(strings.TrimRight(surveytbs, "\n"), 10, 64)
			if err != nil {
				continue
			}
			totalBytes += byteValue
		}

		surveymem, err := script.File(ni).JQ(".ghw_memoryinfo.total_usable_bytes").Reject("null").Replace(`"`, "").String()
		if err != nil {
			continue
		}
		if surveymem != "\n" && surveymem != "" {
			byteValue, err := strconv.ParseInt(strings.TrimRight(surveymem, "\n"), 10, 64)
			if err != nil {
				continue
			}
			totalramBytes += byteValue
		}
	}

	cpustats, err := script.Echo(surveycpus).Freq().String()
	if err != nil {
		return err
	}
	_, err = script.Echo(fmt.Sprintf("Survey CPU statistics:\n%s\n", cpustats)).WriteFile(tempStatsPath + "/cpu.txt")
	if err != nil {
		return err
	}

	archstats, err := script.Echo(surveyarches).Freq().String()
	if err != nil {
		return err
	}
	_, err = script.Echo(fmt.Sprintf("Survey architecture statistics:\n%s\n", archstats)).WriteFile(tempStatsPath + "/arch.txt")
	if err != nil {
		return err
	}

	namestats, err := script.Echo(surveyOSNames).Freq().String()
	if err != nil {
		return err
	}
	_, err = script.Echo(fmt.Sprintf("Survey OS name statistics:\n%s\n", namestats)).WriteFile(tempStatsPath + "/os.txt")
	if err != nil {
		return err
	}

	productstats, err := script.Echo(surveyProductNames).Freq().String()
	if err != nil {
		return err
	}
	_, err = script.Echo(fmt.Sprintf("Survey hardware/product name statistics:\n%s\n", productstats)).WriteFile(tempStatsPath + "/product.txt")
	if err != nil {
		return err
	}

	formattedTotal, err := script.Echo(fmt.Sprintf("%d", totalBytes)).ExecForEach("numfmt --to=iec {{.}}").String()
	if err != nil {
		return err
	}

	_, err = script.Echo(fmt.Sprintf("Survey total byte size (cumulative): %s\n", formattedTotal)).WriteFile(tempStatsPath + "/mem.txt")
	if err != nil {
		return err
	}

	_, err = script.Exec(`bash -c 'jq '.ghw_blockinfo.total_size_bytes' ` + wd + `/log_backups/*/node-info.json | grep -v null | sort -n | numfmt --to=iec | sort -h | uniq -c'`).Reject("T").AppendFile(tempStatsPath + "/mem.txt")
	if err != nil {
		return err
	}

	_, err = script.Exec(`bash -c 'jq '.ghw_blockinfo.total_size_bytes' ` + wd + `/log_backups/*/node-info.json | grep -v null | sort -n | numfmt --to=iec | sort -h | uniq -c'`).Reject("G").AppendFile(tempStatsPath + "/mem.txt")
	if err != nil {
		return err
	}

	ramTotal, err := script.Echo(fmt.Sprintf("%d", totalramBytes)).ExecForEach("numfmt --to=iec {{.}}").String()
	if err != nil {
		return err
	}
	_, err = script.Echo(fmt.Sprintf("<u>Survey total RAM byte size (cumulative):</u> %s\n", ramTotal)).WriteFile(tempStatsPath + "/ram.txt")
	if err != nil {
		return err
	}

	_, err = script.Exec(`bash -c 'jq '.ghw_memoryinfo.total_usable_bytes' ` + wd + `/log_backups/*/node-info.json | grep -v null | sort -n | numfmt --to=iec | sort -h | uniq -c'`).Reject("G").AppendFile(tempStatsPath + "/ram.txt")
	if err != nil {
		return err
	}

	_, err = script.Exec(`bash -c 'jq '.ghw_memoryinfo.total_usable_bytes' ` + wd + `/log_backups/*/node-info.json | grep -v null | sort -n | numfmt --to=iec | sort -h | uniq -c'`).AppendFile(tempStatsPath + "/ram.txt")
	if err != nil {
		return err
	}

	// Generate country statistics
	err = generateAndCacheCountryStats()
	if err != nil {
		// Log but don't fail the whole stats generation
		fmt.Println("Warning: failed to generate country stats:", err)
	}

	return nil
}

// CountryStat represents visor count for a country
type CountryStat struct {
	Count       int    `json:"count"`
	CountryName string `json:"country_name"`
	CountryCode string `json:"country_code"`
	Flag        string `json:"flag"`
}

// CountryStatsResponse is the JSON response for country statistics
type CountryStatsResponse struct {
	Stats      []CountryStat `json:"stats"`
	TotalNodes int           `json:"total_nodes"`
	Generated  string        `json:"generated"`
}

// countryCodeToFlag converts a 2-letter country code to a flag emoji
func countryCodeToFlag(code string) string {
	if len(code) != 2 {
		return ""
	}
	code = strings.ToUpper(code)
	// Regional indicator symbols start at U+1F1E6 (🇦)
	// Each letter A-Z maps to U+1F1E6 to U+1F1FF
	r1 := rune(code[0]) - 'A' + 0x1F1E6
	r2 := rune(code[1]) - 'A' + 0x1F1E6
	return string([]rune{r1, r2})
}

// geoIPResult holds cached geoip lookup result
type geoIPResult struct {
	CountryCode string
	CountryName string
}

func generateAndCacheCountryStats() error {
	pks, err := script.ListFiles(wd + "/log_backups").Basename().Slice()
	if err != nil {
		return err
	}

	// First pass: collect all IPs and build unique IP set
	ipToVisors := make(map[string]int) // IP -> count of visors with this IP
	visorIPs := make([]string, 0)      // all visor IPs (including duplicates)

	for i := range pks {
		ni := wd + "/log_backups/" + pks[i] + "/node-info.json"

		// Extract IP address from node-info.json
		ipAddr, err := script.File(ni).JQ(".ip_address").Replace(`"`, "").Replace("\n", "").String()
		if err != nil || ipAddr == "" || ipAddr == "null" {
			continue
		}
		ipAddr = strings.TrimSpace(ipAddr)

		ipToVisors[ipAddr]++
		visorIPs = append(visorIPs, ipAddr)
	}

	// Second pass: query geoip for unique IPs using the embedded database directly
	// (avoids spawning a subprocess per IP)
	ipCache := make(map[string]geoIPResult)

	db, dbErr := geoip2.OpenBytes(geoipcmd.EmbeddedGeoIP())
	if dbErr != nil {
		fmt.Printf("Warning: failed to open embedded GeoIP database: %v\n", dbErr)
	} else {
		defer db.Close() //nolint:errcheck,gosec
		for ip := range ipToVisors {
			res, err := geoipcmd.LookupIP(db, ip)
			if err != nil {
				continue
			}
			if res.CountryCode != "" {
				ipCache[ip] = geoIPResult{
					CountryCode: res.CountryCode,
					CountryName: res.CountryName,
				}
			}
		}
	}

	// Build stats for unique IPs (deduplicated)
	uniqueCountryCount := make(map[string]int)
	countryNames := make(map[string]string)

	for ip := range ipToVisors {
		if geo, ok := ipCache[ip]; ok {
			uniqueCountryCount[geo.CountryCode]++
			if countryNames[geo.CountryCode] == "" {
				countryNames[geo.CountryCode] = geo.CountryName
			}
		}
	}

	// Build stats for all visors (full count)
	fullCountryCount := make(map[string]int)

	for _, ip := range visorIPs {
		if geo, ok := ipCache[ip]; ok {
			fullCountryCount[geo.CountryCode]++
		}
	}

	// Generate unique IP stats
	if err := writeCountryStats(uniqueCountryCount, countryNames, "country_unique", "Unique IPs by country"); err != nil {
		return err
	}

	// Generate full visor count stats
	if err := writeCountryStats(fullCountryCount, countryNames, "country_full", "Visor count by country"); err != nil {
		return err
	}

	return nil
}

func writeCountryStats(countryCount map[string]int, countryNames map[string]string, filePrefix, title string) error {
	var stats []CountryStat
	totalNodes := 0

	for code, count := range countryCount {
		stats = append(stats, CountryStat{
			Count:       count,
			CountryCode: code,
			CountryName: countryNames[code],
			Flag:        countryCodeToFlag(code),
		})
		totalNodes += count
	}

	// Sort by count descending
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].Count > stats[j].Count
	})

	// Generate plaintext output
	var plaintext strings.Builder
	plaintext.WriteString(title + ":\n\n")
	for _, s := range stats {
		fmt.Fprintf(&plaintext, "%d %s %s\n", s.Count, s.CountryName, s.Flag)
	}
	fmt.Fprintf(&plaintext, "\nTotal: %d\n", totalNodes)

	_, err := script.Echo(plaintext.String()).WriteFile(tempStatsPath + "/" + filePrefix + ".txt")
	if err != nil {
		return err
	}

	// Generate JSON output
	response := CountryStatsResponse{
		Stats:      stats,
		TotalNodes: totalNodes,
		Generated:  time.Now().Format(time.RFC3339),
	}
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(tempStatsPath+"/"+filePrefix+".json", jsonData, 0600)
}

// PieChartItem represents a single item in a pie chart
type PieChartItem struct {
	Label string
	Count int
}

// pieChartColors provides a palette of distinct colors for pie chart slices
var pieChartColors = []string{
	"#FF6384", "#36A2EB", "#FFCE56", "#4BC0C0", "#9966FF",
	"#FF9F40", "#E7E9ED", "#8BC34A", "#FF5722", "#607D8B",
	"#E91E63", "#00BCD4", "#CDDC39", "#795548", "#9C27B0",
	"#03A9F4", "#FFC107", "#673AB7", "#009688", "#F44336",
}

// ParseFrequencyStats parses frequency statistics from text format (e.g., "  10 amd64\n   5 arm64")
func ParseFrequencyStats(statsText string) []PieChartItem {
	var items []PieChartItem
	lines := strings.Split(statsText, "\n")
	// Match lines like "  10 amd64" or "   5 arm64"
	re := regexp.MustCompile(`^\s*(\d+)\s+(.+)$`)

	for _, line := range lines {
		matches := re.FindStringSubmatch(line)
		if len(matches) == 3 {
			count, err := strconv.Atoi(matches[1])
			if err != nil {
				continue
			}
			label := strings.TrimSpace(matches[2])
			if label != "" {
				items = append(items, PieChartItem{Label: label, Count: count})
			}
		}
	}
	return items
}

// VersionHistoryEntry represents version counts for a single day
type VersionHistoryEntry struct {
	Date     string         `json:"date"`
	Versions map[string]int `json:"versions"`
	Total    int            `json:"total"`
}

// UptimeVisor represents a visor entry from uptime tracker JSON
type UptimeVisor struct {
	PK      string            `json:"pk"`
	On      bool              `json:"on"`
	Version string            `json:"version"`
	Daily   map[string]string `json:"daily"`
}

// histCache memoizes ParseHistoricUptimeData. The parse globs every
// hist/*_ut.json, then reads and unmarshals all of them — hundreds of files,
// on EVERY request to /stats and /stats/version-history. The key is the newest
// file's modification time plus the file count, so a new daily drop (or a
// rewritten one) invalidates it immediately; it is not a timed cache and never
// serves data older than the directory.
var histCache struct {
	sync.Mutex
	key     string
	minUp   float64
	entries []VersionHistoryEntry
}

// histCacheKey summarizes the directory cheaply: newest mtime + file count.
func histCacheKey(files []string) string {
	var newest time.Time
	for _, f := range files {
		if info, err := os.Stat(f); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return fmt.Sprintf("%d|%d", len(files), newest.UnixNano())
}

// ParseHistoricUptimeData reads all _ut.json files and returns version counts per day
// Only counts visors that had >= minUptime% uptime on each specific day
func ParseHistoricUptimeData(histDir string, minUptime float64) ([]VersionHistoryEntry, error) {
	files, err := filepath.Glob(filepath.Join(histDir, "*_ut.json"))
	if err != nil {
		return nil, err
	}

	key := histCacheKey(files)
	histCache.Lock()
	if histCache.entries != nil && histCache.key == key && histCache.minUp == minUptime {
		cached := histCache.entries
		histCache.Unlock()
		return cached, nil
	}
	histCache.Unlock()

	// Sort files by date (newest first) so we use most recent data for each visor+date
	sort.Slice(files, func(i, j int) bool {
		return files[i] > files[j]
	})

	// Track which visor+date combinations we've already processed to avoid double-counting
	// Key: "date|pk", Value: version (already recorded)
	seen := make(map[string]string)

	// Map of date -> version -> count
	dateVersionCounts := make(map[string]map[string]int)

	for _, file := range files {
		data, err := os.ReadFile(file) //nolint:gosec
		if err != nil {
			continue
		}

		var visors []UptimeVisor
		if err := json.Unmarshal(data, &visors); err != nil {
			continue
		}

		// For each visor, check each day in their daily uptime
		for _, v := range visors {
			version := normalizeVersion(v.Version)
			if version == "" {
				continue
			}

			for date, uptimeStr := range v.Daily {
				uptime, err := strconv.ParseFloat(uptimeStr, 64)
				if err != nil {
					continue
				}

				// Only count if uptime >= minUptime
				if uptime < minUptime {
					continue
				}

				// Check if we've already counted this visor for this date
				key := date + "|" + v.PK
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = version

				if dateVersionCounts[date] == nil {
					dateVersionCounts[date] = make(map[string]int)
				}
				dateVersionCounts[date][version]++
			}
		}
	}

	// Convert to sorted slice
	var dates []string
	for d := range dateVersionCounts {
		dates = append(dates, d)
	}
	sort.Strings(dates)

	var history []VersionHistoryEntry
	for _, date := range dates {
		versions := dateVersionCounts[date]
		total := 0
		for _, count := range versions {
			total += count
		}
		history = append(history, VersionHistoryEntry{
			Date:     date,
			Versions: versions,
			Total:    total,
		})
	}

	histCache.Lock()
	histCache.key, histCache.minUp, histCache.entries = key, minUptime, history
	histCache.Unlock()

	return history, nil
}

// normalizeVersion cleans up version strings for consistent grouping
func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	// Remove "dirty" suffix variations but keep the base version
	v = strings.ReplaceAll(v, "+dirty", "")
	v = strings.ReplaceAll(v, " dirty", "")
	v = strings.ReplaceAll(v, "-dirty", "")
	v = strings.TrimSpace(v)
	return v
}

// compareVersions compares two version strings (simple semver comparison)
func compareVersions(a, b string) int {
	// Strip 'v' prefix
	a = strings.TrimPrefix(a, "v")
	b = strings.TrimPrefix(b, "v")

	partsA := strings.Split(a, ".")
	partsB := strings.Split(b, ".")

	for i := 0; i < len(partsA) && i < len(partsB); i++ {
		numA, errA := strconv.Atoi(partsA[i])
		numB, errB := strconv.Atoi(partsB[i])
		if errA != nil || errB != nil {
			// If parsing fails, compare as strings
			if partsA[i] < partsB[i] {
				return -1
			} else if partsA[i] > partsB[i] {
				return 1
			}
			continue
		}
		if numA != numB {
			return numA - numB
		}
	}
	return len(partsA) - len(partsB)
}

// versionChartBands caps how many versions get their own band. The network has
// carried 140+ distinct version strings; drawing every one gives an unreadable
// chart and a path per version. The rest are summed into "other versions",
// which is honest — the total still adds up to the day's visor count.
const versionChartBands = 12

// GenerateVersionHistoryChartHTML renders version adoption over time as an
// inline SVG stacked area.
//
// This previously emitted one absolutely-positioned 1-pixel <div> per plotted
// pixel — 14,417 elements and 2.1 MB of HTML for two years of history, which
// took eight seconds to serve. The same data is now a handful of <path>
// elements. Hover detail is a <title> per band rather than per pixel.
func GenerateVersionHistoryChartHTML(history []VersionHistoryEntry, chartWidth, chartHeight int) string {
	if len(history) == 0 {
		return "<p>No historic data available</p>"
	}
	if chartWidth <= 0 {
		chartWidth = 900
	}
	if chartHeight <= 0 {
		chartHeight = 300
	}

	// Rank versions by their PEAK SHARE of the network on any single day, then
	// order the chosen bands oldest-first so the stack reads as an adoption
	// curve. Ranking by total visor-days instead looks reasonable and is not:
	// it weights by how long a version survived, so the 2024 releases with two
	// years of accumulated days crowd out every version running today, and the
	// whole current network collapses into the "other" band. Peak share scores
	// a wave by how much of the network it actually held, whenever it held it.
	share := make(map[string]float64)
	for _, entry := range history {
		if entry.Total == 0 {
			continue
		}
		for v, c := range entry.Versions {
			if s := float64(c) / float64(entry.Total); s > share[v] {
				share[v] = s
			}
		}
	}
	if len(share) == 0 {
		return "<p>No version data available</p>"
	}
	ranked := sortedMapKeys(share)

	top := ranked
	var rest []string
	if len(ranked) > versionChartBands {
		top, rest = ranked[:versionChartBands], ranked[versionChartBands:]
	}
	sort.Slice(top, func(i, j int) bool { return compareVersions(top[i], top[j]) < 0 })

	var series []chartSeries
	if len(rest) > 0 {
		vals := make([]float64, len(history))
		restTotal := 0
		for i, entry := range history {
			sum := 0
			for _, v := range rest {
				sum += entry.Versions[v]
			}
			vals[i] = float64(sum)
			restTotal += sum
		}
		series = append(series, chartSeries{
			Name:  fmt.Sprintf("%d older/other versions", len(rest)),
			Color: "#555555",
			Vals:  vals,
			Note:  fmt.Sprintf("%d visor-days combined", restTotal),
		})
	}
	for i, v := range top {
		vals := make([]float64, len(history))
		peak := 0
		for di, entry := range history {
			c := entry.Versions[v]
			vals[di] = float64(c)
			if c > peak {
				peak = c
			}
		}
		series = append(series, chartSeries{
			Name:  v,
			Color: chartColors[i%len(chartColors)],
			Vals:  vals,
			Note:  fmt.Sprintf("peak %d visors", peak),
		})
	}

	dates := make([]string, len(history))
	for i, entry := range history {
		dates[i] = entry.Date
	}

	opts := chartOpts{
		Width: chartWidth, Height: chartHeight,
		Labels:     shortDates(dates),
		Title:      fmt.Sprintf("Version Distribution Over Time (≥75%% daily uptime, %d days)", len(history)),
		YAxisLabel: "visors per version, stacked",
		FormatY:    func(v float64) string { return strconv.FormatFloat(v, 'f', 0, 64) },
		MaxXLabels: 10,
	}
	out := renderStackedAreaSVG(opts, series)

	// The most recent days as text — the readable form of the right edge.
	latest := history[len(history)-1]
	out += fmt.Sprintf("<p>Latest: <b>%s</b> — %d visors at ≥75%% uptime.</p>",
		html.EscapeString(latest.Date), latest.Total)
	out += "<details style='margin:8px 0;'><summary style='cursor:pointer;color:#3399FF;'>latest day, all versions</summary><pre>"
	for _, v := range sortedMapKeys(latest.Versions) {
		out += fmt.Sprintf("%-28s %d\n", html.EscapeString(v), latest.Versions[v])
	}
	out += "</pre></details>"
	return out
}

// GeneratePieChartHTML generates a CSS-based pie chart with legend
// GeneratePieChartHTML generates a pie chart. All items are shown — no grouping.
// The maxSlices parameter is ignored (kept for API compatibility).
func GeneratePieChartHTML(items []PieChartItem, _ int) string {
	if len(items) == 0 {
		return ""
	}

	// Calculate total
	total := 0
	for _, item := range items {
		total += item.Count
	}
	if total == 0 {
		return ""
	}

	displayItems := items

	// Build conic-gradient
	var gradientParts []string
	currentDeg := 0.0
	for i, item := range displayItems {
		color := pieChartColors[i%len(pieChartColors)]
		percentage := float64(item.Count) / float64(total) * 360
		endDeg := currentDeg + percentage
		gradientParts = append(gradientParts, fmt.Sprintf("%s %.1fdeg %.1fdeg", color, currentDeg, endDeg))
		currentDeg = endDeg
	}

	gradient := strings.Join(gradientParts, ", ")

	// Build HTML with responsive layout
	// Pie chart on right on desktop, stacks vertically on mobile
	var sb strings.Builder
	sb.WriteString("<div style='display: flex; flex-direction: row-reverse; align-items: flex-start; gap: 20px; margin: 10px 0; flex-wrap: wrap;'>\n")

	// Pie chart (larger, on the right)
	fmt.Fprintf(&sb, "<div style='width: 200px; height: 200px; border-radius: 50%%; background: conic-gradient(%s); flex-shrink: 0;'></div>\n", gradient) //nolint:errcheck,gosec

	// Legend
	sb.WriteString("<div style='font-size: 12px; line-height: 1.5; flex: 1; min-width: 200px;'>\n")
	for i, item := range displayItems {
		color := pieChartColors[i%len(pieChartColors)]
		pct := float64(item.Count) / float64(total) * 100
		label := html.EscapeString(item.Label)
		if len(label) > 35 {
			label = label[:32] + "..."
		}
		fmt.Fprintf(&sb, "<div style='margin: 2px 0;'><span style='display: inline-block; width: 14px; height: 14px; background: %s; margin-right: 6px; vertical-align: middle;'></span>%s (%d, %.1f%%)</div>\n", color, label, item.Count, pct) //nolint:errcheck,gosec
	}
	fmt.Fprintf(&sb, "<div style='margin-top: 8px; font-weight: bold; font-size: 13px;'>Total: %d</div>\n", total) //nolint:errcheck,gosec
	sb.WriteString("</div>\n")

	sb.WriteString("</div>\n")
	return sb.String()
}

// BandwidthHistoryEntry represents bandwidth totals for a single day
type BandwidthHistoryEntry struct {
	Date       string            `json:"date"`
	Total      uint64            `json:"total"`
	ByVisor    map[string]uint64 `json:"by_visor,omitempty"`
	VisorCount int               `json:"visor_count"`
}

// ParseHistoricBandwidthData reads all *_bandwidth.json files from hist directory
func ParseHistoricBandwidthData(histDir string) ([]BandwidthHistoryEntry, error) {
	files, err := filepath.Glob(filepath.Join(histDir, "*_bandwidth.json"))
	if err != nil {
		return nil, err
	}

	sort.Strings(files) // chronological order

	var history []BandwidthHistoryEntry
	for _, file := range files {
		data, err := os.ReadFile(file) //nolint:gosec
		if err != nil {
			continue
		}

		var bwMap map[string]uint64
		if err := json.Unmarshal(data, &bwMap); err != nil {
			continue
		}

		// Extract date from filename: YYYY-MM-DD_bandwidth.json
		base := filepath.Base(file)
		date := strings.TrimSuffix(base, "_bandwidth.json")

		var total uint64
		for _, bw := range bwMap {
			total += bw
		}

		history = append(history, BandwidthHistoryEntry{
			Date:       date,
			Total:      total,
			ByVisor:    bwMap,
			VisorCount: len(bwMap),
		})
	}

	return history, nil
}

// GenerateBandwidthHistoryChartHTML creates a CSS-based bar chart showing daily network bandwidth
func GenerateBandwidthHistoryChartHTML(history []BandwidthHistoryEntry, chartWidth, chartHeight int) string {
	if len(history) == 0 {
		return "<p>No bandwidth history data available</p>"
	}

	// Find max total for scaling
	var maxTotal uint64
	for _, entry := range history {
		if entry.Total > maxTotal {
			maxTotal = entry.Total
		}
	}
	if maxTotal == 0 {
		return "<p>No bandwidth data points</p>"
	}

	var sb strings.Builder

	// Container
	sb.WriteString("<div style='margin: 20px 0; max-width: 100%; overflow-x: auto;'>\n")
	sb.WriteString("<h3>Daily Network Bandwidth (qualifying visors)</h3>\n")

	// Chart container with axes
	sb.WriteString("<div style='display: flex; align-items: flex-end; gap: 5px; min-width: fit-content;'>\n")

	// Y-axis labels
	sb.WriteString(fmt.Sprintf("<div style='display: flex; flex-direction: column; justify-content: space-between; height: %dpx; font-size: 11px; color: #888; text-align: right; padding-right: 5px; flex-shrink: 0;'>\n", chartHeight))
	sb.WriteString(fmt.Sprintf("<span>%s</span>\n", formatBytesChart(maxTotal)))
	sb.WriteString(fmt.Sprintf("<span>%s</span>\n", formatBytesChart(maxTotal*3/4)))
	sb.WriteString(fmt.Sprintf("<span>%s</span>\n", formatBytesChart(maxTotal/2)))
	sb.WriteString(fmt.Sprintf("<span>%s</span>\n", formatBytesChart(maxTotal/4)))
	sb.WriteString("<span>0</span>\n")
	sb.WriteString("</div>\n")

	// Chart area
	sb.WriteString(fmt.Sprintf("<div style='position: relative; min-width: %dpx; height: %dpx; border-left: 1px solid #444; border-bottom: 1px solid #444; background: #1a1a1a; flex-shrink: 0;'>\n", chartWidth, chartHeight))

	// Grid lines
	for i := 1; i <= 4; i++ {
		y := chartHeight - (chartHeight * i / 4)
		sb.WriteString(fmt.Sprintf("<div style='position: absolute; left: 0; right: 0; top: %dpx; border-top: 1px dashed #333;'></div>\n", y))
	}

	// Calculate bar width
	barWidth := chartWidth / len(history)
	if barWidth < 4 {
		barWidth = 4
	}

	// Draw bars
	for i, entry := range history {
		x := i * barWidth
		barHeight := int(entry.Total * uint64(chartHeight) / maxTotal) //nolint:gosec // chartHeight is always positive
		if barHeight < 1 && entry.Total > 0 {
			barHeight = 1
		}

		color := "#36A2EB"
		sb.WriteString(fmt.Sprintf("<div style='position: absolute; left: %dpx; bottom: 0; width: %dpx; height: %dpx; background: %s;' title='%s: %s (%d visors)'></div>\n",
			x, barWidth-1, barHeight, color, entry.Date, formatBytesChart(entry.Total), entry.VisorCount))
	}

	sb.WriteString("</div>\n") // chart area
	sb.WriteString("</div>\n") // flex container

	// X-axis labels
	labelInterval := len(history) / 10
	if labelInterval < 1 {
		labelInterval = 1
	}

	sb.WriteString(fmt.Sprintf("<div style='display: flex; margin-left: 40px; font-size: 10px; color: #888; min-width: %dpx;'>\n", chartWidth))
	for i, entry := range history {
		if i%labelInterval == 0 || i == len(history)-1 {
			dateParts := strings.Split(entry.Date, "-")
			label := entry.Date
			if len(dateParts) == 3 {
				label = dateParts[1] + "-" + dateParts[2]
			}
			width := barWidth * labelInterval
			sb.WriteString(fmt.Sprintf("<span style='width: %dpx; text-align: left;'>%s</span>\n", width, label))
		}
	}
	sb.WriteString("</div>\n")

	// Summary table
	sb.WriteString("<div style='margin-top: 15px; font-size: 12px;'>\n")
	if len(history) > 0 {
		latest := history[len(history)-1]
		sb.WriteString(fmt.Sprintf("<p>Latest: %s — %s total bandwidth across %d qualifying visors</p>\n",
			latest.Date, formatBytesChart(latest.Total), latest.VisorCount))
	}
	sb.WriteString("</div>\n")

	sb.WriteString("</div>\n") // container

	return sb.String()
}

// GenerateVisorBandwidthChartHTML creates a stacked bar chart showing per-visor bandwidth over time.
// Only the top maxVisors by total bandwidth are shown individually; the rest are grouped as "Other".
func GenerateVisorBandwidthChartHTML(history []BandwidthHistoryEntry, chartWidth, chartHeight, maxVisors int) string {
	if len(history) == 0 {
		return "<p>No bandwidth history data available</p>"
	}

	// Aggregate total bandwidth per visor across all days
	visorTotals := make(map[string]uint64)
	for _, entry := range history {
		for pk, bw := range entry.ByVisor {
			visorTotals[pk] += bw
		}
	}
	if len(visorTotals) == 0 {
		return "<p>No per-visor bandwidth data available</p>"
	}

	// Sort visors by total bandwidth descending
	type visorBW struct {
		PK    string
		Total uint64
	}
	var sorted []visorBW
	for pk, total := range visorTotals {
		sorted = append(sorted, visorBW{PK: pk, Total: total})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Total > sorted[j].Total
	})

	// Top N visors shown individually, rest grouped as "Other"
	topVisors := make([]string, 0, maxVisors)
	topSet := make(map[string]bool)
	for i, v := range sorted {
		if i >= maxVisors-1 && len(sorted) > maxVisors {
			break
		}
		topVisors = append(topVisors, v.PK)
		topSet[v.PK] = true
	}
	hasOther := len(sorted) > maxVisors

	// Build list of labels (short PKs + "Other")
	allLabels := make([]string, 0, len(topVisors)+1)
	allLabels = append(allLabels, topVisors...)
	if hasOther {
		allLabels = append(allLabels, "Other")
	}

	// Find max daily total for Y-axis scaling
	var maxTotal uint64
	for _, entry := range history {
		if entry.Total > maxTotal {
			maxTotal = entry.Total
		}
	}
	if maxTotal == 0 {
		return "<p>No bandwidth data points</p>"
	}

	// Assign colors
	labelColors := make(map[string]string)
	for i, label := range allLabels {
		labelColors[label] = pieChartColors[i%len(pieChartColors)]
	}

	var sb strings.Builder

	sb.WriteString("<div style='margin: 20px 0; max-width: 100%; overflow-x: auto;'>\n")
	sb.WriteString("<h3>Per-Visor Bandwidth Over Time</h3>\n")

	// Chart container
	sb.WriteString("<div style='display: flex; align-items: flex-end; gap: 5px; min-width: fit-content;'>\n")

	// Y-axis
	sb.WriteString(fmt.Sprintf("<div style='display: flex; flex-direction: column; justify-content: space-between; height: %dpx; font-size: 11px; color: #888; text-align: right; padding-right: 5px; flex-shrink: 0;'>\n", chartHeight))
	sb.WriteString(fmt.Sprintf("<span>%s</span>\n", formatBytesChart(maxTotal)))
	sb.WriteString(fmt.Sprintf("<span>%s</span>\n", formatBytesChart(maxTotal*3/4)))
	sb.WriteString(fmt.Sprintf("<span>%s</span>\n", formatBytesChart(maxTotal/2)))
	sb.WriteString(fmt.Sprintf("<span>%s</span>\n", formatBytesChart(maxTotal/4)))
	sb.WriteString("<span>0</span>\n")
	sb.WriteString("</div>\n")

	// Chart area
	sb.WriteString(fmt.Sprintf("<div style='position: relative; min-width: %dpx; height: %dpx; border-left: 1px solid #444; border-bottom: 1px solid #444; background: #1a1a1a; flex-shrink: 0;'>\n", chartWidth, chartHeight))

	// Grid lines
	for i := 1; i <= 4; i++ {
		y := chartHeight - (chartHeight * i / 4)
		sb.WriteString(fmt.Sprintf("<div style='position: absolute; left: 0; right: 0; top: %dpx; border-top: 1px dashed #333;'></div>\n", y))
	}

	barWidth := chartWidth / len(history)
	if barWidth < 2 {
		barWidth = 2
	}

	// Draw stacked bars
	for i, entry := range history {
		x := i * barWidth
		currentY := 0

		// Draw top visors
		for _, pk := range topVisors {
			bw := entry.ByVisor[pk]
			if bw == 0 {
				continue
			}
			segmentHeight := int(bw * uint64(chartHeight) / maxTotal) //nolint:gosec
			if segmentHeight < 1 {
				segmentHeight = 1
			}
			color := labelColors[pk]
			sb.WriteString(fmt.Sprintf("<div style='position: absolute; left: %dpx; bottom: %dpx; width: %dpx; height: %dpx; background: %s;' title='%s: %s (%s)'></div>\n",
				x, currentY, barWidth-1, segmentHeight, color, entry.Date, pk, formatBytesChart(bw)))
			currentY += segmentHeight
		}

		// Draw "Other" aggregate
		if hasOther {
			var otherBW uint64
			for pk, bw := range entry.ByVisor {
				if !topSet[pk] {
					otherBW += bw
				}
			}
			if otherBW > 0 {
				segmentHeight := int(otherBW * uint64(chartHeight) / maxTotal) //nolint:gosec
				if segmentHeight < 1 {
					segmentHeight = 1
				}
				color := labelColors["Other"]
				sb.WriteString(fmt.Sprintf("<div style='position: absolute; left: %dpx; bottom: %dpx; width: %dpx; height: %dpx; background: %s;' title='%s: Other (%s)'></div>\n",
					x, currentY, barWidth-1, segmentHeight, color, entry.Date, formatBytesChart(otherBW)))
			}
		}
	}

	sb.WriteString("</div>\n") // chart area
	sb.WriteString("</div>\n") // flex container

	// X-axis labels
	labelInterval := len(history) / 10
	if labelInterval < 1 {
		labelInterval = 1
	}
	sb.WriteString(fmt.Sprintf("<div style='display: flex; margin-left: 40px; font-size: 10px; color: #888; min-width: %dpx;'>\n", chartWidth))
	for i, entry := range history {
		if i%labelInterval == 0 || i == len(history)-1 {
			dateParts := strings.Split(entry.Date, "-")
			label := entry.Date
			if len(dateParts) == 3 {
				label = dateParts[1] + "-" + dateParts[2]
			}
			width := barWidth * labelInterval
			sb.WriteString(fmt.Sprintf("<span style='width: %dpx; text-align: left;'>%s</span>\n", width, label))
		}
	}
	sb.WriteString("</div>\n")

	// Legend — show full PKs with total bandwidth
	sb.WriteString("<div style='margin-top: 15px; display: flex; flex-wrap: wrap; gap: 8px 15px; font-size: 12px;'>\n")
	for _, pk := range topVisors {
		color := labelColors[pk]
		sb.WriteString(fmt.Sprintf("<span style='display: inline-flex; align-items: center; gap: 4px;'><span style='display: inline-block; width: 12px; height: 12px; background: %s; flex-shrink: 0;'></span><span style='font-family: monospace; word-break: break-all;'>%s</span> (%s)</span>\n",
			color, html.EscapeString(pk), formatBytesChart(visorTotals[pk])))
	}
	if hasOther {
		var otherTotal uint64
		for _, v := range sorted[maxVisors-1:] {
			otherTotal += v.Total
		}
		color := labelColors["Other"]
		sb.WriteString(fmt.Sprintf("<span style='display: inline-flex; align-items: center; gap: 4px; white-space: nowrap;'><span style='display: inline-block; width: 12px; height: 12px; background: %s; flex-shrink: 0;'></span>Other (%d visors, %s)</span>\n",
			color, len(sorted)-(maxVisors-1), formatBytesChart(otherTotal)))
	}
	sb.WriteString("</div>\n")

	sb.WriteString("</div>\n") // container

	return sb.String()
}

// formatBytesChart converts bytes to human-readable for chart labels
func formatBytesChart(b uint64) string {
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

// fetchTPDBandwidthMetrics fetches bandwidth metrics from TPD for the given number of days
// statsDmsgHTTPClient carries a dmsghttp RoundTripper so the stats handlers can
// fetch the deployment's dmsg://<pk>:80 service URLs (TPD /metrics, …). Set once the
// reward server's dmsg client is up (see server.go). Nil in standalone/test
// contexts, where statsHTTPGet falls back to the default client.
var statsDmsgHTTPClient *http.Client

// statsHTTPGet GETs url with the dmsg-capable client when available — required for
// dmsg://<pk>:80 deployment URLs, which the default client rejects with
// "unsupported protocol scheme dmsg" — otherwise the default client.
func statsHTTPGet(url string) (*http.Response, error) {
	if statsDmsgHTTPClient != nil {
		return statsDmsgHTTPClient.Get(url) //nolint:noctx
	}
	return http.Get(url) //nolint:noctx,gosec
}

func fetchTPDBandwidthMetrics(days int) ([]tpdTransportMetric, error) {
	tpdURL := strings.TrimSuffix(deployment.Prod.TransportDiscovery, "/")
	url := fmt.Sprintf("%s/metrics?days=%d&bandwidth=true&latency=false", tpdURL, days)

	resp, err := statsHTTPGet(url)
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

	var metrics []tpdTransportMetric
	if err := json.Unmarshal(body, &metrics); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	return metrics, nil
}

// tpdTransportMetric represents a transport metric from TPD /metrics endpoint
type tpdTransportMetric struct {
	ID    string   `json:"id"`
	Type  string   `json:"type"`
	Live  bool     `json:"live"`
	Edges []string `json:"edges,omitempty"`
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

// renderTPDBandwidthTable renders TPD transport metrics as an HTML table
func renderTPDBandwidthTable(metrics []tpdTransportMetric) string {
	if len(metrics) == 0 {
		return "<p>No transport metrics available from TPD</p>"
	}

	// Collect all dates across all transports
	dateSet := make(map[string]struct{})
	for _, m := range metrics {
		for _, d := range m.Daily {
			dateSet[d.Date] = struct{}{}
		}
	}
	var dates []string
	for d := range dateSet {
		dates = append(dates, d)
	}
	sort.Strings(dates)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("<p>Showing %d transports with bandwidth data over %d days</p>\n", len(metrics), len(dates)))
	sb.WriteString("<table style='border-collapse: collapse; font-size: 10px;'>\n")
	sb.WriteString("<thead><tr style='border-bottom: 1px solid #555;'>")
	sb.WriteString("<th style='padding: 4px 8px; text-align: left;'>Transport</th>")
	sb.WriteString("<th style='padding: 4px 8px;'>Type</th>")
	sb.WriteString("<th style='padding: 4px 8px;'>Live</th>")
	for _, date := range dates {
		dateParts := strings.Split(date, "-")
		label := date
		if len(dateParts) == 3 {
			label = dateParts[1] + "-" + dateParts[2]
		}
		sb.WriteString(fmt.Sprintf("<th style='padding: 4px 8px;'>%s</th>", label))
	}
	sb.WriteString("</tr></thead>\n<tbody>\n")

	for _, m := range metrics {
		// Build a map of date -> bandwidth for this transport
		dailyBW := make(map[string]uint64)
		for _, d := range m.Daily {
			var bw uint64
			if d.A != nil {
				bw += d.A.Sent + d.A.Recv
			}
			if d.B != nil {
				bw += d.B.Sent + d.B.Recv
			}
			dailyBW[d.Date] = bw
		}

		liveStr := "<span style='color:#FF6384'>no</span>"
		if m.Live {
			liveStr = "<span style='color:#4BC0C0'>yes</span>"
		}

		sb.WriteString("<tr style='border-bottom: 1px solid #333;'>")
		sb.WriteString(fmt.Sprintf("<td style='padding: 4px 8px; font-size: 11px;'>%s</td>", m.ID))
		sb.WriteString(fmt.Sprintf("<td style='padding: 4px 8px; text-align: center;'>%s</td>", m.Type))
		sb.WriteString(fmt.Sprintf("<td style='padding: 4px 8px; text-align: center;'>%s</td>", liveStr))

		for _, date := range dates {
			bw := dailyBW[date]
			var color string
			switch {
			case bw == 0:
				color = "#666"
			case bw < 1024:
				color = "#FFCE56" // yellow - very low
			default:
				color = "#36A2EB" // blue - verified
			}
			sb.WriteString(fmt.Sprintf("<td style='padding: 4px 8px; text-align: right; color: %s;'>%s</td>", color, formatBytesChart(bw)))
		}
		sb.WriteString("</tr>\n")
	}

	sb.WriteString("</tbody></table>\n")
	return sb.String()
}

// tpdNetworkSummary holds cached network-wide TPD metrics summary.
type tpdNetworkSummary struct {
	TotalTransports int            `json:"total_transports"`
	ByType          map[string]int `json:"by_type"`
	TotalBandwidth  uint64         `json:"total_bandwidth"`
	UniqueVisors    int            `json:"unique_visors"`
	LastUpdated     string         `json:"last_updated"`
	// live/latency counts are gone with the per-transport reduction they came
	// from: /all-transports/stats reports no live-vs-total split, and the
	// aggregate carries no latency. Reporting either as a zero would have been
	// worse than not reporting it.
	// BandwidthOK distinguishes "verified total is zero" from "the bandwidth
	// fetch did not succeed". Without it a failed fetch renders as 0 B, which
	// reads as a real measurement.
	BandwidthOK bool `json:"bandwidth_ok"`
	// BandwidthErr carries why the bandwidth fetch failed, so the page can say
	// so instead of quietly showing a zero.
	BandwidthErr string `json:"bandwidth_err,omitempty"`
}

const tpdSummaryCacheFile = "tpd_summary.json"

const tpdSummaryCacheMaxAge = 5 * time.Minute

// getTPDNetworkSummary returns cached TPD network summary, fetching fresh data
// only if the cache is stale (on-demand caching).
func getTPDNetworkSummary() (*tpdNetworkSummary, error) {
	cachePath := filepath.Join(tempStatsPath, tpdSummaryCacheFile)

	// Check if cache is fresh
	info, err := os.Stat(cachePath)
	if err == nil && time.Since(info.ModTime()) <= tpdSummaryCacheMaxAge {
		data, err := os.ReadFile(cachePath) //nolint:gosec
		if err == nil {
			var summary tpdNetworkSummary
			if json.Unmarshal(data, &summary) == nil {
				return &summary, nil
			}
		}
	}

	// Cache is stale or missing — fetch fresh data.
	//
	// The counts come from TPD's own aggregate rather than by reducing the
	// per-transport bodies. /metrics?days=1&bandwidth=true&latency=true&edges=true
	// is 24 MB over dmsg and was failing outright with EOF, which took the whole
	// summary down — the page rendered nothing but the error. /all-transports/stats
	// is 138 bytes and carries exactly the three numbers the counts need.
	tpdURL := strings.TrimSuffix(deployment.Prod.TransportDiscovery, "/")

	summary := &tpdNetworkSummary{
		ByType:      make(map[string]int),
		LastUpdated: time.Now().UTC().Format(time.RFC3339),
	}
	if err := fillTPDCounts(tpdURL, summary); err != nil {
		return nil, err
	}

	// Bandwidth is fetched separately and is BEST EFFORT: it needs the
	// per-transport daily records, because the figure reported here is the
	// min()-verified one that mirrors the reward calculation (see the trust
	// model below), not TPD's own cumulative aggregate. Dropping edges and
	// latency from that query removes roughly a third of the body. If it still
	// fails, the counts above are already good and the summary says bandwidth
	// is unavailable instead of reporting a zero that looks like real data.
	if err := fillTPDBandwidth(tpdURL, summary); err != nil {
		summary.BandwidthErr = err.Error()
	}

	// Write to cache
	if cacheData, err := json.MarshalIndent(summary, "", "  "); err == nil {
		os.WriteFile(cachePath, cacheData, 0600) //nolint:errcheck,gosec
	}
	return summary, nil
}

// fillTPDCounts populates the transport/visor counts from TPD's small
// aggregate endpoint.
func fillTPDCounts(tpdURL string, summary *tpdNetworkSummary) error {
	resp, err := statsHTTPGet(tpdURL + "/all-transports/stats")
	if err != nil {
		return fmt.Errorf("TPD request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("TPD returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read TPD response: %w", err)
	}
	var stats struct {
		TotalTransports int            `json:"total_transports"`
		ByType          map[string]int `json:"by_type"`
		UniqueVisors    int            `json:"unique_visors"`
	}
	if err := json.Unmarshal(body, &stats); err != nil {
		return fmt.Errorf("failed to parse TPD stats: %w", err)
	}
	summary.TotalTransports = stats.TotalTransports
	summary.UniqueVisors = stats.UniqueVisors
	for k, v := range stats.ByType {
		summary.ByType[k] = v
	}
	return nil
}

// fillTPDBandwidth adds the verified-bandwidth total. Separate from the counts
// so a failure here costs only that one line of the summary.
func fillTPDBandwidth(tpdURL string, summary *tpdNetworkSummary) error {
	url := fmt.Sprintf("%s/metrics?days=1&bandwidth=true&latency=false&edges=false", tpdURL)

	resp, err := statsHTTPGet(url)
	if err != nil {
		return fmt.Errorf("TPD request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("TPD returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read TPD response: %w", err)
	}

	var metrics []struct {
		ID      string   `json:"id"`
		Type    string   `json:"type"`
		Live    bool     `json:"live"`
		Edges   []string `json:"edges,omitempty"`
		Latency *struct {
			Avg int64 `json:"avg"`
		} `json:"latency,omitempty"`
		Daily []struct {
			A *struct {
				Sent uint64 `json:"sent"`
				Recv uint64 `json:"recv"`
			} `json:"a,omitempty"`
			B *struct {
				Sent uint64 `json:"sent"`
				Recv uint64 `json:"recv"`
			} `json:"b,omitempty"`
		} `json:"daily"`
	}
	if err := json.Unmarshal(body, &metrics); err != nil {
		return fmt.Errorf("failed to parse TPD metrics: %w", err)
	}

	for _, m := range metrics {
		for _, daily := range m.Daily {
			// Mirror the canonical three-branch trust model used by the reward
			// bw-collect (cmd/.../rewards/transports.go) and `cli tp metrics`
			// (verifiedBandwidth): an edge whose record is present but
			// {sent:0,recv:0} has "not reported yet", NOT "verified zero". Key
			// on reported-data, not record presence — otherwise a present
			// {0,0} edge takes the min() branch and zeroes the counterparty's
			// real bandwidth (e.g. min(0, 986MB)=0), which is exactly why
			// single-edge-reporting transports showed 0 verified bandwidth here
			// while the actual reward calc credited them.
			aReported := daily.A != nil && (daily.A.Sent > 0 || daily.A.Recv > 0)
			bReported := daily.B != nil && (daily.B.Sent > 0 || daily.B.Recv > 0)
			switch {
			case aReported && bReported:
				aToB := daily.A.Sent
				if daily.B.Recv < aToB {
					aToB = daily.B.Recv
				}
				bToA := daily.A.Recv
				if daily.B.Sent < bToA {
					bToA = daily.B.Sent
				}
				summary.TotalBandwidth += aToB + bToA
			case aReported:
				summary.TotalBandwidth += daily.A.Sent + daily.A.Recv
			case bReported:
				summary.TotalBandwidth += daily.B.Sent + daily.B.Recv
			}
		}
	}

	summary.BandwidthOK = true
	return nil
}

// renderTPDNetworkSummaryHTML renders the TPD network summary as HTML.
func renderTPDNetworkSummaryHTML() string {
	summary, err := getTPDNetworkSummary()
	if err != nil {
		return fmt.Sprintf("<p style='color: #FF6384;'>Error fetching TPD network summary: %v</p>", err)
	}

	l := fmt.Sprintf("<h2>Transport Discovery Network Summary (%s)</h2>", time.Now().UTC().Format("2006-01-02"))
	l += "<pre>"
	l += fmt.Sprintf("Total Transports:  %d\n", summary.TotalTransports)
	l += fmt.Sprintf("Unique Visors:     %d\n", summary.UniqueVisors)

	// Transport counts by type, ordered biggest first. Ranging the map
	// directly reshuffled this list on every render, so the same data looked
	// like it had changed between refreshes.
	l += "Transports by Type:\n"
	for _, tpType := range sortedTransportTypes(summary.ByType) {
		l += fmt.Sprintf("  %-8s %d\n", tpType, summary.ByType[tpType])
	}

	// Bandwidth — best effort, and said plainly when it is missing rather than
	// rendered as a zero that reads like a measurement.
	if summary.BandwidthOK {
		l += fmt.Sprintf("Network Bandwidth: %s (verified, 1 day)\n", formatBytesChart(summary.TotalBandwidth))
	} else {
		l += "Network Bandwidth: unavailable\n"
	}

	l += "</pre>"
	l += fmt.Sprintf("<p style='color: #888; font-size: 0.8em;'>Last updated: %s</p>", summary.LastUpdated)
	return l
}

// sortedTransportTypes orders transport types biggest first, ties broken by
// name. Ranging the map directly reshuffled the list on every render, so
// unchanged data appeared to move between page refreshes.
func sortedTransportTypes(byType map[string]int) []string {
	types := make([]string, 0, len(byType))
	for tpType := range byType {
		types = append(types, tpType)
	}
	sort.Slice(types, func(i, j int) bool {
		if byType[types[i]] != byType[types[j]] {
			return byType[types[i]] > byType[types[j]]
		}
		return types[i] < types[j]
	})
	return types
}
