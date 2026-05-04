// Package visor pkg/visor/api_tpd_uptime_subscriber.go
//
// Visor-side CXO subscriber for TPD's network-wide visor-uptime
// feed (the publisher lives in
// pkg/transport-discovery/api/cxo_uptime_publisher.go and serves on
// skyenv.DmsgTPDUptimeCXOPort).
//
// Lazily-created the first time the hvui Network Uptime tab asks
// for fleet uptime; once running it stays connected and receives
// Root updates on the publisher's recompute cadence (~60s). The
// hvui handler reads via FetchVisorUptimeCXO; on a cache miss the
// handler falls back to DMSG-HTTP and finally HTTP, so the tab
// works on every deployment regardless of whether the publisher is
// available.
//
// "Run on demand" semantics: nothing dials TPD until a hypervisor
// actually asks for fleet uptime data. After the first dial the
// subscriber sticks around so subsequent fetches are a local
// memory read; the alternative — a fresh dial-and-tear-down per
// hvui open — would burn a DMSG handshake every minute.
package visor

import (
	"errors"
	"fmt"
	"time"

	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// ErrTPDUptimeNotReady is returned by FetchVisorUptimeCXO when the
// local subscriber hasn't received a payload for the requested day
// window yet (subscriber not yet running, hasn't received any Root,
// or TPD hasn't published this window). Callers should fall back
// to the HTTP path on this error.
var ErrTPDUptimeNotReady = errors.New("tpd uptime: cxo cache miss")

// tpdUptimeSubscriber is the long-lived state for the TPD-uptime
// subscriber. Created lazily on the first FetchVisorUptimeCXO call
// (via ensureTPDUptimeSubscriber) and kept alive for the visor's
// lifetime.
type tpdUptimeSubscriber struct {
	sub        *treestore.Subscriber
	lastRootAt time.Time
}

// FetchVisorUptimeCXO returns the cached visor-uptime blob for the
// given day window from the TPD CXO subscriber. (bytes, lastRootAt,
// nil) on a hit; (nil, zero, ErrTPDUptimeNotReady) when the cache
// has nothing for that path yet.
//
// `days` should be one of the values the TPD publisher writes
// (currently 1, 7, 30); other values always miss because the
// publisher doesn't write them.
func (v *Visor) FetchVisorUptimeCXO(days int) ([]byte, time.Time, error) {
	state, err := v.ensureTPDUptimeSubscriber()
	if err != nil {
		return nil, time.Time{}, err
	}
	if state == nil || state.sub == nil {
		return nil, time.Time{}, ErrTPDUptimeNotReady
	}
	path := fmt.Sprintf("uptimes/days/%d", days)
	body, ok := state.sub.Get(path)
	if !ok || len(body) == 0 {
		return nil, time.Time{}, ErrTPDUptimeNotReady
	}
	v.tpdUptimeSubMu.RLock()
	ts := state.lastRootAt
	v.tpdUptimeSubMu.RUnlock()
	return body, ts, nil
}

// ensureTPDUptimeSubscriber lazily constructs the subscriber on
// first use. Returns nil + nil when the visor has no DMSG client or
// no parseable TPD CXO peer (the caller treats both as a cache miss
// and falls through to HTTP).
func (v *Visor) ensureTPDUptimeSubscriber() (*tpdUptimeSubscriber, error) {
	v.tpdUptimeSubMu.RLock()
	state := v.tpdUptimeSub
	v.tpdUptimeSubMu.RUnlock()
	if state != nil {
		return state, nil
	}

	v.tpdUptimeSubMu.Lock()
	defer v.tpdUptimeSubMu.Unlock()
	if v.tpdUptimeSub != nil {
		return v.tpdUptimeSub, nil
	}
	if v.dmsgC == nil {
		return nil, nil //nolint:nilnil // intentional sentinel for caller
	}
	tpdPK, ok := tpdCXOPeer(v)
	if !ok {
		return nil, nil //nolint:nilnil
	}

	sub, err := treestore.NewSubscriber(v.dmsgC, tpdPK, treestore.SubConfig{
		InMemoryDB: true,
		DmsgPort:   skyenv.DmsgTPDUptimeCXOPort,
	})
	if err != nil {
		return nil, fmt.Errorf("create tpd uptime subscriber: %w", err)
	}

	state = &tpdUptimeSubscriber{sub: sub}
	sub.SetPrefixes([]string{"uptimes/days/"})
	sub.OnUpdate(func(_ []treestore.UpdateEvent) {
		v.tpdUptimeSubMu.Lock()
		if v.tpdUptimeSub != nil {
			v.tpdUptimeSub.lastRootAt = time.Now()
		}
		v.tpdUptimeSubMu.Unlock()
	})
	if err := sub.Connect(tpdPK); err != nil {
		_ = sub.Close() //nolint:errcheck
		return nil, fmt.Errorf("dial tpd uptime publisher: %w", err)
	}
	v.tpdUptimeSub = state
	return state, nil
}

// closeTPDUptimeSubscriber tears down the subscriber on visor close.
// Safe to call multiple times.
func (v *Visor) closeTPDUptimeSubscriber() {
	v.tpdUptimeSubMu.Lock()
	state := v.tpdUptimeSub
	v.tpdUptimeSub = nil
	v.tpdUptimeSubMu.Unlock()
	if state != nil && state.sub != nil {
		_ = state.sub.Close() //nolint:errcheck
	}
}
