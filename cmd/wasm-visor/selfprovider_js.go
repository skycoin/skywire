//go:build js && wasm

// Package main cmd/wasm-visor/selfprovider_js.go c3-vis-wasm
// browser visor (dmsg sessions, router settings, runtime config, runtime logs). Each
// returns pre-marshaled JSON matching the native hypervisor's shape so the Angular UI
// renders instead of erroring ("Failed to fetch dmsg sessions" / "subroute not
// implemented"). See pkg/wasmhv.SelfProvider.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"syscall/js"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
)

// vlogHook mirrors every Go subsystem log line (dmsg / transport / router / …)
// into the in-tab log ring, so the Angular node Logs page (/runtime-logs →
// vlogRing) shows the SAME firehose as the browse.js desktop log window (which
// reads the browser console capture). Without it the two log surfaces diverged —
// the desktop window had the full log, the Angular page only sparse vlog()
// markers — one instance of the recurring wasm dual-surface divergence.
type vlogHook struct{}

func (vlogHook) Levels() []logrus.Level { return logrus.AllLevels }

func (vlogHook) Fire(e *logrus.Entry) error {
	line := e.Message
	if s, err := e.String(); err == nil {
		line = strings.TrimRight(s, "\n")
	}
	vlogRing.appendLevel(e.Level.String(), line)
	// Mirror WARN+ from EVERY subsystem (transport dial, route setup, dmsg
	// session, skysocks) out through the __skylog bridge so the dev harness's
	// shell-readable /ctl/log carries the FAILURE firehose — not just vlog()
	// step markers. Previously these lived only in the in-tab ring
	// (/runtime-logs) and the browser console, so route/exit/transport failures
	// weren't diagnosable from a shell. Gated to warn+ so the info firehose
	// doesn't flood the bridge, and to __skylog's presence (harness/dev only).
	if e.Level <= logrus.WarnLevel {
		if h := js.Global().Get("__skylog"); h.Type() == js.TypeFunction {
			h.Invoke("[" + e.Level.String() + "] " + line)
		}
	}
	return nil
}

// --- log ring buffer -------------------------------------------------------
//
// A browser tab has no on-disk log file, so the node Logs page (which polls
// /visors/<pk>/runtime-logs?since=<cursor>) needs an in-memory source. vlog appends
// each step line here; SelfRuntimeLogs serves the delta since a cursor. Bounded so a
// long-lived tab doesn't grow unbounded.

// Sized to match the browse.js desktop log window's console buffer (5000) so the
// Angular /runtime-logs page shows a comparable window of the same firehose now
// that vlogHook mirrors the Go subsystem logs in here too (not just vlog markers).
const logRingCap = 5000

type logRing struct {
	mu      sync.Mutex
	lines   []string // JSON log-object strings, oldest first
	base    int64    // cursor value of lines[0] (grows as old lines are dropped)
	dropped int64    // total lines evicted (for the Dropped delta field)
}

var vlogRing = &logRing{}

// append records one info-level log line (the vlog() step markers).
func (r *logRing) append(msg string) { r.appendLevel("info", msg) }

