// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/logo.go
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
	"time"

	"github.com/bitfield/script"

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

	// Second pass: query geoip for unique IPs only (cache results)
	ipCache := make(map[string]geoIPResult)

	for ip := range ipToVisors {
		// Query geoip for this IP
		geoResult, err := script.Exec(`skywire svc ip ` + ip).String()
		if err != nil {
			continue
		}

		// Parse JSON response (skip log line if present)
		lines := strings.Split(geoResult, "\n")
		var jsonStr string
		for _, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "{") {
				idx := strings.Index(geoResult, line)
				jsonStr = geoResult[idx:]
				break
			}
		}

		var geoData struct {
			CountryCode string `json:"country_code"`
			CountryName string `json:"country_name"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &geoData); err != nil {
			continue
		}

		if geoData.CountryCode != "" {
			ipCache[ip] = geoIPResult{
				CountryCode: geoData.CountryCode,
				CountryName: geoData.CountryName,
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
		plaintext.WriteString(fmt.Sprintf("%d %s %s\n", s.Count, s.CountryName, s.Flag))
	}
	plaintext.WriteString(fmt.Sprintf("\nTotal: %d\n", totalNodes))

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

// ParseHistoricUptimeData reads all _ut.json files and returns version counts per day
// Only counts visors that had >= minUptime% uptime on each specific day
func ParseHistoricUptimeData(histDir string, minUptime float64) ([]VersionHistoryEntry, error) {
	files, err := filepath.Glob(filepath.Join(histDir, "*_ut.json"))
	if err != nil {
		return nil, err
	}

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

// getAllVersions extracts all unique versions from history, sorted by first appearance
func getAllVersions(history []VersionHistoryEntry) []string {
	versionSet := make(map[string]int) // version -> first seen index
	for i, entry := range history {
		for version := range entry.Versions {
			if _, exists := versionSet[version]; !exists {
				versionSet[version] = i
			}
		}
	}

	// Sort versions by semantic version (newest first for legend)
	var versions []string
	for v := range versionSet {
		versions = append(versions, v)
	}
	sort.Slice(versions, func(i, j int) bool {
		return compareVersions(versions[i], versions[j]) > 0
	})
	return versions
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

// GenerateVersionHistoryChartHTML creates a CSS-based stacked area chart
func GenerateVersionHistoryChartHTML(history []VersionHistoryEntry, chartWidth, chartHeight int) string {
	if len(history) == 0 {
		return "<p>No historic data available</p>"
	}

	versions := getAllVersions(history)
	if len(versions) == 0 {
		return "<p>No version data available</p>"
	}

	// Find max total for scaling
	maxTotal := 0
	for _, entry := range history {
		if entry.Total > maxTotal {
			maxTotal = entry.Total
		}
	}
	if maxTotal == 0 {
		return "<p>No data points</p>"
	}

	// Assign colors to versions
	versionColors := make(map[string]string)
	for i, v := range versions {
		versionColors[v] = pieChartColors[i%len(pieChartColors)]
	}

	var sb strings.Builder

	// Container with responsive styles
	sb.WriteString("<div style='margin: 20px 0; max-width: 100%; overflow-x: auto;'>\n")
	sb.WriteString("<h3>Version Distribution Over Time (≥75% daily uptime)</h3>\n")

	// Chart container with axes
	sb.WriteString("<div style='display: flex; align-items: flex-end; gap: 5px; min-width: fit-content;'>\n")

	// Y-axis labels
	sb.WriteString(fmt.Sprintf("<div style='display: flex; flex-direction: column; justify-content: space-between; height: %dpx; font-size: 11px; color: #888; text-align: right; padding-right: 5px; flex-shrink: 0;'>\n", chartHeight))
	sb.WriteString(fmt.Sprintf("<span>%d</span>\n", maxTotal))
	sb.WriteString(fmt.Sprintf("<span>%d</span>\n", maxTotal*3/4))
	sb.WriteString(fmt.Sprintf("<span>%d</span>\n", maxTotal/2))
	sb.WriteString(fmt.Sprintf("<span>%d</span>\n", maxTotal/4))
	sb.WriteString("<span>0</span>\n")
	sb.WriteString("</div>\n")

	// Chart area - use min-width so it can scroll on mobile
	sb.WriteString(fmt.Sprintf("<div style='position: relative; min-width: %dpx; height: %dpx; border-left: 1px solid #444; border-bottom: 1px solid #444; background: #1a1a1a; flex-shrink: 0;'>\n", chartWidth, chartHeight))

	// Grid lines
	for i := 1; i <= 4; i++ {
		y := chartHeight - (chartHeight * i / 4)
		sb.WriteString(fmt.Sprintf("<div style='position: absolute; left: 0; right: 0; top: %dpx; border-top: 1px dashed #333;'></div>\n", y))
	}

	// Calculate bar width
	barWidth := chartWidth / len(history)
	if barWidth < 2 {
		barWidth = 2
	}

	// Draw stacked bars for each day
	for i, entry := range history {
		x := i * barWidth
		currentY := 0

		// Draw each version's segment (stacked)
		for _, version := range versions {
			count := entry.Versions[version]
			if count == 0 {
				continue
			}

			segmentHeight := count * chartHeight / maxTotal
			if segmentHeight < 1 {
				segmentHeight = 1
			}

			color := versionColors[version]
			bottom := currentY
			currentY += segmentHeight

			sb.WriteString(fmt.Sprintf("<div style='position: absolute; left: %dpx; bottom: %dpx; width: %dpx; height: %dpx; background: %s;' title='%s: %s (%d)'></div>\n",
				x, bottom, barWidth-1, segmentHeight, color, entry.Date, version, count))
		}
	}

	sb.WriteString("</div>\n") // chart area
	sb.WriteString("</div>\n") // flex container

	// X-axis labels (show every Nth date to avoid crowding)
	labelInterval := len(history) / 10
	if labelInterval < 1 {
		labelInterval = 1
	}

	sb.WriteString(fmt.Sprintf("<div style='position: relative; margin-left: 40px; height: 20px; min-width: %dpx; font-size: 10px; color: #888;'>\n", chartWidth))
	for i, entry := range history {
		if i%labelInterval == 0 || i == len(history)-1 {
			dateParts := strings.Split(entry.Date, "-")
			label := entry.Date
			if len(dateParts) == 3 {
				label = dateParts[1] + "-" + dateParts[2]
			}
			x := i * barWidth
			sb.WriteString(fmt.Sprintf("<span style='position: absolute; left: %dpx;'>%s</span>\n", x, label))
		}
	}
	sb.WriteString("</div>\n")

	// Legend - responsive wrapping
	sb.WriteString("<div style='margin-top: 15px; display: flex; flex-wrap: wrap; gap: 8px 15px; font-size: 12px;'>\n")
	for _, version := range versions {
		color := versionColors[version]
		sb.WriteString(fmt.Sprintf("<span style='display: inline-flex; align-items: center; gap: 4px; white-space: nowrap;'><span style='display: inline-block; width: 12px; height: 12px; background: %s; flex-shrink: 0;'></span>%s</span>\n", color, html.EscapeString(version)))
	}
	sb.WriteString("</div>\n")

	sb.WriteString("</div>\n") // container

	return sb.String()
}

// GeneratePieChartHTML generates a CSS-based pie chart with legend
// maxSlices limits how many distinct slices to show (rest grouped as "Other")
func GeneratePieChartHTML(items []PieChartItem, maxSlices int) string {
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

	// Group small items into "Other" if we have too many
	var displayItems []PieChartItem
	otherCount := 0
	for i, item := range items {
		if i < maxSlices-1 || len(items) <= maxSlices {
			displayItems = append(displayItems, item)
		} else {
			otherCount += item.Count
		}
	}
	if otherCount > 0 {
		displayItems = append(displayItems, PieChartItem{Label: "Other", Count: otherCount})
	}

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
	sb.WriteString(fmt.Sprintf("<div style='width: 200px; height: 200px; border-radius: 50%%; background: conic-gradient(%s); flex-shrink: 0;'></div>\n", gradient))

	// Legend
	sb.WriteString("<div style='font-size: 12px; line-height: 1.5; flex: 1; min-width: 200px;'>\n")
	for i, item := range displayItems {
		color := pieChartColors[i%len(pieChartColors)]
		pct := float64(item.Count) / float64(total) * 100
		label := html.EscapeString(item.Label)
		if len(label) > 35 {
			label = label[:32] + "..."
		}
		sb.WriteString(fmt.Sprintf("<div style='margin: 2px 0;'><span style='display: inline-block; width: 14px; height: 14px; background: %s; margin-right: 6px; vertical-align: middle;'></span>%s (%d, %.1f%%)</div>\n", color, label, item.Count, pct))
	}
	sb.WriteString(fmt.Sprintf("<div style='margin-top: 8px; font-weight: bold; font-size: 13px;'>Total: %d</div>\n", total))
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
			shortPK := pk
			if len(shortPK) > 8 {
				shortPK = shortPK[:8] + "..."
			}
			sb.WriteString(fmt.Sprintf("<div style='position: absolute; left: %dpx; bottom: %dpx; width: %dpx; height: %dpx; background: %s;' title='%s: %s (%s)'></div>\n",
				x, currentY, barWidth-1, segmentHeight, color, entry.Date, shortPK, formatBytesChart(bw)))
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

	// Legend — show short PKs with total bandwidth
	sb.WriteString("<div style='margin-top: 15px; display: flex; flex-wrap: wrap; gap: 8px 15px; font-size: 12px;'>\n")
	for _, pk := range topVisors {
		color := labelColors[pk]
		shortPK := pk
		if len(shortPK) > 16 {
			shortPK = shortPK[:16] + "..."
		}
		sb.WriteString(fmt.Sprintf("<span style='display: inline-flex; align-items: center; gap: 4px; white-space: nowrap;'><span style='display: inline-block; width: 12px; height: 12px; background: %s; flex-shrink: 0;'></span>%s (%s)</span>\n",
			color, html.EscapeString(shortPK), formatBytesChart(visorTotals[pk])))
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
func fetchTPDBandwidthMetrics(days int) ([]tpdTransportMetric, error) {
	tpdURL := strings.TrimSuffix(deployment.Prod.TransportDiscovery, "/")
	url := fmt.Sprintf("%s/metrics?days=%d&bandwidth=true&latency=false", tpdURL, days)

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
	LiveTransports  int            `json:"live_transports"`
	ByType          map[string]int `json:"by_type"`
	TotalBandwidth  uint64         `json:"total_bandwidth"`
	AvgLatencyMs    float64        `json:"avg_latency_ms"`
	LatencyCount    int            `json:"latency_count"`
	UniqueVisors    int            `json:"unique_visors"`
	LastUpdated     string         `json:"last_updated"`
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

	// Cache is stale or missing — fetch fresh data
	tpdURL := strings.TrimSuffix(deployment.Prod.TransportDiscovery, "/")
	url := fmt.Sprintf("%s/metrics?days=1&bandwidth=true&latency=true&edges=true", tpdURL)

	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("TPD request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("TPD returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read TPD response: %w", err)
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
		return nil, fmt.Errorf("failed to parse TPD metrics: %w", err)
	}

	// Compute summary
	summary := &tpdNetworkSummary{
		TotalTransports: len(metrics),
		ByType:          make(map[string]int),
		LastUpdated:     time.Now().UTC().Format(time.RFC3339),
	}

	visors := make(map[string]bool)
	var latencySum float64

	for _, m := range metrics {
		summary.ByType[m.Type]++
		if m.Live {
			summary.LiveTransports++
		}
		for _, edge := range m.Edges {
			visors[edge] = true
		}
		if m.Latency != nil && m.Latency.Avg > 0 {
			latencySum += float64(m.Latency.Avg) / 1000.0 // microseconds to ms
			summary.LatencyCount++
		}
		for _, daily := range m.Daily {
			if daily.A != nil && daily.B != nil {
				aToB := daily.A.Sent
				if daily.B.Recv < aToB {
					aToB = daily.B.Recv
				}
				bToA := daily.A.Recv
				if daily.B.Sent < bToA {
					bToA = daily.B.Sent
				}
				summary.TotalBandwidth += aToB + bToA
			} else if daily.A != nil {
				summary.TotalBandwidth += daily.A.Sent + daily.A.Recv
			} else if daily.B != nil {
				summary.TotalBandwidth += daily.B.Sent + daily.B.Recv
			}
		}
	}

	summary.UniqueVisors = len(visors)
	if summary.LatencyCount > 0 {
		summary.AvgLatencyMs = latencySum / float64(summary.LatencyCount)
	}

	// Write to cache
	cacheData, err := json.MarshalIndent(summary, "", "  ")
	if err == nil {
		os.WriteFile(cachePath, cacheData, 0600) //nolint:errcheck,gosec
	}

	return summary, nil
}

// renderTPDNetworkSummaryHTML renders the TPD network summary as HTML.
func renderTPDNetworkSummaryHTML() string {
	summary, err := getTPDNetworkSummary()
	if err != nil {
		return fmt.Sprintf("<p style='color: #FF6384;'>Error fetching TPD network summary: %v</p>", err)
	}

	l := fmt.Sprintf("<h2>Transport Discovery Network Summary (%s)</h2>", time.Now().UTC().Format("2006-01-02"))
	l += "<pre>"
	l += fmt.Sprintf("Total Transports:  %d (%d live)\n", summary.TotalTransports, summary.LiveTransports)
	l += fmt.Sprintf("Unique Visors:     %d\n", summary.UniqueVisors)

	// Transport counts by type
	l += "Transports by Type:\n"
	for tpType, count := range summary.ByType {
		l += fmt.Sprintf("  %-8s %d\n", tpType, count)
	}

	// Bandwidth
	l += fmt.Sprintf("Network Bandwidth: %s (verified, 1 day)\n", formatBytesChart(summary.TotalBandwidth))

	// Latency
	if summary.LatencyCount > 0 {
		l += fmt.Sprintf("Avg Latency:       %.1fms (across %d transports with data)\n", summary.AvgLatencyMs, summary.LatencyCount)
	} else {
		l += "Avg Latency:       no data\n"
	}

	l += "</pre>"
	l += fmt.Sprintf("<p style='color: #888; font-size: 0.8em;'>Last updated: %s</p>", summary.LastUpdated)
	return l
}

// renderTPDBandwidthHistoryChart generates a bar chart of daily total bandwidth from TPD metrics.
func renderTPDBandwidthHistoryChart(metrics []tpdTransportMetric) string {
	// Aggregate bandwidth by date
	dailyTotals := make(map[string]uint64)
	for _, m := range metrics {
		for _, d := range m.Daily {
			var bw uint64
			if d.A != nil {
				bw += d.A.Sent + d.A.Recv
			}
			if d.B != nil {
				bw += d.B.Sent + d.B.Recv
			}
			dailyTotals[d.Date] += bw
		}
	}

	if len(dailyTotals) == 0 {
		return "<p>No bandwidth data available.</p>"
	}

	// Sort dates
	var dates []string
	for d := range dailyTotals {
		dates = append(dates, d)
	}
	sort.Strings(dates)

	// Build Chart.js bar chart
	var labels, data []string
	for _, d := range dates {
		parts := strings.Split(d, "-")
		label := d
		if len(parts) == 3 {
			label = parts[1] + "-" + parts[2]
		}
		labels = append(labels, fmt.Sprintf("'%s'", label))
		data = append(data, fmt.Sprintf("%d", dailyTotals[d]))
	}

	l := "<canvas id='bw-history-chart' width='800' height='300'></canvas>\n"
	l += "<script src='https://cdn.jsdelivr.net/npm/chart.js'></script>\n"
	l += "<script>\n"
	l += "new Chart(document.getElementById('bw-history-chart'), {\n"
	l += "  type: 'bar',\n"
	l += "  data: {\n"
	l += "    labels: [" + strings.Join(labels, ",") + "],\n"
	l += "    datasets: [{\n"
	l += "      label: 'Total Bandwidth (bytes)',\n"
	l += "      data: [" + strings.Join(data, ",") + "],\n"
	l += "      backgroundColor: '#36A2EB'\n"
	l += "    }]\n"
	l += "  },\n"
	l += "  options: {\n"
	l += "    responsive: true,\n"
	l += "    plugins: { legend: { labels: { color: 'white' } } },\n"
	l += "    scales: {\n"
	l += "      x: { ticks: { color: 'white' }, grid: { color: '#333' } },\n"
	l += "      y: { ticks: { color: 'white', callback: function(v) { if(v>=1073741824) return (v/1073741824).toFixed(1)+'GB'; if(v>=1048576) return (v/1048576).toFixed(1)+'MB'; if(v>=1024) return (v/1024).toFixed(1)+'KB'; return v+'B'; } }, grid: { color: '#333' } }\n"
	l += "    }\n"
	l += "  }\n"
	l += "});\n"
	l += "</script>\n"

	// Summary table
	l += "<table style='border-collapse:collapse;margin-top:10px;'>"
	l += "<tr><th style='border:1px solid #444;padding:4px 8px;'>Date</th><th style='border:1px solid #444;padding:4px 8px;'>Bandwidth</th></tr>"
	for _, d := range dates {
		parts := strings.Split(d, "-")
		label := d
		if len(parts) == 3 {
			label = parts[1] + "-" + parts[2]
		}
		l += fmt.Sprintf("<tr><td style='border:1px solid #444;padding:4px 8px;'>%s</td><td style='border:1px solid #444;padding:4px 8px;'>%s</td></tr>", label, formatBytesChart(dailyTotals[d]))
	}
	l += "</table>"

	return l
}

// renderTPDVisorBandwidthChart generates a stacked bar chart of per-visor bandwidth from TPD metrics.
func renderTPDVisorBandwidthChart(metrics []tpdTransportMetric) string {
	// Aggregate bandwidth per visor (by edge PKs) per date
	type visorBW struct {
		pk      string
		daily   map[string]uint64
		totalBW uint64
	}

	visorMap := make(map[string]*visorBW)
	for _, m := range metrics {
		for _, edge := range m.Edges {
			if _, ok := visorMap[edge]; !ok {
				visorMap[edge] = &visorBW{pk: edge, daily: make(map[string]uint64)}
			}
		}
		for _, d := range m.Daily {
			var bw uint64
			if d.A != nil {
				bw += d.A.Sent + d.A.Recv
			}
			if d.B != nil {
				bw += d.B.Sent + d.B.Recv
			}
			// Attribute to both edges
			for _, edge := range m.Edges {
				if v, ok := visorMap[edge]; ok {
					v.daily[d.Date] += bw
					v.totalBW += bw
				}
			}
		}
	}

	if len(visorMap) == 0 {
		return "<p>No per-visor bandwidth data available.</p>"
	}

	// Sort visors by total bandwidth, take top 20
	var allVisors []*visorBW
	for _, v := range visorMap {
		allVisors = append(allVisors, v)
	}
	sort.Slice(allVisors, func(i, j int) bool {
		return allVisors[i].totalBW > allVisors[j].totalBW
	})

	maxVisors := 20
	if len(allVisors) < maxVisors {
		maxVisors = len(allVisors)
	}
	topVisors := allVisors[:maxVisors]

	// Collect all dates
	dateSet := make(map[string]struct{})
	for _, v := range topVisors {
		for d := range v.daily {
			dateSet[d] = struct{}{}
		}
	}
	var dates []string
	for d := range dateSet {
		dates = append(dates, d)
	}
	sort.Strings(dates)

	// Build labels
	var labels []string
	for _, d := range dates {
		parts := strings.Split(d, "-")
		label := d
		if len(parts) == 3 {
			label = parts[1] + "-" + parts[2]
		}
		labels = append(labels, fmt.Sprintf("'%s'", label))
	}

	// Colors for datasets
	colors := []string{
		"#FF6384", "#36A2EB", "#FFCE56", "#4BC0C0", "#9966FF",
		"#FF9F40", "#C9CBCF", "#7BC8A4", "#E7E9ED", "#FF6B6B",
		"#4ECDC4", "#45B7D1", "#96CEB4", "#FFEEAD", "#D4A5A5",
		"#9B59B6", "#3498DB", "#E74C3C", "#2ECC71", "#F39C12",
	}

	// Build datasets
	var datasets []string
	for i, v := range topVisors {
		var data []string
		for _, d := range dates {
			data = append(data, fmt.Sprintf("%d", v.daily[d]))
		}
		shortPK := v.pk
		if len(shortPK) > 12 {
			shortPK = shortPK[:8] + "..."
		}
		color := colors[i%len(colors)]
		datasets = append(datasets, fmt.Sprintf("{ label: '%s', data: [%s], backgroundColor: '%s' }",
			shortPK, strings.Join(data, ","), color))
	}

	l := "<canvas id='visor-bw-chart' width='800' height='400'></canvas>\n"
	l += "<script src='https://cdn.jsdelivr.net/npm/chart.js'></script>\n"
	l += "<script>\n"
	l += "new Chart(document.getElementById('visor-bw-chart'), {\n"
	l += "  type: 'bar',\n"
	l += "  data: {\n"
	l += "    labels: [" + strings.Join(labels, ",") + "],\n"
	l += "    datasets: [" + strings.Join(datasets, ",\n") + "]\n"
	l += "  },\n"
	l += "  options: {\n"
	l += "    responsive: true,\n"
	l += "    scales: {\n"
	l += "      x: { stacked: true, ticks: { color: 'white' }, grid: { color: '#333' } },\n"
	l += "      y: { stacked: true, ticks: { color: 'white', callback: function(v) { if(v>=1073741824) return (v/1073741824).toFixed(1)+'GB'; if(v>=1048576) return (v/1048576).toFixed(1)+'MB'; if(v>=1024) return (v/1024).toFixed(1)+'KB'; return v+'B'; } }, grid: { color: '#333' } }\n"
	l += "    },\n"
	l += "    plugins: { legend: { labels: { color: 'white', font: { size: 10 } } } }\n"
	l += "  }\n"
	l += "});\n"
	l += "</script>\n"

	return l
}
