//go:build js && wasm

// Package main cmd/wasm-visor/registrationstate_js.go c3-vis-wasm
// registrationstate_js.go — the browser visor's transport-registration
// introspection, the wasm analog of `skywire cli visor state`'s
// transport-registration view on a native visor. A browser visor registers its
// transports by publishing a CXO "tp-list" snapshot leaf that TPD's
// cxo-aggregator ingests (pkg/transport/manager.go publishTPDList); TPD only
// subscribes to (and therefore ingests) our feed AFTER a successful AnnounceTo.
// When a route-find to this visor returns "transport not found", the cause is
// almost always one of: (a) we never published a tp-list yet, (b) the publish is
// FROZEN on a standing CXO error so TPD is pinned to a stale/empty snapshot (the
// browser analog of the native bbolt stale-page bug), or (c) our announces to TPD
// are failing so TPD never subscribed. This file records all three so the
// expanded visorStats() can show which it is, instead of guessing.
package main

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
)

// tpListLeafPath mirrors the unexported transport.tpdListPath const
// (pkg/transport/manager.go) — the single top-level CXO leaf carrying this
// visor's full transport list. Kept in sync by hand; it changes ~never.
const tpListLeafPath = "tp-list"

// regStateT captures what we've published to TPD for transport discovery and how
// our announces to TPD are faring. Purely observational; guarded by its own mutex
// so the sample loop, announce loop and visorStats reader don't race.
type regStateT struct {
	mu sync.Mutex

	// Last tp-list snapshot published (the routable transport set TPD ingests).
	lastListAt    time.Time
	lastListBytes int
	lastListCount int
	lastListErr   string
	listPublishes int

	// Announces to TPD (the subscription trigger — no ingest without one).
	lastAnnounceOKAt  time.Time
	lastAnnounceErrAt time.Time
	lastAnnounceErr   string
	announceOKs       int
	announceFails     int
}

var regStat regStateT

// telemetryTPDPK is the TPD public key the telemetry publisher announces to;
// stored so visorStats can report the announce target and whether TPD is on our
// subscriber allowlist. Set once in startTelemetry.
var telemetryTPDPK cipher.PubKey

// recordingLeafPublisher wraps the CXO publisher handed to the transport
// manager's SetTPDLeafPublisher hook, recording each tp-list snapshot as it is
// published so visorStats can show the exact transport set — and count — we told
// TPD about, and when. Everything is delegated to the wrapped publisher; this
// adds observation only, no behavior change.
type recordingLeafPublisher struct {
	inner transport.TPDLeafPublisher
}

func (r recordingLeafPublisher) Put(path string, value []byte) error {
	err := r.inner.Put(path, value)
	if path == tpListLeafPath {
		regStat.mu.Lock()
		regStat.lastListAt = time.Now()
		regStat.lastListBytes = len(value)
		regStat.listPublishes++
		if err != nil {
			regStat.lastListErr = err.Error()
		} else {
			regStat.lastListErr = ""
			regStat.lastListCount = countCompactEntries(value)
		}
		regStat.mu.Unlock()
	}
	return err
}

func (r recordingLeafPublisher) Delete(path string) error { return r.inner.Delete(path) }

// countCompactEntries decodes the tp-list leaf body ({version, c:[...compact]})
// enough to count the transports it advertises. Returns -1 on a malformed body.
func countCompactEntries(value []byte) int {
	var v struct {
		C []json.RawMessage `json:"c"`
	}
	if err := json.Unmarshal(value, &v); err != nil {
		return -1
	}
	return len(v.C)
}

// recordAnnounceResult is called by the telemetry announce loop after each
// AnnounceTo attempt so visorStats can show whether TPD is being reached.
func recordAnnounceResult(err error) {
	regStat.mu.Lock()
	defer regStat.mu.Unlock()
	if err != nil {
		regStat.announceFails++
		regStat.lastAnnounceErrAt = time.Now()
		regStat.lastAnnounceErr = err.Error()
		return
	}
	regStat.announceOKs++
	regStat.lastAnnounceOKAt = time.Now()
	regStat.lastAnnounceErr = ""
}

// secsSince returns whole seconds since t, or -1 if t is the zero time (never).
func secsSince(t time.Time) int64 {
	if t.IsZero() {
		return -1
	}
	return int64(time.Since(t).Seconds())
}

// transportRegistrationSnapshot builds the transport_registration object folded
// into visorStats(). It cross-references three things an operator needs to
// diagnose "transport not found": the live transport set, what we've PUBLISHED to
// TPD (tp-list), and whether TPD can/does ingest it (publisher PublishState —
// including the Frozen stale-snapshot flag — plus announce success). Safe to call
// before telemetry is up (fields default to zero/absent).
func transportRegistrationSnapshot() map[string]interface{} {
	out := map[string]interface{}{}

	// Live transport set (what SHOULD be routable), so it can be compared against
	// the published tp-list count below.
	if tpM != nil {
		var live []map[string]interface{}
		tpM.WalkTransports(func(tp *transport.ManagedTransport) bool {
			live = append(live, map[string]interface{}{
				"id":     tp.Entry.ID.String(),
				"type":   string(tp.Entry.Type),
				"edges":  []string{tp.Entry.Edges[0].Hex(), tp.Entry.Edges[1].Hex()},
				"closed": tp.IsClosed(),
			})
			return true
		})
		out["live_transports"] = live
		out["live_count"] = len(live)
	}

	// Publisher state — the crux. PublishState().Frozen is the browser analog of
	// the native bbolt stale-page bug: the feed advanced but no Root was saved
	// since a standing error, so TPD is pinned to a stale snapshot.
	if telemetryPub != nil {
		out["publisher_up"] = true
		out["feed"] = telemetryPub.Feed().Hex()
		out["publish_state"] = telemetryPub.PublishState()
		out["node_stats"] = telemetryPub.Stats()
		if !telemetryTPDPK.Null() {
			out["tpd_target"] = telemetryTPDPK.Hex()
			out["tpd_subscribe_allowed"] = telemetryPub.AllowsSubscriber(telemetryTPDPK)
		}
	} else {
		out["publisher_up"] = false
	}

	// What we've published + how announces are going.
	regStat.mu.Lock()
	out["tp_list"] = map[string]interface{}{
		"publishes":       regStat.listPublishes,
		"last_count":      regStat.lastListCount,
		"last_bytes":      regStat.lastListBytes,
		"last_err":        regStat.lastListErr,
		"secs_since_last": secsSince(regStat.lastListAt),
	}
	out["announce"] = map[string]interface{}{
		"oks":            regStat.announceOKs,
		"fails":          regStat.announceFails,
		"last_err":       regStat.lastAnnounceErr,
		"secs_since_ok":  secsSince(regStat.lastAnnounceOKAt),
		"secs_since_err": secsSince(regStat.lastAnnounceErrAt),
	}
	regStat.mu.Unlock()

	return out
}