// appendLevel records one log line with an explicit level as a native-shaped
// JSON object string. Used both by vlog() (info) and by vlogHook, which mirrors
// the Go subsystem firehose in at its real level.
func (r *logRing) appendLevel(level, msg string) {
	entry, err := json.Marshal(map[string]interface{}{
		"time":    time.Now().Format(time.RFC3339),
		"level":   level,
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
	sessions := []map[string]interface{}{}
	for _, cs := range dmsgC.AllSessions() {
		servers = append(servers, cs.RemotePK())
		// Mirror the native DmsgServerSession shape so the shared Angular UI —
		// and the wasm-visor onboarding tour — can show which protocol
		// (tcp/ws/wss/webtransport/quic) this browser edge reached each dmsg
		// server over. A browser edge dials ws/wss or WebTransport, never tcp.
		sessions = append(sessions, map[string]interface{}{
			"pk":       cs.RemotePK(),
			"carrier":  cs.Carrier(),
			"protocol": cs.Protocol(),
			"address":  cs.CarrierAddr(),
		})
	}
	out := map[string]interface{}{
		"main": map[string]interface{}{
			"pk":       selfPK,
			"role":     "main",
			"count":    len(servers),
			"servers":  servers,
			"sessions": sessions,
		},
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil
	}
	return b
}

// SelfDmsgConnectAll serves POST /visors/<pk>/dmsg/connect-all. A browser edge
// boots with a session to only a couple of dmsg servers (sessions_count), so it
// can't reach peers delegated to OTHER servers → "dmsg error 202 - cannot
// connect to delegated server" (the skychat-reply + resolving-proxy failures).
// This opens a session to every wss-reachable server the deployment config
// advertises — concurrently, mirroring the boot seed loop (main.go) — so the
// edge can rendezvous with any peer. Result mirrors native Visor.DmsgConnectAll.
func (s visorSelf) SelfDmsgConnectAll() []byte {
	res := map[string]interface{}{"total": 0, "already_connected": 0, "newly_connected": 0}
	if dmsgC == nil {
		b, _ := json.Marshal(res)
		return b
	}
	// ConnectToAllServers enumerates every server in dmsg DISCOVERY (not just the
	// 2 embedded seed servers) and EnsureSessions each over the browser's wss
	// carrier — the same routine the native visor uses. Bounded so a slow/dead
	// server can't hang the /api call forever.
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	r, err := dmsgC.ConnectToAllServers(ctx)
	res["total"] = r.Total
	res["already_connected"] = r.AlreadyConnected
	res["newly_connected"] = r.NewlyConnected
	if len(r.Failed) > 0 {
		failed := make(map[string]string, len(r.Failed))
		for pk, e := range r.Failed {
			failed[pk.String()] = e
		}
		res["failed"] = failed
	}
	if err != nil {
		res["error"] = err.Error()
	}
	vlog(fmt.Sprintf("dmsg connect-all: total=%d already=%d newly=%d failed=%d err=%v",
		r.Total, r.AlreadyConnected, r.NewlyConnected, len(r.Failed), err))
	b, _ := json.Marshal(res)
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
// read-only view that MIRRORS the native `skywire cli config gen` (visorconfig.V1)
// SHAPE section-for-section, so the Runtime Configuration expander shows the same
// surface on both. Every section the wasm visor genuinely has is populated from
// the resolved deployment services + the in-tab runtime state; sections that
// can't exist in a browser tab (raw sockets, on-disk paths, local HTTP servers)
// are present as no-op stubs (so nothing silently disappears) and enumerated in
// platform_omitted with the reason.
//
// We hand-build the shape rather than marshal a real visorconfig.V1: that package
// transitively pulls modernc.org/libc (sqlite/bbolt), whose build constraints
// exclude js/wasm, so it can't be imported into the wasm blob. The `sk` field is
// deliberately never included (never expose the secret key from a served view).
func (s visorSelf) SelfRuntimeConfig() []byte {
	// effectiveSvc = deployment defaults with any boot-time page override applied
	// (configoverride_js.go), so this view matches what the runtime is wired to.
	svc := effectiveSvc
	pkHexes := func(pks []cipher.PubKey) []string {
		out := make([]string, 0, len(pks))
		for _, p := range pks {
			out = append(out, p.Hex())
		}
		return out
	}
	// launcher.apps — the in-tab app registry (skychat + skysocks-client-lite
	// instances) in the native launcher.apps config shape, so the wasm visor's
	// Apps tab and its config agree (these are real, controllable apps).
	apps := []map[string]interface{}{
		{"name": appSkychat, "auto_start": autoStartPref(appSkychat), "port": skychatPort},
	}
	for _, inst := range proxyInstancesSnapshot() {
		app := map[string]interface{}{"name": inst.ID, "auto_start": autoStartPref(inst.ID), "port": skysocksPort}
		if inst.Exit != "" {
			app["args"] = "--srv " + inst.Exit
		}
		apps = append(apps, app)
	}

	out := map[string]interface{}{
		"version":          buildinfo.VersionOrCommit(),
		"pk":               selfPK.Hex(),
		"note":             "browser wasm-visor — no on-disk config, but this IS the config the runtime uses (it mirrors the native config-gen shape). Service endpoints are runtime-reconfigurable: editing + Save persists an override in this browser and reloads the visor with it. config_overrides lists the currently pinned fields (empty = pure deployment defaults). platform_omitted lists sections a browser tab can't have; the secret key is never shown.",
		"config_overrides": cfgOverride,
		// --- applicable sections (populated) ---
		"dmsg": map[string]interface{}{
			"discovery": svc.DmsgDiscoveryDmsg,
			"protocol":  "yamux",
		},
		"transport": map[string]interface{}{
			"discovery":          svc.TransportDiscoveryDmsg,
			"address_resolver":   svc.AddressResolverDmsg,
			"public_autoconnect": true, // startWSAutoconnect dials public WS transports
			"transport_setup":    pkHexes(svc.TransportSetupNodes),
			// stcpr_port / sudph_port are raw-socket only → see platform_omitted.
		},
		"routing": map[string]interface{}{
			"route_finder":         svc.RouteFinderDmsg,
			"route_setup_nodes":    pkHexes(svc.RouteSetupNodes),
			"min_hops":             svc.MinHops,
			"route_finder_timeout": "10s",
		},
		"launcher": map[string]interface{}{
			"service_discovery": svc.ServiceDiscoveryDmsg,
			"apps":              apps,
		},
		"stun_servers":          svc.StunServers, // WebRTC ICE
		"conf_service":          svc.ConfDmsg,
		"uptime_tracker":        svc.UptimeTrackerDmsg,
		"reward_system":         svc.RewardSystemDmsg,
		"is_public":             false,
		"persistent_transports": nil,
		// --- sections a browser tab cannot have (kept as no-op stubs for shape parity) ---
		"pty":         map[string]interface{}{},
		"ui_server":   map[string]interface{}{"enable": false},
		"skywire-tcp": map[string]interface{}{},
		"hypervisor":  map[string]interface{}{"enable": false},
		"hypervisors": []string{},
		"platform_omitted": map[string]string{
			"pty":                             "no unix socket / child processes in a browser tab",
			"skywire-tcp":                     "no raw TCP listener (stcpr) in a browser",
			"transport.stcpr_port/sudph_port": "no raw TCP/UDP sockets in a browser (direct p2p is WebRTC; carriers are ws/wt/dmsg)",
			"ui_server/hypervisor":            "no local HTTP server or users.db in a tab",
			"local_path/log_store/bin_path":   "no filesystem; apps run in-process, logs live in an in-memory ring",
			"sk":                              "never exposed from a served view",
		},
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil
	}
	return b
}

// SelfSetRuntimeConfig applies an edit from the UI's runtime-config editor (the
// native PUT /visors/{pk}/runtime-config). The edited JSON has the
// SelfRuntimeConfig shape; extract the reconfigurable service fields into the
// flat override contract (configoverride_js.go), reject any identity change, then
// hand the override to the page — which owns localStorage + the reload — via the
// worker's __skywireSaveConfig bridge. So the same editor Saves on both surfaces.
func (s visorSelf) SelfSetRuntimeConfig(raw []byte) (int, []byte) {
	var in map[string]interface{}
	if err := json.Unmarshal(raw, &in); err != nil {
		return 400, []byte(`{"error":"invalid runtime config json"}`)
	}
	if pk, ok := in["pk"].(string); ok && pk != "" && pk != selfPK.Hex() {
		return 400, []byte(`{"error":"public key is not editable via runtime config"}`)
	}

	ov := map[string]interface{}{}
	sub := func(section string) map[string]interface{} {
		m, _ := in[section].(map[string]interface{})
		return m
	}
	str := func(dst string, m map[string]interface{}, key string) {
		if m != nil {
			if v, ok := m[key].(string); ok && v != "" {
				ov[dst] = v
			}
		}
	}
	raw2 := func(dst string, m map[string]interface{}, key string) {
		if m != nil {
			if v, ok := m[key]; ok && v != nil {
				ov[dst] = v
			}
		}
	}
	dmsg, transport, routing, launcher := sub("dmsg"), sub("transport"), sub("routing"), sub("launcher")
	str("dmsg_discovery", dmsg, "discovery")
	str("transport_discovery", transport, "discovery")
	str("address_resolver", transport, "address_resolver")
	raw2("transport_setup_nodes", transport, "transport_setup")
	str("route_finder", routing, "route_finder")
	raw2("route_setup_nodes", routing, "route_setup_nodes")
	raw2("min_hops", routing, "min_hops")
	str("service_discovery", launcher, "service_discovery")
	str("conf_service", in, "conf_service")
	str("uptime_tracker", in, "uptime_tracker")
	str("reward_system", in, "reward_system")
	raw2("stun_servers", in, "stun_servers")

	ovJSON, err := json.Marshal(ov)
	if err != nil {
		return 500, []byte(`{"error":"marshal override"}`)
	}
	saver := js.Global().Get("__skywireSaveConfig")
	if saver.Type() != js.TypeFunction {
		return 501, []byte(`{"error":"config save bridge unavailable in this context"}`)
	}
	saver.Invoke(string(ovJSON))
	return 200, []byte(`{"ok":true,"note":"configuration saved; reloading the visor to apply"}`)
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
