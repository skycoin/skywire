// Package visor pkg/visor/memlimit.go c3-vis-core
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
//   - "auto": set to 90% of the total system RAM (or cgroup limit)
//   - "256MiB", "512MiB", "1GiB", etc.: explicit limit
//   - "": no limit (default)
//
// A GOMEMLIMIT in the environment wins, so a service unit can size the
// visor without editing its config.
func applyMemoryLimit(log *logging.Logger, limit string) {
	if limit == "" {
		return
	}
	if env := os.Getenv("GOMEMLIMIT"); env != "" {
		log.Infof("GOMEMLIMIT=%s from the environment, ignoring memory_limit %q", env, limit)
		return
	}

	var bytes int64
	if limit == "auto" {
		total := totalMemoryBytes()
		if total <= 0 {
			log.Warn("Could not detect total memory, skipping GOMEMLIMIT")
			return
		}
		bytes = autoMemoryLimit(total)
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

// autoMemoryLimit returns the "auto" limit for a machine with total bytes of
// memory. It is a safety net near the total, not a working budget, because a
// lower limit only turns memory into back to back GC cycles.
func autoMemoryLimit(total int64) int64 {
	return total / 10 * 9
}

// totalMemoryBytes returns the memory of the machine, or of its cgroup when
// that is smaller.
func totalMemoryBytes() int64 {
	total := int64(0)
	if f, err := os.Open("/proc/meminfo"); err == nil {
		defer f.Close() //nolint:errcheck
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			if line := scanner.Text(); strings.HasPrefix(line, "MemTotal:") {
				total = parseMemInfoLine(line)
				break
			}
		}
	}
	for _, p := range []string{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"} {
		b, err := os.ReadFile(p) //nolint:gosec
		if err != nil {
			continue
		}
		if v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil && v > 0 && (total == 0 || v < total) {
			total = v
		}
	}
	return total
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
