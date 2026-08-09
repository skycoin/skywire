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
	"errors"
	"fmt"
	"time"
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
	body, ts, ok := mgr.Get(FeedTPDUptime, path)
	if !ok || len(body) == 0 {
		// Cold snapshot: AcquireFor only started the cycle. Block briefly for the
		// first sync (over dmsg) so CXO serves this call instead of the caller
		// falling back to dmsg-http — see the sd-services case in FetchCXO.
		ctx, cancel := context.WithTimeout(context.Background(), feedFirstSyncTimeout(FeedTPDUptime))
		_, _ = mgr.RefreshNow(ctx, FeedTPDUptime) //nolint:errcheck
		cancel()
		body, ts, ok = mgr.Get(FeedTPDUptime, path)
		if !ok || len(body) == 0 {
			return nil, time.Time{}, ErrTPDUptimeNotReady
		}
	}
	return body, ts, nil
}
