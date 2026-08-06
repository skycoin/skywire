// Package wasmhv pkg/wasmhv/self.go c3-vis-wasm
package wasmhv

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
)

// SelfProvider supplies the LOCAL (tab's own) visor's hypervisor view, so a
// wasm-visor presents ITSELF in its hypervisor UI alongside the remote visors
// that dial in. Unlike a remote visor (reached over a gob RPC dial), the self
// visor is THIS process — its Overview/Summary/Transports are read straight from
// the tab's own transport.Manager + router, no dmsg round-trip.
//
// A wasm-visor wires this via Core.SetSelf; a plain standalone wasm hypervisor
// (no local visor) leaves it nil and only shows dialed-in visors.
type SelfProvider interface {
	SelfPK() cipher.PubKey
	SelfOverview() Overview
	SelfSummary() Summary
	SelfTransports() []*TransportSummary
	// SelfRoutes returns the tab's own routing rules as pre-marshaled JSON
	// matching the native /visors/{pk}/routes shape ([]{key, rule, rule_summary}).
	// Pre-marshaled so pkg/wasmhv needn't import pkg/routing; nil/empty JSON when
	// the edge holds no rules.
	SelfRoutes() []byte
	// SelfNetworkView returns the SD/TPD/UT-aggregated network table as
	// pre-marshaled JSON (the native /api/network-view shape: {entries,fetched_at}).
	// Pre-marshaled so pkg/wasmhv needn't import the visor aggregation; nil when the
	// tab can't reach the deployment services. Without it the wasm core 404s
	// /api/network-view (which the native HV serves), leaving the network-view table
	// + visualizer empty in the browser.
	SelfNetworkView() []byte
	// SelfNetworkTransports returns the TPD network-wide transport metrics
	// (each {edges:[a,b], type, live, bandwidth, latency}) as pre-marshaled JSON
	// — the native /api/network/transports shape: a flat array the visualizer
	// draws as graph EDGES. Without it the wasm core 404s and the network
	// visualizer renders nodes with no links ("N visors · 0 transports"). nil
	// when the tab can't reach the TPD.
	SelfNetworkTransports(days int) []byte

	// SelfServiceHealth returns the deployment-service health table as the native
	// /api/service-health shape ([]ServiceHealthEntry) pre-marshaled — probed
	// over the tab's dmsg client — so the HV-UI Services-Health tab populates on a
	// browser edge instead of degrading to "not available". nil → the route
	// falls back to [].
	SelfServiceHealth() []byte
	// SelfDmsgSessions returns this tab's dmsg client sessions as pre-marshaled
	// JSON in the native /visors/{pk}/dmsg/sessions shape
	// ({main?,route_setup?,transport_setup?} of {pk,role,count,servers}). A browser
	// edge runs only the main client. nil when dmsg isn't up. Without it the node
	// page's dmsg-sessions expander errors "Failed to fetch dmsg sessions".
	SelfDmsgSessions() []byte
	// SelfDmsgConnectAll opens a dmsg session to every wss-reachable server the
	// tab knows (mirrors the native Visor.DmsgConnectAll), so a browser edge can
	// rendezvous with peers delegated to servers it wasn't already on — the fix
	// for "dmsg error 202 - cannot connect to delegated server" that breaks
	// skychat replies + the resolving proxy. Returns the native
	// DmsgConnectAllResult JSON ({total,already_connected,newly_connected,failed?}).
	SelfDmsgConnectAll() []byte
	// SelfRouterSettings returns the router knobs as pre-marshaled JSON in the
	// native /visors/{pk}/router-settings shape
	// ({force_local_routes,existing_tp_only,mux_routes,min_hops}). nil when the
	// router isn't up. Without it the Router Settings expander errors.
	SelfRouterSettings() []byte
	// SelfRuntimeConfig returns a representative runtime config as pre-marshaled
	// JSON (a browser edge has no on-disk config file). Without it the Runtime
	// Configuration expander errors.
	SelfRuntimeConfig() []byte
	// SelfSetRuntimeConfig applies an edit from the UI's runtime-config editor
	// (the native PUT /visors/{pk}/runtime-config). A browser edge has no on-disk
	// file, so it extracts the reconfigurable service fields, persists them as the
	// page-side override, and triggers a reload — making the SAME editor work on
	// both surfaces. Identity (sk/pk) is not editable. Returns (status, jsonBody).
	SelfSetRuntimeConfig(body []byte) (int, []byte)
	// SelfRuntimeLogs returns the tab's captured log lines newer than `since` as
	// pre-marshaled JSON in the native runtime-logs delta shape
	// ({entries:[]string of JSON log objects, latest, dropped}). Without it the
	// node Logs page errors "self visor subroute not implemented".
	SelfRuntimeLogs(since int64) []byte
	// SelfApps returns the tab's own in-process launcher apps as pre-marshaled
	// JSON ([]AppState, the native /visors/{pk}/apps shape). nil/empty when the
	// browser visor has no app registry. This is what lets the SHARED Angular
	// Apps tab list + control the wasm visor's apps (skychat, skycoin-web, …)
	// exactly as it does a native visor — closing the biggest management gap.
	SelfApps() []byte
	// StartApp / StopApp / SetAutoStart control an in-process app by name. A
	// browser tab has no child processes, so "start/stop" toggles the in-tab
	// AppFunc/route rather than spawning/killing a subprocess; autostart is
	// best-effort per-session (no on-disk config in a tab). Return an error the
	// Apps tab surfaces; unknown app → error.
	StartApp(name string) error
	StopApp(name string) error
	SetAutoStart(name string, autostart bool) error
}

