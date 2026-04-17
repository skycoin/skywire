// Package uptimestats pkg/uptimestats/filter.go
//
// Filter / stats helpers shared by the CLI consumers. Kept here rather
// than in each per-service CLI package so the hypervisor RPC layer can
// apply the same filter + counting semantics when it eventually exposes
// this data to the UI.
package uptimestats

import (
	"sort"
	"strconv"
	"strings"
)

// VisorFilter is a declarative filter applied after fetch. Zero value
// matches every entry.
type VisorFilter struct {
	OnlineOnly    bool   // drop entries where Online == false
	PKSubstr      string // PK must contain this substring (useful for CLI grep-style matching)
	VersionEquals string // version must equal exactly (after trimming "v" prefix + anything after a space)
	MinVersion    string // version must be >= this semver (same trimming)
}

// Filter returns a new slice containing only entries that match f.
// Original slice is untouched.
func Filter(entries []VisorSummary, f VisorFilter) []VisorSummary {
	out := make([]VisorSummary, 0, len(entries))
	for _, e := range entries {
		if f.OnlineOnly && !e.Online {
			continue
		}
		if f.PKSubstr != "" && !strings.Contains(e.PK.Hex(), f.PKSubstr) {
			continue
		}
		if f.VersionEquals != "" && normalizeVersion(e.Version) != normalizeVersion(f.VersionEquals) {
			continue
		}
		if f.MinVersion != "" && !versionGTE(e.Version, f.MinVersion) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// VersionCounts returns a sorted-by-count (desc) slice of
// (version, count) pairs for the given entries. Useful for the
// `--stats` / `--list-versions` style CLI output.
type VersionCount struct {
	Version string
	Count   int
}

// VersionCounts tallies the Version field across entries and returns
// the counts sorted by count desc, then by version asc for stability.
func VersionCounts(entries []VisorSummary) []VersionCount {
	counts := make(map[string]int)
	for _, e := range entries {
		counts[e.Version]++
	}
	out := make([]VersionCount, 0, len(counts))
	for v, c := range counts {
		out = append(out, VersionCount{Version: v, Count: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Version < out[j].Version
	})
	return out
}

// MinDailyUptime returns entries whose minimum daily uptime
// percentage (across all recorded days in Daily) is >= threshold.
// Entries with no Daily data are excluded. Threshold is 0..100.
func MinDailyUptime(entries []VisorSummary, threshold int) []VisorSummary {
	out := make([]VisorSummary, 0, len(entries))
	for _, e := range entries {
		if len(e.Daily) == 0 {
			continue
		}
		keep := true
		for _, pctStr := range e.Daily {
			pct, err := strconv.ParseFloat(pctStr, 64)
			if err != nil {
				keep = false
				break
			}
			if int(pct) < threshold {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, e)
		}
	}
	return out
}

// --- version comparison helpers (duplicated from cliut to keep
// this package free of CLI-layer dependencies) ---

func normalizeVersion(v string) string {
	v = strings.TrimPrefix(v, "v")
	if idx := strings.Index(v, " "); idx != -1 {
		v = v[:idx]
	}
	return v
}

func parseSemver(v string) (major, minor, patch int, ok bool) {
	v = normalizeVersion(v)
	if v == "" {
		return 0, 0, 0, false
	}
	parts := strings.Split(v, ".")
	if len(parts) < 3 {
		return 0, 0, 0, false
	}
	var err error
	if major, err = strconv.Atoi(parts[0]); err != nil {
		return 0, 0, 0, false
	}
	if minor, err = strconv.Atoi(parts[1]); err != nil {
		return 0, 0, 0, false
	}
	// Patch may have a suffix like "-0" or "-dirty" — take the
	// leading digits only.
	for i, r := range parts[2] {
		if r < '0' || r > '9' {
			parts[2] = parts[2][:i]
			break
		}
	}
	if patch, err = strconv.Atoi(parts[2]); err != nil {
		return 0, 0, 0, false
	}
	return major, minor, patch, true
}

func versionGTE(a, b string) bool {
	aMaj, aMin, aPatch, aOK := parseSemver(a)
	bMaj, bMin, bPatch, bOK := parseSemver(b)
	if !aOK || !bOK {
		return false
	}
	if aMaj != bMaj {
		return aMaj > bMaj
	}
	if aMin != bMin {
		return aMin > bMin
	}
	return aPatch >= bPatch
}
