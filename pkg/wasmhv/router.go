// Package wasmhv pkg/wasmhv/router.go c3-vis-wasm
package wasmhv

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
)

var (
	errNotConnected = errors.New("visor not connected")
	errRPCTimeout   = errors.New("visor RPC timeout")
)

// ServeHTTP routes a hypervisor /api request and returns (status, jsonBody).
// It is the in-wasm equivalent of the binary hypervisor's HTTP handler — the
// browser's fetch/XHR override (override.js) calls this instead of going over
// dmsg to a remote hypervisor. The read dashboard endpoints plus the apps /
// transports control endpoints are implemented; unknown routes 404.
func (c *Core) ServeHTTP(method, path string, body []byte) (int, []byte) {
	p := strings.TrimPrefix(path, "/api")
	p = strings.SplitN(p, "?", 2)[0]
	p = strings.TrimSuffix(p, "/")

	switch {
	case p == "/ping":
		return 200, []byte(`"PONG!"`)

	case p == "/csrf":
		return jsonResp(map[string]string{"csrf_token": "wasm-hv"})

	// No-auth mode: report no user-management so the UI skips the login flow
	// and lands on the dashboard (mirrors a hypervisor built with
	// EnableAuth=false, where create-account/login aren't registered).
	case p == "/user-exists":
		return jsonResp(false)
	case p == "/user":
		return jsonResp(map[string]interface{}{"username": "admin"})

	case p == "/about":
		connected := false
		sessions := 0
		if c.dmsgC != nil {
			sessions = len(c.dmsgC.AllSessions())
			connected = sessions > 0
		}
		return jsonResp(About{PubKey: c.pk, Build: buildinfo.Get(), DmsgConnected: connected, DmsgSessions: sessions})

	case p == "/visors":
		return jsonResp(c.allOverviews())

	case p == "/visors-summary":
		return jsonResp(c.allSummaries())

	// The node-list UI uses /visors-tree-summary (getNodesTree) and expects a
	// {sections:[{hypervisor_pk, visors:[...]}]} TREE. Section 0 is this
	// hypervisor; each connected visor that is ITSELF a hypervisor gets its own
	// section, so the UI renders separate per-hypervisor clusters like the native
	// HV (was a single flat section here).
	case p == "/visors-tree-summary":
		return jsonResp(map[string]interface{}{"sections": c.treeSections()})

	case strings.HasPrefix(p, "/visors/"):
		rawQuery := ""
		if i := strings.IndexByte(path, '?'); i >= 0 {
			rawQuery = path[i+1:]
		}
		return c.visorRoute(method, strings.TrimPrefix(p, "/visors/"), body, rawQuery)

	// Network-wide SD/TPD/UT aggregation (the network-view table + the
	// visualizer's nodes). The native HV serves this; without it here the wasm
	// core 404s and both render empty. The tab builds it from its own dmsg fetch
	// of the deployment services (reachable since the service-PK seed fix).
	case p == "/network-view":
		if self := c.selfProvider(); self != nil {
			if b := self.SelfNetworkView(); len(b) > 0 {
				return 200, b
			}
		}
		return 200, []byte(`{"entries":[],"fetched_at":""}`)

	// The visualizer's EDGES: TPD network-wide transport metrics. The query is
	// stripped from p above, so parse days= from the raw path (bounded 0..35 like
	// the native handler). Without this the visualizer draws nodes with no links.
	case p == "/network/transports":
		days := 1
		if q := strings.SplitN(path, "?", 2); len(q) == 2 {
			for _, kv := range strings.Split(q[1], "&") {
				if v, ok := strings.CutPrefix(kv, "days="); ok {
					if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 35 {
						days = n
					}
				}
			}
		}
		if self := c.selfProvider(); self != nil {
			if b := self.SelfNetworkTransports(days); len(b) > 0 {
				return 200, b
			}
		}
		return 200, []byte(`[]`)

	// The hvui client-error-reporter POSTs console errors here to ship them to
	// the visor log. In the browser the console already IS the log, so accept +
	// discard rather than 404 every reported error (which itself spams console).
	case p == "/client-log":
		return 200, []byte(`{"ok":true}`)
	}
	return 404, []byte(`{"error":"not found in wasm hypervisor core"}`)
}