// SetSelf attaches the local visor's hypervisor view. Pass nil to detach.
func (c *Core) SetSelf(self SelfProvider) {
	c.mu.Lock()
	c.self = self
	c.mu.Unlock()
}

// selfProvider returns the attached SelfProvider (nil if none).
func (c *Core) selfProvider() SelfProvider {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.self
}

// selfRoute handles /visors/<selfPK>[/sub] for the local visor, reading the
// tab's own transport.Manager + router instead of dialing over dmsg. Only the
// read views the dashboard needs are served; control sub-routes (app/transport
// mutation) are not yet wired for the self visor and 404.
func (c *Core) selfRoute(self SelfProvider, method, sub string, body []byte, query string) (int, []byte) {
	switch sub {
	case "":
		return jsonResp(self.SelfOverview())
	case "summary":
		return jsonResp(self.SelfSummary())
	case "health":
		// The local visor is, by definition, up if the tab is running.
		return jsonResp(HealthInfo{ServicesHealth: "healthy"})
	case "transports":
		return jsonResp(self.SelfTransports())
	case "routes":
		// The wasm-visor is an edge router; serve its own rules (panic-safe
		// rule.Summary() on the cmd side), so the Routing tab works instead of 404.
		if body := self.SelfRoutes(); body != nil {
			return 200, body
		}
		return jsonResp([]struct{}{})
	case "routegroups":
		// A browser-edge visor doesn't relay route groups; empty (not 404) so the
		// Routing tab renders cleanly rather than erroring.
		return jsonResp([]struct{}{})
	case "transport-types":
		// The direct transport types the wasm-visor can create (WebRTC is a
		// symmetric DataChannel; swsr/swtr are browser-dial-only).
		return jsonResp([]string{"dmsg", "swsr", "swtr", "webrtc"})
	case "host-stats":
		// A browser tab has no host to measure (no CPU/RAM/disk/NIC of its own),
		// so report the shape + js/wasm identity but flag the hardware metrics as
		// unmeasurable via "available": false — the honest signal so the UI can
		// render N/A instead of treating 0% CPU / 0-byte RAM as a real reading
		// (which made a browser visor look idle-but-fine rather than
		// not-applicable). This keeps the multi-visor resources page from
		// 404-erroring on a serverless visor. The zeros remain for older UI that
		// reads the numbers directly.
		return jsonResp(map[string]interface{}{
			"hostname": "browser", "os": "js", "platform": "wasm", "arch": "wasm",
			"available":      false,
			"uptime_seconds": 0, "cpu_percent": 0, "cpu_count": 0, "cpu_logical_count": 0,
			"mem_total": 0, "mem_used": 0, "mem_available": 0, "mem_percent": 0,
			"disk_total": 0, "disk_used": 0, "disk_free": 0, "disk_percent": 0,
			"net_bytes_sent": 0, "net_bytes_recv": 0, "net_packets_sent": 0, "net_packets_recv": 0,
		})
	case "dmsg/sessions":
		// The node page's dmsg-sessions expander. A browser edge runs only the main
		// dmsg client; serve its real sessions so the expander stops erroring.
		if body := self.SelfDmsgSessions(); body != nil {
			return 200, body
		}
		return jsonResp(map[string]interface{}{})
	case "dmsg/connect-all":
		// POST "Connect to all servers": reach every known wss dmsg server so the
		// edge can rendezvous with any peer. Native implements this; the wasm core
		// used to 404, so the button errored in the browser.
		if method != "POST" {
			return 405, []byte(`{"error":"method not allowed"}`)
		}
		if body := self.SelfDmsgConnectAll(); body != nil {
			return 200, body
		}
		return jsonResp(map[string]interface{}{"total": 0, "already_connected": 0, "newly_connected": 0})
	case "router-settings":
		// The Router Settings expander (force-local-routes / existing-tp-only /
		// mux-routes / min-hops).
		if body := self.SelfRouterSettings(); body != nil {
			return 200, body
		}
		return jsonResp(map[string]interface{}{})
	case "runtime-config":
		// The Runtime Configuration expander. GET serves a representative config
		// (a browser edge has no on-disk file). PUT is the UI editor's Save: apply
		// the edit as the page-side override + reload (SelfSetRuntimeConfig), so the
		// same editor drives both the native and browser visor.
		if method == "PUT" {
			return self.SelfSetRuntimeConfig(body)
		}
		if cfg := self.SelfRuntimeConfig(); cfg != nil {
			return 200, cfg
		}
		return jsonResp(map[string]interface{}{})
	case "runtime-logs":
		// The node Logs page (diff-streamed: ?since=<cursor>). Serve the tab's own
		// captured log lines so the page tails instead of 404-erroring.
		if body := self.SelfRuntimeLogs(sinceFromQuery(query)); body != nil {
			return 200, body
		}
		return jsonResp(map[string]interface{}{"entries": []string{}, "latest": 0, "dropped": 0})
	case "ports":
		// Forwarded-port registry: a browser edge hosts content via serveContent,
		// not the native forwarded_ports table. Empty (not 404) so the node page's
		// port fetch renders cleanly.
		return jsonResp([]struct{}{})
	case "apps":
		// The tab's own in-process apps, so the shared Apps tab lists + controls
		// them like a native visor (instead of 404-ing on GET .../apps).
		if b := self.SelfApps(); len(b) > 0 {
			return 200, b
		}
		return jsonResp([]struct{}{})
	}
	if strings.HasPrefix(sub, "apps/") {
		return c.selfAppRoute(self, method, strings.TrimPrefix(sub, "apps/"), body)
	}
	return 404, []byte(`{"error":"self visor subroute not implemented in wasm core"}`)
}

