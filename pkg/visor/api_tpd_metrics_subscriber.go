// Package visor pkg/visor/api_tpd_metrics_subscriber.go
//
// Visor-side CXO subscriber for TPD's network-wide transport metrics
// feed (the publisher lives in pkg/transport-discovery/api/cxo_metrics_publisher.go).
//
// The subscriber is lazily-created the first time the hvui asks for
// network/transports stats. Once running it stays connected and
// receives Root updates from TPD on the publisher's recompute
// cadence (~60s). The hvui handler reads the cached value via
// FetchTransportMetricsCXO; on a hit the response is served
// instantly with no DMSG round-trip, so a Transports tab open is
// effectively a redis-paid read on the publisher's tick rather than
// a per-tab full-payload HTTP fetch.
//
// When the publisher hasn't pushed anything yet (e.g. deployment
// hasn't been updated, or the subscriber just connected and the
// first OnUpdate callback hasn't fired) the cache lookup returns
// (nil, time.Time{}, ErrTPDMetricsNotReady) and the hvui handler
// falls through to the existing HTTP path.
package visor

import (
	"errors"
	"fmt"
	"time"

	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// ErrTPDMetricsNotReady is returned by FetchTransportMetricsCXO when
// the local subscriber hasn't received a payload for the requested
// day window yet (subscriber not yet running, hasn't received any
// Root, or TPD hasn't published this window). Callers should fall
// back to the HTTP path on this error.
var ErrTPDMetricsNotReady = errors.New("tpd metrics: cxo cache miss")

// tpdMetricsSubscriber is the long-lived state for the TPD-metrics
// subscriber. Created lazily on the first FetchTransportMetricsCXO
// call (via initTPDMetricsSubscriber) and kept alive for the
// visor's lifetime.
type tpdMetricsSubscriber struct {
	sub        *treestore.Subscriber
	lastRootAt time.Time
}

// FetchTransportMetricsCXO returns the cached metrics blob for the
// given day window from the TPD CXO subscriber. (bytes, lastRootAt,
// nil) on a hit; (nil, zero, ErrTPDMetricsNotReady) when the cache
// has nothing for that path yet.
//
// `days` should be one of the values the TPD publisher writes
// (currently 1, 7, 30); other values always miss because the
// publisher doesn't write them.
func (v *Visor) FetchTransportMetricsCXO(days int) ([]byte, time.Time, error) {
	state, err := v.ensureTPDMetricsSubscriber()
	if err != nil {
		return nil, time.Time{}, err
	}
	if state == nil || state.sub == nil {
		return nil, time.Time{}, ErrTPDMetricsNotReady
	}
	path := fmt.Sprintf("metrics/days/%d", days)
	body, ok := state.sub.Get(path)
	if !ok || len(body) == 0 {
		return nil, time.Time{}, ErrTPDMetricsNotReady
	}
	v.tpdMetricsSubMu.RLock()
	ts := state.lastRootAt
	v.tpdMetricsSubMu.RUnlock()
	return body, ts, nil
}

// ensureTPDMetricsSubscriber lazily constructs the subscriber on
// first use. Returns nil + nil when the visor has no DMSG client or
// no parseable TPD CXO peer (the caller treats both as a cache miss
// and falls through to HTTP).
func (v *Visor) ensureTPDMetricsSubscriber() (*tpdMetricsSubscriber, error) {
	v.tpdMetricsSubMu.RLock()
	state := v.tpdMetricsSub
	v.tpdMetricsSubMu.RUnlock()
	if state != nil {
		return state, nil
	}

	v.tpdMetricsSubMu.Lock()
	defer v.tpdMetricsSubMu.Unlock()
	// Double-check under the write lock.
	if v.tpdMetricsSub != nil {
		return v.tpdMetricsSub, nil
	}
	if v.dmsgC == nil {
		return nil, nil //nolint:nilnil // intentional: caller treats nil state as "no subscriber"
	}
	tpdPK, ok := tpdCXOPeer(v)
	if !ok {
		return nil, nil //nolint:nilnil
	}

	sub, err := treestore.NewSubscriber(v.dmsgC, tpdPK, treestore.SubConfig{
		InMemoryDB: true,
		DmsgPort:   skyenv.DmsgTPDMetricsCXOPort,
	})
	if err != nil {
		return nil, fmt.Errorf("create tpd metrics subscriber: %w", err)
	}

	state = &tpdMetricsSubscriber{sub: sub}
	sub.SetPrefixes([]string{"metrics/days/"})
	sub.OnUpdate(func(_ []treestore.UpdateEvent) {
		v.tpdMetricsSubMu.Lock()
		if v.tpdMetricsSub != nil {
			v.tpdMetricsSub.lastRootAt = time.Now()
		}
		v.tpdMetricsSubMu.Unlock()
	})
	if err := sub.Connect(tpdPK); err != nil {
		_ = sub.Close() //nolint:errcheck
		return nil, fmt.Errorf("dial tpd metrics publisher: %w", err)
	}
	v.tpdMetricsSub = state
	return state, nil
}

// closeTPDMetricsSubscriber tears down the subscriber on visor close.
// Safe to call multiple times.
func (v *Visor) closeTPDMetricsSubscriber() {
	v.tpdMetricsSubMu.Lock()
	state := v.tpdMetricsSub
	v.tpdMetricsSub = nil
	v.tpdMetricsSubMu.Unlock()
	if state != nil && state.sub != nil {
		_ = state.sub.Close() //nolint:errcheck
	}
}
