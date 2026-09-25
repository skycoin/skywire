// Package skymail pkg/skymail/units.go c4-app-mail
package skymail

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

var sizeUnits = []struct {
	suffix string
	mult   int64
}{
	{"gib", 1 << 30}, {"mib", 1 << 20}, {"kib", 1 << 10},
	{"gb", 1e9}, {"mb", 1e6}, {"kb", 1e3},
	{"g", 1 << 30}, {"m", 1 << 20}, {"k", 1 << 10}, {"b", 1},
}

// ParseSize reads a limit written "16MiB", "500KB" or "1048576", or
// "default" (0) or "none" (-1), the values Limits takes.
func ParseSize(s string) (int64, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "default":
		return 0, nil
	case "none", "unlimited":
		return -1, nil
	}
	mult := int64(1)
	for _, u := range sizeUnits {
		if strings.HasSuffix(s, u.suffix) {
			s, mult = strings.TrimSpace(strings.TrimSuffix(s, u.suffix)), u.mult
			break
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return 0, fmt.Errorf("%q is not a size", s)
	}
	return int64(f * float64(mult)), nil
}

// ParseAge reads a Go duration, also taking days ("7d"), "default" (0)
// or "none" (-1).
func ParseAge(s string) (time.Duration, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "default":
		return 0, nil
	case "none", "forever":
		return -1, nil
	}
	if d, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.ParseFloat(d, 64)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("%q is not a duration", s)
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%q is not a duration", s)
	}
	return d, nil
}

// FormatSize writes a size the way ParseSize reads it.
func FormatSize(n int64) string {
	switch {
	case n < 0:
		return "none"
	case n >= 1<<30 && n%(1<<30) == 0:
		return strconv.FormatInt(n>>30, 10) + "GiB"
	case n >= 1<<20 && n%(1<<20) == 0:
		return strconv.FormatInt(n>>20, 10) + "MiB"
	case n >= 1<<20:
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + "MiB"
	case n >= 1<<10:
		return strconv.FormatFloat(float64(n)/(1<<10), 'f', 1, 64) + "KiB"
	}
	return strconv.FormatInt(n, 10) + "B"
}

// FormatAge writes a duration the way ParseAge reads it.
func FormatAge(d time.Duration) string {
	switch {
	case d < 0:
		return "none"
	case d%(24*time.Hour) == 0:
		return strconv.FormatInt(int64(d/(24*time.Hour)), 10) + "d"
	}
	return d.String()
}
