//go:build js && wasm

// Package main — the extra SelfProvider read-views the HV-UI node page needs from a
// browser visor (dmsg sessions, router settings, runtime config, runtime logs). Each
// returns pre-marshaled JSON matching the native hypervisor's shape so the Angular UI
// renders instead of erroring ("Failed to fetch dmsg sessions" / "subroute not
// implemented"). See pkg/wasmhv.SelfProvider.
package main

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorcore"
)

// --- log ring buffer -------------------------------------------------------
//
// A browser tab has no on-disk log file, so the node Logs page (which polls
// /visors/<pk>/runtime-logs?since=<cursor>) needs an in-memory source. vlog appends
// each step line here; SelfRuntimeLogs serves the delta since a cursor. Bounded so a
// long-lived tab doesn't grow unbounded.

const logRingCap = 1000

type logRing struct {
	mu      sync.Mutex
	lines   []string // JSON log-object strings, oldest first
	base    int64    // cursor value of lines[0] (grows as old lines are dropped)
	dropped int64    // total lines evicted (for the Dropped delta field)
}

var vlogRing = &logRing{}

// append records one log line as a native-shaped JSON object string.
func (r *logRing) append(msg string) {
	entry, err := json.Marshal(map[string]interface{}{
		"time":    time.Now().Format(time.RFC3339),
		"level":   "info",
		"_module": "wasm-visor",
		"msg":     msg,
	})
	if err != nil {
		return
	}
	r.mu.Lock()
	r.lines = append(r.lines, string(entry))
	if len(r.lines) > logRingCap {
		drop := len(r.lines) - logRingCap
		r.lines = r.lines[drop:]
		r.base += int64(drop)
		r.dropped += int64(drop)
	}
	r.mu.Unlock()
}

// since returns entries whose cursor is strictly greater than `since`, the new
// cursor (highest index), and how many entries the caller missed (evicted).
func (r *logRing) since(since int64) (entries []string, latest, dropped int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	latest = r.base + int64(len(r.lines))
	// Start index within r.lines for the first entry newer than `since`.
	start := since - r.base
	if start < 0 {
		// Caller's cursor aged out of the ring — report the gap.
		dropped = r.base - since
		start = 0
	}
	if start > int64(len(r.lines)) {
		start = int64(len(r.lines))
	}
	out := make([]string, 0, int64(len(r.lines))-start)
	out = append(out, r.lines[start:]...)
	return out, latest, dropped
}

// --- SelfProvider read-views ----------------------------------------------

// SelfDmsgSessions serves /visors/<pk>/dmsg/sessions. A browser edge runs only the
// main dmsg client; its sessions are the live client→server sessions (the remote of
// each IS a server). Shape: {main:{pk,role,count,servers}} (route_setup /
// transport_setup omitted — a browser edge embeds neither setup node).
func (s visorSelf) SelfDmsgSessions() []byte {
	if dmsgC == nil {
		return nil
	}
	servers := []cipher.PubKey{}
	for _, cs := range dmsgC.AllSessions() {
		servers = append(servers, cs.RemotePK())
	}
	out := map[string]interface{}{
		"main": map[string]interface{}{
			"pk":      selfPK,
			"role":    "main",
			"count":   len(servers),
			"servers": servers,
		},
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil
	}
	return b
}

// SelfRouterSettings serves /visors/<pk>/router-settings — the four router knobs.
// min_hops is 0 for a browser edge (it originates routes; the finder picks hops).
func (s visorSelf) SelfRouterSettings() []byte {
	if rtr == nil {
		return nil
	}
	out := map[string]interface{}{
		"force_local_routes": rtr.GetForceLocalRoutes(),
		"existing_tp_only":   rtr.GetExistingTPOnly(),
		"mux_routes":         rtr.GetMuxRoutes(),
		"min_hops":           0,
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil
	}
	return b
}

// SelfRuntimeConfig serves /visors/<pk>/runtime-config. A browser edge has no
// on-disk config file — its config IS its build — so serve a representative,
// read-only view (identity + build + the resolved deployment service endpoints).
func (s visorSelf) SelfRuntimeConfig() []byte {
	svc := visorcore.ResolveServices(nil)
	out := map[string]interface{}{
		"version": buildinfo.Version(),
		"pk":      selfPK.Hex(),
		"note":    "browser wasm-visor — no on-disk config; settings are ephemeral to this tab",
		"dmsg": map[string]interface{}{
			"discovery": svc.DmsgDiscoveryDmsg,
		},
		"transport": map[string]interface{}{
			"discovery": svc.TransportDiscoveryDmsg,
		},
		"routing": map[string]interface{}{
			"route_finder": svc.RouteFinderDmsg,
		},
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil
	}
	return b
}

// SelfRuntimeLogs serves /visors/<pk>/runtime-logs?since=<cursor> from the in-tab
// log ring, in the native RuntimeLogsDelta shape {entries,latest,dropped}.
func (s visorSelf) SelfRuntimeLogs(since int64) []byte {
	entries, latest, dropped := vlogRing.since(since)
	b, err := json.Marshal(map[string]interface{}{
		"entries": entries,
		"latest":  latest,
		"dropped": dropped,
	})
	if err != nil {
		return nil
	}
	return b
}
