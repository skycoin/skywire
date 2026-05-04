// Package visor pkg/visor/api_fetch_cxo.go
//
// FetchCXO routes a (feed, path) pair to the matching lazy-on-demand
// CXO subscriber and returns the cached payload — or a miss reason
// the CLI's fetch chain can decide what to do with.
//
// Adding a new feed: register a new (subscriber struct, ensure
// helper, Fetch helper) under pkg/visor/api_*_subscriber.go using
// the same lazy-on-demand pattern, then add a case to the switch
// below. The CLI side learns about it through its URL→feed mapping
// table; nothing in the RPC/marshal layer needs to change.
package visor

import (
	"errors"
)

// FetchCXO implements API.
func (v *Visor) FetchCXO(args FetchCXOArgs) (*FetchCXOResult, error) {
	switch args.Feed {
	case "tpd-metrics":
		// Path is "metrics/days/<n>" — the subscriber-side helper
		// takes the int directly so the CLI can keep its mapping
		// table feed-agnostic.
		days := daysFromPath(args.Path, "metrics/days/")
		if days <= 0 {
			return &FetchCXOResult{Reason: "invalid path for tpd-metrics: " + args.Path}, nil
		}
		body, ts, err := v.FetchTransportMetricsCXO(days)
		if err != nil {
			if errors.Is(err, ErrTPDMetricsNotReady) {
				return &FetchCXOResult{Reason: "tpd-metrics: cache miss"}, nil
			}
			return &FetchCXOResult{Reason: "tpd-metrics: " + err.Error()}, nil
		}
		return &FetchCXOResult{Hit: true, Body: body, LastRootAt: ts}, nil

	case "tpd-uptime":
		days := daysFromPath(args.Path, "uptimes/days/")
		if days <= 0 {
			return &FetchCXOResult{Reason: "invalid path for tpd-uptime: " + args.Path}, nil
		}
		body, ts, err := v.FetchVisorUptimeCXO(days)
		if err != nil {
			if errors.Is(err, ErrTPDUptimeNotReady) {
				return &FetchCXOResult{Reason: "tpd-uptime: cache miss"}, nil
			}
			return &FetchCXOResult{Reason: "tpd-uptime: " + err.Error()}, nil
		}
		return &FetchCXOResult{Hit: true, Body: body, LastRootAt: ts}, nil

	default:
		return &FetchCXOResult{Reason: "unknown feed: " + args.Feed}, nil
	}
}

// daysFromPath extracts the trailing integer from a "<prefix>N"
// path. Returns 0 on any parse miss — the FetchCXO switch treats
// that as an invalid path and reports back to the caller.
func daysFromPath(path, prefix string) int {
	if len(path) <= len(prefix) || path[:len(prefix)] != prefix {
		return 0
	}
	rest := path[len(prefix):]
	n := 0
	for _, c := range rest {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
