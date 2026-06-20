package wasmhv

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"

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
// dmsg to a remote hypervisor. Only the read endpoints needed for the node
// dashboard are implemented so far; unknown routes 404.
func (c *Core) ServeHTTP(method, path string, _ []byte) (int, []byte) {
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

	case p == "/visors-summary" || p == "/visors-tree-summary":
		return jsonResp(c.allSummaries())

	case strings.HasPrefix(p, "/visors/"):
		return c.visorRoute(strings.TrimPrefix(p, "/visors/"))
	}
	return 404, []byte(`{"error":"not found in wasm hypervisor core"}`)
}

// visorRoute handles /visors/<pk>[/sub].
func (c *Core) visorRoute(rest string) (int, []byte) {
	parts := strings.SplitN(rest, "/", 2)
	var pk cipher.PubKey
	if err := pk.UnmarshalText([]byte(parts[0])); err != nil {
		return 400, []byte(`{"error":"bad pk"}`)
	}
	sub := ""
	if len(parts) == 2 {
		sub = parts[1]
	}
	switch sub {
	case "":
		ov := c.overviewOf(pk)
		return jsonResp(ov)
	case "summary":
		return jsonResp(c.summaryOf(pk))
	case "health":
		var h HealthInfo
		if err := c.call(pk, "Health", &struct{}{}, &h); err != nil {
			return jsonResp(HealthInfo{})
		}
		return jsonResp(h)
	}
	return 404, []byte(`{"error":"visor subroute not implemented in wasm core"}`)
}

// allOverviews concurrently fetches every connected visor's Overview.
func (c *Core) allOverviews() []Overview {
	pks := c.connectedPKs()
	out := make([]Overview, len(pks))
	var wg sync.WaitGroup
	wg.Add(len(pks))
	for i, pk := range pks {
		go func(i int, pk cipher.PubKey) { defer wg.Done(); out[i] = c.overviewOf(pk) }(i, pk)
	}
	wg.Wait()
	return out
}

// allSummaries concurrently fetches every connected visor's Summary.
func (c *Core) allSummaries() []Summary {
	pks := c.connectedPKs()
	out := make([]Summary, len(pks))
	var wg sync.WaitGroup
	wg.Add(len(pks))
	for i, pk := range pks {
		go func(i int, pk cipher.PubKey) { defer wg.Done(); out[i] = c.summaryOf(pk) }(i, pk)
	}
	wg.Wait()
	return out
}

func jsonResp(v interface{}) (int, []byte) {
	b, err := json.Marshal(v)
	if err != nil {
		return 500, []byte(`{"error":"marshal"}`)
	}
	return 200, b
}
