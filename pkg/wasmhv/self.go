package wasmhv

import "github.com/skycoin/skywire/pkg/cipher"

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
func (c *Core) selfRoute(self SelfProvider, sub string) (int, []byte) {
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
		// so report the shape with zeros + js/wasm identity. This keeps the
		// multi-visor resources page from 404-erroring on a serverless visor; it
		// renders the row with empty gauges rather than failing the whole page.
		return jsonResp(map[string]interface{}{
			"hostname": "browser", "os": "js", "platform": "wasm", "arch": "wasm",
			"uptime_seconds": 0, "cpu_percent": 0, "cpu_count": 0, "cpu_logical_count": 0,
			"mem_total": 0, "mem_used": 0, "mem_available": 0, "mem_percent": 0,
			"disk_total": 0, "disk_used": 0, "disk_free": 0, "disk_percent": 0,
			"net_bytes_sent": 0, "net_bytes_recv": 0, "net_packets_sent": 0, "net_packets_recv": 0,
		})
	}
	return 404, []byte(`{"error":"self visor subroute not implemented in wasm core"}`)
}
