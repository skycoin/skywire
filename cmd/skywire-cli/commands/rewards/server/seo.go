// Package clirewardsserver seo.go — SEO enrichment for per-date reward
// pages.
//
// Crawlers index per-page metadata better when the title + description
// carry the actual reward numbers (visor count, total SKY, country
// count) instead of templated stubs, and the schema.org Dataset
// JSON-LD block gives Google et al a machine-readable rich-snippet
// target. All inputs come from the same hist/<date>_stats.txt the
// /skycoin-rewards/hist/:date route already reads to render the page,
// so there's no extra disk hit and no risk of metadata diverging
// from rendered content.
package clirewardsserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// rewardStats is the parsed summary of a per-date _stats.txt file
// surfaced for SEO meta generation. Zero values mean the field
// couldn't be parsed — callers degrade gracefully to a generic
// description instead of asserting.
type rewardStats struct {
	Date             string  // YYYY-MM-DD (from "date:" line)
	QualifyingVisors int     // from "qualifying visors:" (first occurrence — Presence Pool)
	TotalRewardSKY   float64 // from "Total Reward Amount (both pools):" (or single-pool variant)
	UniqueIPs        int     // from "Unique IP Addresses:"
	Countries        int     // distinct country codes in the Regional Saturation table
	TotalBandwidth   string  // from "total network bandwidth:" (already human-readable)
	RewardMode       string  // from "reward mode:" (e.g. "presence + bandwidth")
}

// parseRewardStats reads hist/<date>_stats.txt and extracts the fields
// used for SEO meta + JSON-LD. Missing or malformed stats files yield
// an empty struct; the caller methods are designed to handle that.
func parseRewardStats(wd, date string) rewardStats {
	stats := rewardStats{Date: date}
	path := filepath.Join(wd, "hist", date+"_stats.txt")
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return stats
	}
	body := string(data)

	// Single-line scalar extractors. Use FIRST match for fields that
	// appear multiple times (qualifying visors appears once per pool
	// in bandwidth mode but is the same count by construction).
	// strconv errors are unreachable: the regex capture groups
	// guarantee the matched substring parses as the target type
	// (\d+ → Atoi; \d+\.\d+ → ParseFloat). errcheck-ignored.
	if m := regexp.MustCompile(`(?m)^qualifying visors:\s*(\d+)`).FindStringSubmatch(body); len(m) == 2 {
		stats.QualifyingVisors, _ = strconv.Atoi(m[1]) //nolint:errcheck
	}
	if m := regexp.MustCompile(`(?m)^Total Reward Amount.*:\s*([0-9]+\.[0-9]+)`).FindStringSubmatch(body); len(m) == 2 {
		stats.TotalRewardSKY, _ = strconv.ParseFloat(m[1], 64) //nolint:errcheck
	}
	if m := regexp.MustCompile(`(?m)^Unique IP Addresses:\s*(\d+)`).FindStringSubmatch(body); len(m) == 2 {
		stats.UniqueIPs, _ = strconv.Atoi(m[1]) //nolint:errcheck
	}
	if m := regexp.MustCompile(`(?m)^total network bandwidth:\s*(.+)$`).FindStringSubmatch(body); len(m) == 2 {
		stats.TotalBandwidth = strings.TrimSpace(m[1])
	}
	if m := regexp.MustCompile(`(?m)^reward mode:\s*(.+)$`).FindStringSubmatch(body); len(m) == 2 {
		stats.RewardMode = strings.TrimSpace(m[1])
	}

	// Country count = number of data rows in the Regional Saturation
	// table. Header is "CC    Visors   IPs     Shares  SKY/visor";
	// data rows start with a 2-letter country code followed by
	// whitespace and digits. Stop scanning at the next blank line or
	// "Total Reward Amount" line so we don't accidentally count
	// tail content.
	if i := strings.Index(body, "CC    Visors"); i >= 0 {
		tail := body[i:]
		// Drop the header line itself.
		if nl := strings.Index(tail, "\n"); nl >= 0 {
			tail = tail[nl+1:]
		}
		ccLine := regexp.MustCompile(`^[A-Z]{2}\s+\d+`)
		for _, line := range strings.Split(tail, "\n") {
			line = strings.TrimRight(line, "\r")
			if line == "" || strings.HasPrefix(line, "Total Reward Amount") {
				break
			}
			if ccLine.MatchString(line) {
				stats.Countries++
			}
		}
	}

	return stats
}

// title returns a descriptive page title for the per-date page. The
// templated <title> in htmlHeadTemplate already appends " - Skywire
// Network", so this just supplies the prefix.
func (s rewardStats) title(date string) string {
	if s.QualifyingVisors == 0 || s.TotalRewardSKY == 0 {
		return "Skycoin Rewards " + date
	}
	return fmt.Sprintf("Skycoin Rewards %s — %.2f SKY to %d visors", date, s.TotalRewardSKY, s.QualifyingVisors)
}

