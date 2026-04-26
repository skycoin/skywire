// Package visor pkg/visor/memlimit.go
package visor

import (
	"bufio"
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/skycoin/skywire/pkg/logging"
)

// applyMemoryLimit sets GOMEMLIMIT based on the config value.
// Supported values:
//   - "auto": set to 60% of available system RAM
//   - "256MiB", "512MiB", "1GiB", etc.: explicit limit
//   - "": no limit (default)
func applyMemoryLimit(log *logging.Logger, limit string) {
	if limit == "" {
		return
	}

	var bytes int64
	if limit == "auto" {
		avail := availableMemoryBytes()
		if avail <= 0 {
			log.Warn("Could not detect available memory, skipping GOMEMLIMIT")
			return
		}
		bytes = int64(float64(avail) * 0.6)
	} else {
		var err error
		bytes, err = parseMemorySize(limit)
		if err != nil {
			log.WithError(err).Warnf("Invalid memory_limit %q, skipping", limit)
			return
		}
	}

	if bytes < 64*1024*1024 { // minimum 64 MiB
		bytes = 64 * 1024 * 1024
	}

	prev := debug.SetMemoryLimit(bytes)
	log.Infof("GOMEMLIMIT set to %s (was %s)", formatBytes(bytes), formatBytes(prev))
}

// availableMemoryBytes reads total available RAM from /proc/meminfo.
func availableMemoryBytes() int64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close() //nolint:errcheck

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		// Prefer MemAvailable (accounts for caches/buffers)
		if strings.HasPrefix(line, "MemAvailable:") {
			return parseMemInfoLine(line)
		}
	}

	// Fall back to MemTotal if MemAvailable not present
	f.Seek(0, 0) //nolint:errcheck,gosec
	scanner = bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			return parseMemInfoLine(line)
		}
	}
	return 0
}

// parseMemInfoLine parses a /proc/meminfo line like "MemAvailable:  1234567 kB"
func parseMemInfoLine(line string) int64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	kb, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return kb * 1024 // kB to bytes
}

// parseMemorySize parses a human-readable memory size like "256MiB" or "1GiB".
func parseMemorySize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty memory size")
	}

	var multiplier int64 = 1
	upper := strings.ToUpper(s)
	if strings.HasSuffix(upper, "GIB") {
		multiplier = 1024 * 1024 * 1024
		s = s[:len(s)-3]
	} else if strings.HasSuffix(upper, "MIB") {
		multiplier = 1024 * 1024
		s = s[:len(s)-3]
	} else if strings.HasSuffix(upper, "KIB") {
		multiplier = 1024
		s = s[:len(s)-3]
	} else if strings.HasSuffix(upper, "GB") {
		multiplier = 1000 * 1000 * 1000
		s = s[:len(s)-2]
	} else if strings.HasSuffix(upper, "MB") {
		multiplier = 1000 * 1000
		s = s[:len(s)-2]
	} else if strings.HasSuffix(upper, "B") {
		s = s[:len(s)-1]
	}

	val, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number: %w", err)
	}

	return int64(val * float64(multiplier)), nil
}

func formatBytes(b int64) string {
	switch {
	case b >= 1024*1024*1024:
		return fmt.Sprintf("%.1fGiB", float64(b)/(1024*1024*1024))
	case b >= 1024*1024:
		return fmt.Sprintf("%.0fMiB", float64(b)/(1024*1024))
	case b >= 1024:
		return fmt.Sprintf("%.0fKiB", float64(b)/1024)
	default:
		return fmt.Sprintf("%dB", b)
	}
}
