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
	case "transport-types":
		// The direct transport types the wasm-visor can create (WebRTC is a
		// symmetric DataChannel; ws/wt are browser-dial-only).
		return jsonResp([]string{"dmsg", "ws", "wt", "webrtc"})
	}
	return 404, []byte(`{"error":"self visor subroute not implemented in wasm core"}`)
}