// description returns a rich, indexable per-page description. Crawlers
// see real numbers (visor count, total SKY, country count) instead of
// the previous generic "reward calculation details for <date>" stub.
func (s rewardStats) description(date string) string {
	// Graceful degradation: if stats parse failed, fall back to the
	// pre-PR templated stub so we don't render an obviously-empty
	// page description.
	if s.QualifyingVisors == 0 || s.TotalRewardSKY == 0 {
		return "Skycoin reward calculation details for " + date + " on the Skywire Network."
	}
	parts := []string{
		fmt.Sprintf("On %s the Skywire Network distributed %.6f SKY to %d qualifying visors", date, s.TotalRewardSKY, s.QualifyingVisors),
	}
	if s.Countries > 0 {
		parts = append(parts, fmt.Sprintf("across %d countries", s.Countries))
	}
	if s.UniqueIPs > 0 {
		parts = append(parts, fmt.Sprintf("from %d unique IPs", s.UniqueIPs))
	}
	desc := strings.Join(parts, " ") + "."
	if s.TotalBandwidth != "" {
		desc += fmt.Sprintf(" Bandwidth pool aggregate: %s.", s.TotalBandwidth)
	}
	return desc
}

// jsonLD returns the schema.org Dataset block (already wrapped in a
// <script type='application/ld+json'> tag) describing this date's
// reward distribution. Empty string when the stats file couldn't be
// parsed — the htmlHeadTemplate conditional skips emitting anything
// in that case.
func (s rewardStats) jsonLD(canonicalURL, date string) string {
	if s.QualifyingVisors == 0 || s.TotalRewardSKY == 0 || canonicalURL == "" {
		return ""
	}
	// Derive the index-page URL from the canonical so the isPartOf
	// link points at the same host the page was served from rather
	// than hard-coding theskywirenetwork.net (the configured canonical
	// could be anything via the --canonical flag).
	idx := canonicalURL
	if i := strings.Index(canonicalURL, "/skycoin-rewards/"); i >= 0 {
		idx = canonicalURL[:i+len("/skycoin-rewards")]
	}

	type propVal struct {
		Type     string      `json:"@type"`
		Name     string      `json:"name"`
		Value    interface{} `json:"value"`
		UnitText string      `json:"unitText,omitempty"`
	}
	type dataDl struct {
		Type           string `json:"@type"`
		EncodingFormat string `json:"encodingFormat"`
		Name           string `json:"name"`
		ContentURL     string `json:"contentUrl"`
	}
	type partOf struct {
		Type string `json:"@type"`
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	type creator struct {
		Type string `json:"@type"`
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	doc := map[string]interface{}{
		"@context":    "https://schema.org",
		"@type":       "Dataset",
		"name":        fmt.Sprintf("Skycoin Rewards %s", date),
		"description": s.description(date),
		"url":         canonicalURL,
		"isPartOf": partOf{
			Type: "Dataset",
			Name: "Skywire Network Daily Reward Distributions",
			URL:  idx,
		},
		"creator": creator{
			Type: "Organization",
			Name: "Skywire",
			URL:  "https://github.com/skycoin/skywire",
		},
		"dateCreated": date,
		"license":     "https://github.com/skycoin/skywire/blob/develop/LICENSE",
		"variableMeasured": []propVal{
			{Type: "PropertyValue", Name: "qualifying visors", Value: s.QualifyingVisors},
			{Type: "PropertyValue", Name: "total SKY distributed", Value: s.TotalRewardSKY, UnitText: "SKY"},
			{Type: "PropertyValue", Name: "unique IP addresses", Value: s.UniqueIPs},
			{Type: "PropertyValue", Name: "countries represented", Value: s.Countries},
		},
		"distribution": []dataDl{
			{Type: "DataDownload", EncodingFormat: "text/csv", Name: "reward transactions (broadcast input)", ContentURL: canonicalURL + "_rewardtxn0.csv"},
			{Type: "DataDownload", EncodingFormat: "text/csv", Name: "per-visor shares + reward breakdown", ContentURL: canonicalURL + "_shares.csv"},
			{Type: "DataDownload", EncodingFormat: "text/csv", Name: "ineligibility audit", ContentURL: canonicalURL + "_ineligible.csv"},
			{Type: "DataDownload", EncodingFormat: "text/plain", Name: "statistical summary", ContentURL: canonicalURL + "_stats.txt"},
		},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return ""
	}
	return `<script type='application/ld+json'>` + string(b) + `</script>`
}
