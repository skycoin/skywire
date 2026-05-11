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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
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

	case "sd-services":
		// Path is "type/<typeName>". Maps onto the SD HTTP endpoint
		// /api/services?type=<typeName>; the materialized snapshot
		// lives in the CXOSubscriptionManager's FeedSDServices and
		// is walked by servicesFromCXO (no extra subscriber needed).
		const prefix = "type/"
		if !strings.HasPrefix(args.Path, prefix) {
			return &FetchCXOResult{Reason: "invalid path for sd-services: " + args.Path}, nil
		}
		serviceType := args.Path[len(prefix):]
		if serviceType == "" {
			return &FetchCXOResult{Reason: "sd-services: missing service type"}, nil
		}
		services, ok := v.servicesFromCXO(serviceType, "", "")
		if !ok {
			return &FetchCXOResult{Reason: "sd-services: cache miss"}, nil
		}
		body, err := json.Marshal(services)
		if err != nil {
			return &FetchCXOResult{Reason: "sd-services: marshal: " + err.Error()}, nil
		}
		return &FetchCXOResult{Hit: true, Body: body, LastRootAt: time.Now()}, nil

	case "tpd-all-transports":
		// Path is "with-self" or "without-self".
		var withSelf bool
		switch args.Path {
		case "with-self":
			withSelf = true
		case "without-self":
			withSelf = false
		default:
			return &FetchCXOResult{Reason: "invalid path for tpd-all-transports: " + args.Path}, nil
		}
		body, err := v.FetchAllTransportsCXO(withSelf)
		if err != nil {
			if errors.Is(err, ErrTPDAllTransportsNotReady) {
				return &FetchCXOResult{Reason: "tpd-all-transports: cache miss"}, nil
			}
			return &FetchCXOResult{Reason: "tpd-all-transports: " + err.Error()}, nil
		}
		return &FetchCXOResult{Hit: true, Body: body, LastRootAt: time.Now()}, nil

	default:
		return &FetchCXOResult{Reason: "unknown feed: " + args.Feed}, nil
	}
}

// CXOStatus returns the per-feed snapshot/refcount/last-sync/last-err
// state of the in-memory CXO subscription manager. Used by
// `skywire cli visor cxo status` so operators can see why a feed is
// missing data (cycle never started? publisher unreachable? snapshot
// genuinely empty?).
//
// Returns an empty slice when the manager hasn't been initialized
// (e.g. visor still has no DMSG client). Errors only for genuine
// internal failures; "no feeds yet" is not an error.
func (v *Visor) CXOStatus() ([]FeedStatus, error) {
	mgr := v.CXOSubMgr()
	if mgr == nil {
		return []FeedStatus{}, nil
	}
	return mgr.Status(), nil
}

// CXORefreshFeed forces a fresh subscribe → first-Root → walk cycle
// on the named feed and blocks until it succeeds or the timeout
// expires. The returned FeedStatus reflects the snapshot *after* the
// sync attempt, so the operator can tell at a glance whether the
// publisher is reachable.
func (v *Visor) CXORefreshFeed(args CXORefreshArgs) (*FeedStatus, error) {
	mgr := v.CXOSubMgr()
	if mgr == nil {
		return nil, errors.New("CXO subscription manager not initialized (no DMSG client?)")
	}
	feed, ok := CXOFeedFromString(args.Feed)
	if !ok {
		return nil, fmt.Errorf("unknown feed %q (valid: tpd-metrics, tpd-uptime, sd-services, dmsgd-clients-by-server, tpd-all-transports)", args.Feed)
	}
	timeout := args.Timeout
	if timeout <= 0 {
		timeout = firstSyncTimeout + 5*time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	st, err := mgr.RefreshNow(ctx, feed)
	return &st, err
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
