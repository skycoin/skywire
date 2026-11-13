// Package ui2 pkg/visor/ui2/services.go c3-vis-core
package ui2

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// The deployment services page, as services-health.component.ts.

// Services is the health of the deployment services this visor uses.
type Services struct {
	Entries []visorapi.ServiceHealthEntry
	Err     string
	Checked string
}

// AnyDown reports a service whose status is not OK.
func (s Services) AnyDown() bool {
	for _, e := range s.Entries {
		if !strings.EqualFold(e.Status, "OK") {
			return true
		}
	}
	return false
}

// SvcKey is the svc-fetch key of a service name, or "" for one that cannot
// be fetched.
func SvcKey(name string) string {
	switch strings.ToLower(name) {
	case "transport discovery":
		return "tpd"
	case "dmsg discovery":
		return "dmsgd"
	case "address resolver":
		return "ar"
	case "route finder":
		return "rf"
	case "service discovery":
		return "sd"
	case "uptime tracker":
		return "ut"
	}
	return ""
}

var healthSuffix = regexp.MustCompile(`(?i)/health$`)

// EndpointURL is the service's /health URL.
func EndpointURL(u string) string {
	s := strings.TrimRight(strings.TrimSpace(u), "/")
	if s == "" || healthSuffix.MatchString(s) {
		return s
	}
	return s + "/health"
}

func statusDot(status string) string {
	switch strings.ToUpper(status) {
	case "OK":
		return "on"
	case "DOWN":
		return "off"
	}
	return "warn"
}

func latencyTone(ms int64) string {
	switch {
	case ms < 500:
		return "ok"
	case ms < 2000:
		return "warn-text"
	}
	return "error"
}

// RSNRow is one route setup node's remote /stats.
type RSNRow struct {
	PK             string
	Err            string
	StatsAvailable bool
	Rate           float64
	Successful     uint64
	Failed         uint64
	Active         int
	P50            int64
	P95            int64
	P99            int64
	LastSuccess    string
	LastFailure    string
	Failures       []RSNFailure
}

// RSNFailure is a failure reason and how often it happened.
type RSNFailure struct {
	Reason string
	Count  uint64
}

// TopFailures keeps the three most frequent reasons.
func TopFailures(m map[string]uint64) []RSNFailure {
	var out []RSNFailure
	for r, n := range m {
		if n > 0 {
			out = append(out, RSNFailure{r, n})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Reason < out[j].Reason
	})
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

func rateTone(r float64) string {
	switch {
	case r >= 90:
		return "ok"
	case r >= 70:
		return "warn-text"
	}
	return "error"
}

func pct(r float64) string { return strconv.FormatFloat(r, 'f', 1, 64) + "%" }

func u64(n uint64) string { return strconv.FormatUint(n, 10) }

func i64(n int64) string { return strconv.FormatInt(n, 10) }
