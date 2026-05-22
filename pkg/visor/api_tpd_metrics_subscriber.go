// Package visor pkg/visor/api_tpd_metrics_subscriber.go
//
// Reader-side helper for TPD's network-wide transport metrics feed.
// The CXO subscription lives in the unified CXOSubscriptionManager
// (see cxo_subscription_manager.go); this file is a thin facade that
// AcquireFor's the relevant tab and serves the cached blob.
//
// Pre-task-#134 this file owned its own standalone Subscriber that
// bound DmsgTPDMetricsCXOPort directly. That subscriber raced with
// the unified manager for the same DMSG port, causing one of them to
// fail with "dmsg listen on port 51: port already occupied". The
// duplication is gone now — there's a single subscriber per CXO
// port, owned by the manager.
package visor

import (
	"errors"
	"fmt"
	"time"
)

// ErrTPDMetricsNotReady is returned by FetchTransportMetricsCXO when
// the local manager has no snapshot for the requested day window yet
// (manager not initialized, first cycle hasn't completed, or TPD
// hasn't published that window). Callers should fall back to the
// HTTP path on this error.
var ErrTPDMetricsNotReady = errors.New("tpd metrics: cxo cache miss")

// FetchTransportMetricsCXO returns the cached metrics blob for the
// given day window. (bytes, lastRootAt, nil) on a hit; (nil, zero,
// ErrTPDMetricsNotReady) when the cache has nothing for that path
// yet.
//
// `days` should be one of the values the TPD publisher writes
// (currently 1, 7, 30); other values always miss because the
// publisher doesn't write them.
func (v *Visor) FetchTransportMetricsCXO(days int) ([]byte, time.Time, error) {
	mgr := v.CXOSubMgr()
	if mgr == nil {
		return nil, time.Time{}, ErrTPDMetricsNotReady
	}
	mgr.AcquireFor(TabMetrics)
	defer mgr.ReleaseFor(TabMetrics)

	path := fmt.Sprintf("metrics/days/%d", days)
	body, ts, ok := mgr.Get(FeedTPDMetrics, path)
	if !ok || len(body) == 0 {
		return nil, time.Time{}, ErrTPDMetricsNotReady
	}
	return body, ts, nil
}