// visorRoute handles /visors/<pk>[/sub] for all HTTP methods. query is the raw
// query string (stripped from the path), threaded through for self subroutes that
// need it (e.g. runtime-logs ?since=<cursor>).
func (c *Core) visorRoute(method, rest string, body []byte, query string) (int, []byte) {
	parts := strings.SplitN(rest, "/", 2)
	var pk cipher.PubKey
	if err := pk.UnmarshalText([]byte(parts[0])); err != nil {
		return 400, []byte(`{"error":"bad pk"}`)
	}
	sub := ""
	if len(parts) == 2 {
		sub = parts[1]
	}
	// The tab's OWN visor is served locally (its transport.Manager + router),
	// not over a gob RPC dial.
	if self := c.selfProvider(); self != nil && self.SelfPK() == pk {
		return c.selfRoute(self, method, sub, body, query)
	}
	switch {
	case sub == "":
		return jsonResp(c.overviewOf(pk))
	case sub == "summary":
		return jsonResp(c.summaryOf(pk))
	case sub == "health":
		var h HealthInfo
		if err := c.call(pk, "Health", &struct{}{}, &h); err != nil {
			return jsonResp(HealthInfo{})
		}
		return jsonResp(h)

	// --- apps ---
	case sub == "apps":
		apps, err := c.apps(pk)
		if err != nil {
			return errResp(err)
		}
		return jsonResp(apps)
	case strings.HasPrefix(sub, "apps/"):
		return c.appRoute(method, pk, strings.TrimPrefix(sub, "apps/"), body)

	// --- transports ---
	case sub == "transport-types":
		types, err := c.transportTypes(pk)
		if err != nil {
			return errResp(err)
		}
		return jsonResp(types)
	case sub == "transports":
		return c.transportsRoute(method, pk, body)
	case strings.HasPrefix(sub, "transports/"):
		return c.transportRoute(method, pk, strings.TrimPrefix(sub, "transports/"))
	}
	return 404, []byte(`{"error":"visor subroute not implemented in wasm core"}`)
}

// appRoute handles /visors/<pk>/apps/<app>: GET returns the app, PUT applies
// status (start/stop) and/or autostart changes then returns the fresh state.
func (c *Core) appRoute(method string, pk cipher.PubKey, appName string, body []byte) (int, []byte) {
	// The app name is the remaining path segment; sub-routes like
	// apps/<app>/logs|stats|connections aren't implemented in the wasm core yet.
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
			if err := c.setAutoStart(pk, appName, *req.AutoStart); err != nil {
				return errResp(err)
			}
		}
		if req.Status != nil {
			switch *req.Status {
			case 0:
				if err := c.stopApp(pk, appName); err != nil {
					return errResp(err)
				}
			case 1:
				if err := c.startApp(pk, appName); err != nil {
					return errResp(err)
				}
			default:
				return 400, []byte(`{"error":"status must be 0 (stop) or 1 (start)"}`)
			}
		}
	}
	app, err := c.app(pk, appName)
	if err != nil {
		return errResp(err)
	}
	return jsonResp(app)
}

