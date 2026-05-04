// Package visor pkg/visor/api_local_uptime_stats.go
//
// Local tier-uptime read API. The visor's stats tracker
// (pkg/visor/stats) keeps a 288-slot-per-day bitmap per tier
// (process / dmsg / skynet) reflecting which 5-minute slots the
// visor was observed online. This file exposes that data through
// the Visor RPC surface so the hvui's per-visor and network-wide
// Uptime tabs can render it through the same hypervisor proxy
// chain everything else in the hvui uses — no per-visor logserver
// HTTP client.
//
// The wire shape mirrors `GET /stats/uptime` on the visor's
// logserver (a {tier: {date: 288-ascii}} map): the renderer just
// reads '.' as online, ' ' as offline.
package visor

import (
	"errors"
	"time"

	"github.com/skycoin/skywire/pkg/visor/stats"
)

// LocalUptimeResponse is the wire shape for LocalUptimeStats.
//
// The tier name is whatever the tracker recorded (the visor uses
// "process", "dmsg", "skynet"; future probes may add more — keep
// the renderer name-agnostic). Dates are UTC YYYY-MM-DD; bitmaps
// are 288 chars where '.' = online slot and ' ' = offline slot.
type LocalUptimeResponse struct {
	Tiers     map[string]map[string]string `json:"tiers"`
	Since     time.Time                    `json:"since"`
	Until     time.Time                    `json:"until"`
	FetchedAt time.Time                    `json:"fetched_at"`
}

// LocalUptimeArgs is the request shape. Empty Since/Until mean
// "default 7-day window ending now" — caller-side defaults match
// the logserver's own /stats/uptime behavior so a hypervisor and a
// direct curl agree on the rendered timeline.
type LocalUptimeArgs struct {
	Since time.Time `json:"since,omitempty"`
	Until time.Time `json:"until,omitempty"`
}

// localUptimeDefaultWindow is the implicit `since` when the caller
// hasn't passed one. Same value the logserver uses for /stats/*
// (defaultHistoryWindow) — kept in sync intentionally.
const localUptimeDefaultWindow = 7 * 24 * time.Hour

// LocalUptimeStats returns the visor's tier-uptime bitmaps over the
// requested window. Returns an empty Tiers map (no error) when the
// stats subsystem isn't initialized so the hvui surfaces "no data"
// rather than an error.
func (v *Visor) LocalUptimeStats(args LocalUptimeArgs) (*LocalUptimeResponse, error) {
	now := time.Now().UTC()
	until := args.Until
	if until.IsZero() {
		until = now
	} else {
		until = until.UTC()
	}
	since := args.Since
	if since.IsZero() {
		since = until.Add(-localUptimeDefaultWindow)
	} else {
		since = since.UTC()
	}
	if since.After(until) {
		return nil, errors.New("since must be <= until")
	}

	resp := &LocalUptimeResponse{
		Tiers:     map[string]map[string]string{},
		Since:     since,
		Until:     until,
		FetchedAt: now,
	}

	if v.statsTracker == nil {
		return resp, nil
	}
	store := v.statsTracker.Store()
	if store == nil {
		return resp, nil
	}

	tiers, err := store.TierNames()
	if err != nil {
		return nil, err
	}

	sinceKey := since.Format("2006-01-02")
	untilKey := until.Format("2006-01-02")

	for _, tier := range tiers {
		dates, err := store.TierDates(tier)
		if err != nil {
			continue
		}
		view := make(map[string]string)
		for _, date := range dates {
			if date < sinceKey || date > untilKey {
				continue
			}
			d, err := time.Parse("2006-01-02", date)
			if err != nil {
				continue
			}
			bm, err := store.TierBitmap(tier, d)
			if err != nil {
				continue
			}
			view[date] = stats.Render(bm)
		}
		if len(view) > 0 {
			resp.Tiers[tier] = view
		}
	}
	return resp, nil
}
