// Package visor pkg/visor/memlimit.go c3-vis-core
package visor

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/skycoin/skywire/pkg/logging"
)

// minMemoryLimit is the lowest limit applyMemoryLimit sets: a smaller value
// is raised to it. Below it a limit only buys GC thrash — the phone core's
// live heap is 18–19 MB after a GC — and the iOS profile asks for 40 MiB, so
// the floor has to sit under that.
const minMemoryLimit = 32 * 1024 * 1024

// applyMemoryLimit sets GOMEMLIMIT based on the config value.
// Supported values:
//   - "auto": set to 60% of available system RAM (Linux and Android only:
//     it reads /proc/meminfo, so on macOS and iOS it sets nothing and warns)
//   - "256MiB", "512MiB", "1GiB", etc.: explicit limit, at least 32 MiB
//   - "": no limit (default)
func applyMemoryLimit(log *logging.Logger, limit string) {
	if limit == "" {
		return
	}
	bytes, err := resolveMemoryLimit(limit, availableMemoryBytes)
	switch {
	case errors.Is(err, errNoMemInfo):
		log.Warn(`memory_limit "auto" could not read the available memory (it reads /proc/meminfo, which macOS and iOS do not have), so GOMEMLIMIT is not set: set memory_limit to an explicit size instead, such as "512MiB"`)
		return
	case err != nil:
		log.WithError(err).Warnf("Invalid memory_limit %q, skipping", limit)
		return
	}
	prev := debug.SetMemoryLimit(bytes)
	was := "none"
	if prev != math.MaxInt64 {
		was = formatBytes(prev)
	}
	log.Infof("GOMEMLIMIT set to %s (was %s)", formatBytes(bytes), was)
}

// errNoMemInfo is resolveMemoryLimit's answer to "auto" on a system whose
// memory size it cannot read.
var errNoMemInfo = errors.New("available memory unknown")

// resolveMemoryLimit turns a non-empty memory_limit value into the limit to
// set, raised to minMemoryLimit. avail reports the available RAM for "auto"
// (0 when unknown).
func resolveMemoryLimit(limit string, avail func() int64) (int64, error) {
	var bytes int64
	if limit == "auto" {
		a := avail()
		if a <= 0 {
			return 0, errNoMemInfo
		}
		bytes = int64(float64(a) * 0.6)
	} else {
		var err error
		if bytes, err = parseMemorySize(limit); err != nil {
			return 0, err
		}
	}
	return max(bytes, minMemoryLimit), nil
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