// transportsRoute handles /visors/<pk>/transports: GET lists, POST adds.
func (c *Core) transportsRoute(method string, pk cipher.PubKey, body []byte) (int, []byte) {
	if method == "POST" {
		var req struct {
			TpType     string        `json:"transport_type"`
			Remote     cipher.PubKey `json:"remote_pk"`
			Label      string        `json:"label,omitempty"`
			NoRegister bool          `json:"no_register,omitempty"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			return 400, []byte(`{"error":"bad request body"}`)
		}
		ts, err := c.addTransport(pk, addTransportIn{
			RemotePK:   req.Remote,
			TpType:     req.TpType,
			Label:      req.Label,
			NoRegister: req.NoRegister,
		})
		if err != nil {
			return errResp(err)
		}
		return jsonResp(ts)
	}
	tps, err := c.transports(pk)
	if err != nil {
		return errResp(err)
	}
	return jsonResp(tps)
}

// transportRoute handles /visors/<pk>/transports/<tid>: DELETE removes it.
func (c *Core) transportRoute(method string, pk cipher.PubKey, tid string) (int, []byte) {
	tid = strings.SplitN(tid, "/", 2)[0]
	id, err := uuid.Parse(tid)
	if err != nil {
		return 400, []byte(`{"error":"bad transport id"}`)
	}
	if method == "DELETE" {
		if err := c.removeTransport(pk, id); err != nil {
			return errResp(err)
		}
		return jsonResp(true)
	}
	return 404, []byte(`{"error":"transport subroute not implemented in wasm core"}`)
}

// allOverviews concurrently fetches every connected visor's Overview, with the
// tab's OWN visor (if any) listed first.
func (c *Core) allOverviews() []Overview {
	pks := c.connectedPKs()
	out := make([]Overview, len(pks))
	var wg sync.WaitGroup
	wg.Add(len(pks))
	for i, pk := range pks {
		go func(i int, pk cipher.PubKey) { defer wg.Done(); out[i] = c.overviewOf(pk) }(i, pk)
	}
	wg.Wait()
	if self := c.selfProvider(); self != nil {
		out = append([]Overview{self.SelfOverview()}, out...)
	}
	return out
}

// allSummaries concurrently fetches every connected visor's Summary, with the
// tab's OWN visor (if any) listed first.
func (c *Core) allSummaries() []Summary {
	pks := c.connectedPKs()
	out := make([]Summary, len(pks))
	var wg sync.WaitGroup
	wg.Add(len(pks))
	for i, pk := range pks {
		go func(i int, pk cipher.PubKey) { defer wg.Done(); out[i] = c.summaryOf(pk) }(i, pk)
	}
	wg.Wait()
	if self := c.selfProvider(); self != nil {
		out = append([]Summary{self.SelfSummary()}, out...)
	}
	return out
}

// treeSections groups the connected visors into the per-hypervisor sections the
// node-list UI renders as separate clusters (result.sections). Section 0 is this
// wasm hypervisor (self + everything connected to it); then one section per
// connected visor that is ITSELF a hypervisor, whose members are the visors that
// report it in their connected_hypervisor list. A visor managed by several
// hypervisors appears under each, mirroring the native HV's tree. Previously the
// wasm HV emitted a single flat section, so no per-hypervisor clusters showed.
func (c *Core) treeSections() []map[string]interface{} {
	sums := c.allSummaries()
	sections := []map[string]interface{}{
		{"hypervisor_pk": c.pk.String(), "via_chain": []string{}, "visors": sums},
	}
	seen := map[string]bool{c.pk.String(): true}
	for _, s := range sums {
		if !s.IsHypervisor || s.Overview == nil {
			continue
		}
		hvpk := s.Overview.PubKey.String()
		if seen[hvpk] {
			continue
		}
		seen[hvpk] = true
		members := []Summary{}
		for _, m := range sums {
			if m.Overview == nil {
				continue
			}
			for _, ch := range m.Overview.ConnectedHypervisor {
				if ch.String() == hvpk {
					members = append(members, m)
					break
				}
			}
		}
		sections = append(sections, map[string]interface{}{
			"hypervisor_pk": hvpk, "via_chain": []string{}, "visors": members,
		})
	}
	return sections
}

func jsonResp(v interface{}) (int, []byte) {
	b, err := json.Marshal(v)
	if err != nil {
		return 500, []byte(`{"error":"marshal"}`)
	}
	return 200, b
}

// errResp renders an error as the hypervisor's {"error":"..."} shape with a 500.
// A not-connected / timeout error is the common case (the visor dropped its dial
// since the last poll); the UI surfaces the message as-is.
func errResp(err error) (int, []byte) {
	b, mErr := json.Marshal(map[string]string{"error": err.Error()})
	if mErr != nil {
		return 500, []byte(`{"error":"internal"}`)
	}
	return 500, b
}
