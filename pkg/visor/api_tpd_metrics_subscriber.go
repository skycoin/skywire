// Package visor pkg/visor/api_tpd_metrics_subscriber.go c3-vis-core
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
	"bytes"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
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
	if body, ts, ok := mgr.Get(FeedTPDMetrics, path); ok && len(body) > 0 {
		// Gunzip passes a raw body through unchanged, so this reads both the
		// gzipped bodies the publisher writes now and any uncompressed ones
		// still cached from an older TPD.
		return cxoutils.Gunzip(body), ts, nil
	}

	// A window too large for one CXO object is published as
	// "metrics/days/<n>/part/<NNNN>" leaves. Stitch them back into the single
	// JSON array every caller of this function expects.
	return v.joinTransportMetricParts(path)
}

// joinTransportMetricParts concatenates the part leaves under base into one
// JSON array. Parts are spliced as bytes rather than decoded and re-encoded:
// these windows run to tens of megabytes, and the caller only forwards the
// array on.
func (v *Visor) joinTransportMetricParts(base string) ([]byte, time.Time, error) {
	mgr := v.CXOSubMgr()
	if mgr == nil {
		return nil, time.Time{}, ErrTPDMetricsNotReady
	}

	type part struct {
		path string
		body []byte
	}
	var parts []part
	var newest time.Time

	mgr.Walk(FeedTPDMetrics, base+"/part/", func(p string, body []byte) bool {
		if len(body) == 0 {
			return true
		}
		// Walk lends the snapshot's bytes; Gunzip of a raw body returns that
		// same slice, so copy before the callback returns.
		decoded := cxoutils.Gunzip(body)
		parts = append(parts, part{path: p, body: append([]byte(nil), decoded...)})
		if ts, ok := mgr.SyncedAt(FeedTPDMetrics, p); ok && ts.After(newest) {
			newest = ts
		}
		return true
	})
	if len(parts) == 0 {
		return nil, time.Time{}, ErrTPDMetricsNotReady
	}
	// Walk iterates a map, so order is arbitrary; the zero-padded part index
	// makes a lexical sort the publication order.
	sort.Slice(parts, func(i, j int) bool { return parts[i].path < parts[j].path })

	bodies := make([][]byte, len(parts))
	for i, p := range parts {
		bodies[i] = p.body
	}
	joined, err := spliceJSONArrays(bodies)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("%w: %v", ErrTPDMetricsNotReady, err)
	}
	return joined, newest, nil
}

// spliceJSONArrays concatenates JSON arrays into one array at the byte level.
// Splicing a body that is not an array would produce silently malformed JSON,
// so that is an error rather than something the caller discovers downstream.
func spliceJSONArrays(bodies [][]byte) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('[')
	first := true
	for i, body := range bodies {
		inner := bytes.TrimSpace(body)
		if len(inner) < 2 || inner[0] != '[' || inner[len(inner)-1] != ']' {
			return nil, fmt.Errorf("part %d is not a JSON array", i)
		}
		if inner = bytes.TrimSpace(inner[1 : len(inner)-1]); len(inner) == 0 {
			continue
		}
		if !first {
			buf.WriteByte(',')
		}
		buf.Write(inner)
		first = false
	}
	buf.WriteByte(']')
	return buf.Bytes(), nil
}