// selfAppRoute handles /visors/<selfPK>/apps/<app> for the tab's own visor —
// the self-visor mirror of the remote appRoute (router.go). GET returns the
// single app; PUT applies status (start/stop) and/or autostart via the
// SelfProvider's in-process app control, then returns the fresh state.
func (c *Core) selfAppRoute(self SelfProvider, method, appName string, body []byte) (int, []byte) {
	// apps/<app>/logs: a browser tab has no per-app BoltDB log store, but the
	// Angular app-detail page requests /logs and treats a 404 as an error
	// dialog. Serve a valid, EMPTY LogsRes (the native shape) so the logs panel
	// renders "no logs" cleanly instead of erroring — the same
	// render-cleanly-don't-404 treatment routegroups/host-stats already get.
	// (Richer in-process app logs on the wasm edge are a separate follow-up.)
	if parts := strings.SplitN(appName, "/", 2); len(parts) == 2 && parts[1] == "logs" {
		return jsonResp(map[string]interface{}{"last_log_timestamp": "", "logs": []string{}})
	}
	// Only the bare app name for the remaining GET/PUT; stats|connections aren't
	// wired for the self visor either — strip the tail.
	appName = strings.SplitN(appName, "/", 2)[0]
	if method == "PUT" {
		var req struct {
			Status    *int  `json:"status,omitempty"`
			AutoStart *bool `json:"autostart,omitempty"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			return 400, []byte(`{"error":"bad request body"}`)
		}
		if req.AutoStart != nil {
			if err := self.SetAutoStart(appName, *req.AutoStart); err != nil {
				return errResp(err)
			}
		}
		if req.Status != nil {
			switch *req.Status {
			case 0:
				if err := self.StopApp(appName); err != nil {
					return errResp(err)
				}
			case 1:
				if err := self.StartApp(appName); err != nil {
					return errResp(err)
				}
			default:
				return 400, []byte(`{"error":"status must be 0 (stop) or 1 (start)"}`)
			}
		}
	}
	// Return the single app's fresh state, read back from the app list.
	var apps []*AppState
	if err := json.Unmarshal(self.SelfApps(), &apps); err == nil {
		for _, a := range apps {
			if a != nil && a.Name == appName {
				return jsonResp(a)
			}
		}
	}
	return 404, []byte(`{"error":"app not found"}`)
}

// sinceFromQuery parses the runtime-logs diff cursor from a raw query string
// ("since=<n>"); returns 0 (full buffer) when absent or malformed.
func sinceFromQuery(query string) int64 {
	for _, kv := range strings.Split(query, "&") {
		if v, ok := strings.CutPrefix(kv, "since="); ok {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				return n
			}
		}
	}
	return 0
}
