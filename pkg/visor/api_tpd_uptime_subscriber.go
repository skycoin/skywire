// Package visor pkg/visor/api_tpd_uptime_subscriber.go c3-vis-core
//
// Reader-side helper for TPD's network-wide visor-uptime feed. The
// CXO subscription lives in the unified CXOSubscriptionManager (see
// cxo_subscription_manager.go); this file is a thin facade that
// AcquireFor's the relevant tab and serves the cached blob.
//
// Pre-task-#134 this file owned its own standalone Subscriber that
// bound DmsgTPDUptimeCXOPort directly. That subscriber raced with
// the unified manager for the same DMSG port, causing one of them
// to fail with "dmsg listen on port 52: port already occupied". The
// duplication is gone now — there's a single subscriber per CXO
// port, owned by the manager.
package visor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/transport-discovery/store"
)

// ErrTPDUptimeNotReady is returned by FetchVisorUptimeCXO when the
// local manager has no snapshot for the requested day window yet
// (manager not initialized, first cycle hasn't completed, or TPD
// hasn't published that window). Callers should fall back to the
// HTTP path on this error.
var ErrTPDUptimeNotReady = errors.New("tpd uptime: cxo cache miss")

// FetchVisorUptimeCXO returns the cached visor-uptime blob for the
// given day window. (bytes, lastRootAt, nil) on a hit; (nil, zero,
// ErrTPDUptimeNotReady) when the cache has nothing for that path
// yet.
//
// `days` should be one of the values the TPD publisher writes
// (currently 1, 7, 30); other values always miss because the
// publisher doesn't write them.
func (v *Visor) FetchVisorUptimeCXO(days int) ([]byte, time.Time, error) {
	mgr := v.CXOSubMgr()
	if mgr == nil {
		return nil, time.Time{}, ErrTPDUptimeNotReady
	}
	mgr.AcquireFor(TabUptime)
	defer mgr.ReleaseFor(TabUptime)

	path := fmt.Sprintf("uptimes/days/%d", days)
	if body, ts, ok := readUptimeWindow(mgr, path); ok {
		return body, ts, nil
	}
	// Cold snapshot: AcquireFor only started the cycle. Block briefly for the
	// first sync (over dmsg) so CXO serves this call instead of the caller
	// falling back to dmsg-http — see the sd-services case in FetchCXO.
	ctx, cancel := context.WithTimeout(context.Background(), feedFirstSyncTimeout(FeedTPDUptime))
	_, _ = mgr.RefreshNow(ctx, FeedTPDUptime) //nolint:errcheck
	cancel()
	if body, ts, ok := readUptimeWindow(mgr, path); ok {
		return body, ts, nil
	}
	return nil, time.Time{}, ErrTPDUptimeNotReady
}

// uptimeCXOSubMgr is the subset of the CXO subscription manager
// readUptimeWindow needs. Satisfied by *CXOSubscriptionManager; declared as an
// interface so the reassembly logic is unit-testable without a live manager.
type uptimeCXOSubMgr interface {
	Get(feed CXOFeed, path string) ([]byte, time.Time, bool)
	Walk(feed CXOFeed, prefix string, fn func(path string, body []byte) bool) bool
}

// compactVisorSummary mirrors the publisher-side wire shape
// (pkg/transport-discovery/api/cxo_uptime_publisher.go): a per-visor uptime
// leaf whose timeline is the raw 36-byte bitmap rather than the 288-char
// string. Re-declared here to keep the dependency direction one-way; keep the
// JSON tags in sync with the publisher.
type compactVisorSummary struct {
	PK       cipher.PubKey     `json:"pk"`
	Online   bool              `json:"on"`
	Version  string            `json:"v,omitempty"`
	Daily    map[string]string `json:"d,omitempty"`
	Timeline map[string][]byte `json:"tb,omitempty"` // date -> 36-byte bitmap
}

