// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/logo.go
package clirewardsserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bitfield/script"
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

	// Build HTML
	var sb strings.Builder
	sb.WriteString("<div style='display: flex; align-items: flex-start; gap: 20px; margin: 10px 0;'>\n")

	// Pie chart
	sb.WriteString(fmt.Sprintf("<div style='width: 150px; height: 150px; border-radius: 50%%; background: conic-gradient(%s); flex-shrink: 0;'></div>\n", gradient))

	// Legend
	sb.WriteString("<div style='font-size: 11px; line-height: 1.4;'>\n")
	for i, item := range displayItems {
		color := pieChartColors[i%len(pieChartColors)]
		pct := float64(item.Count) / float64(total) * 100
		label := html.EscapeString(item.Label)
		if len(label) > 30 {
			label = label[:27] + "..."
		}
		sb.WriteString(fmt.Sprintf("<div><span style='display: inline-block; width: 12px; height: 12px; background: %s; margin-right: 5px;'></span>%s (%d, %.1f%%)</div>\n", color, label, item.Count, pct))
	}
	sb.WriteString(fmt.Sprintf("<div style='margin-top: 5px; font-weight: bold;'>Total: %d</div>\n", total))
	sb.WriteString("</div>\n")

	sb.WriteString("</div>\n")
	return sb.String()
}