// readUptimeWindow serves the v3 `[]store.VisorSummary` JSON for a day window
// from whichever wire form the connected TPD published (dual-parse for the
// rollout window where publishers and subscribers redeploy independently):
//
//   - Current: one gzipped `[]compactVisorSummary` leaf at exactly
//     "uptimes/days/<n>" — gunzipped and reassembled into `[]store.VisorSummary`,
//     with each day's bitmap rendered back to the 288-char string so the
//     CLI/hvui consumers see the exact HTTP /uptimes?v=v3 shape unchanged. One
//     leaf (not one-per-visor) is what lets the subscriber's Root fill complete
//     over the transient dmsg conn — see the publisher's type doc.
//   - Legacy: one per-visor leaf at "uptimes/days/<n>/<pkHex>" carrying a
//     compactVisorSummary — the previous shape, reassembled the same way. Kept
//     so a not-yet-redeployed TPD still reads (its Root rarely fills, but when
//     it does this salvages it).
//
// ok is false when neither form has any data yet.
func readUptimeWindow(mgr uptimeCXOSubMgr, path string) ([]byte, time.Time, bool) {
	if body, ts, ok := mgr.Get(FeedTPDUptime, path); ok && len(body) > 0 {
		var compact []compactVisorSummary
		if err := json.Unmarshal(cxoutils.Gunzip(body), &compact); err == nil {
			summaries := make([]store.VisorSummary, 0, len(compact))
			for i := range compact {
				summaries = append(summaries, fromCompactVisorSummary(compact[i]))
			}
			if out, ok := marshalSummaries(summaries); ok {
				return out, ts, true
			}
		}
	}
	var (
		summaries []store.VisorSummary
		firstPath string
	)
	mgr.Walk(FeedTPDUptime, path+"/", func(p string, body []byte) bool {
		var c compactVisorSummary
		if err := json.Unmarshal(body, &c); err != nil {
			return true // skip a malformed leaf; keep reassembling the rest
		}
		if firstPath == "" {
			firstPath = p
		}
		summaries = append(summaries, fromCompactVisorSummary(c))
		return true
	})
	if len(summaries) == 0 {
		return nil, time.Time{}, false
	}
	// The manager snapshot shares one lastSyncAt across the whole feed; read it
	// off any one leaf (Get on the prefix itself doesn't address a leaf).
	var newest time.Time
	if _, ts, ok := mgr.Get(FeedTPDUptime, firstPath); ok {
		newest = ts
	}
	out, ok := marshalSummaries(summaries)
	if !ok {
		return nil, time.Time{}, false
	}
	return out, newest, true
}

// marshalSummaries sorts by PK (stable output so repeated reads and cache files
// don't churn) and marshals the v3 `[]store.VisorSummary` body.
func marshalSummaries(summaries []store.VisorSummary) ([]byte, bool) {
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].PK.Hex() < summaries[j].PK.Hex() })
	out, err := json.Marshal(summaries)
	if err != nil {
		return nil, false
	}
	return out, true
}

// fromCompactVisorSummary reconstructs the full v3 store.VisorSummary from a
// compact per-visor leaf, rendering each day's 36-byte bitmap back to the
// 288-char '.'/' ' timeline string.
func fromCompactVisorSummary(c compactVisorSummary) store.VisorSummary {
	s := store.VisorSummary{
		PK:      c.PK,
		Online:  c.Online,
		Version: c.Version,
		Daily:   c.Daily,
	}
	if len(c.Timeline) > 0 {
		s.Timeline = make(map[string]string, len(c.Timeline))
		for date, bm := range c.Timeline {
			s.Timeline[date] = bitmapToTimelineString(bm)
		}
	}
	return s
}

// bitmapToTimelineString renders a 36-byte MSB-first uptime bitmap back to the
// v3 288-char timeline string ('.' = bit set, ' ' = unset) — the inverse of
// the publisher's timelineStringToBitmap.
func bitmapToTimelineString(bm []byte) string {
	out := make([]byte, 288)
	for i := range out {
		if i/8 < len(bm) && bm[i/8]&(1<<uint(7-i%8)) != 0 {
			out[i] = '.'
		} else {
			out[i] = ' '
		}
	}
	return string(out)
}
