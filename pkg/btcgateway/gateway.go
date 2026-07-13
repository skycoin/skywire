// Package btcgateway serves the skycoin-web wallet's /v1/btc/* endpoints by
// translating them to an Electrum backend, so a Skywire visor can provide the
// in-browser BTC wallet its chain data (balance / utxos / history / fee /
// broadcast). The wallet keeps keys, address derivation and signing entirely
// client-side (skycoin-lite WASM) — only these chain queries cross the mesh.
//
// The Electrum connection is dialed via an optional DialFunc, so the visor can
// reach an ssl:// Electrum server through skysocks/dmsg over the mesh (nil dial
// = clearnet, for a native visor that already has egress). This is what lets
// BTC "just work" in the browser wallet without per-port dmsg forwarding —
// a raw ssl://host:port electrum URL is enough.
package btcgateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/skycoin/skycoin/src/btc"
	"github.com/skycoin/skycoin/src/electrum"
)

// DialFunc routes the Electrum TCP connection over a custom transport (e.g.
// skysocks-client-lite for clearnet, or a dmsg stream). nil = stdlib clearnet.
type DialFunc = electrum.DialFunc

// Gateway is an /v1/btc/* HTTP handler backed by Electrum. Backends are cached
// per Electrum server URL because Electrum connections are long-lived.
type Gateway struct {
	dial     DialFunc
	mu       sync.Mutex
	backends map[string]btc.Backend
}

// New returns a Gateway that dials Electrum via dial (nil = clearnet).
func New(dial DialFunc) *Gateway {
	return &Gateway{dial: dial, backends: make(map[string]btc.Backend)}
}

// Close shuts down all cached Electrum backends.
func (g *Gateway) Close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for url, b := range g.backends {
		_ = b.Close() //nolint:errcheck
		delete(g.backends, url)
	}
}

// backendFor returns a cached (or newly-connected) Electrum backend for url.
func (g *Gateway) backendFor(url string) (btc.Backend, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if b, ok := g.backends[url]; ok {
		return b, nil
	}
	b, err := btc.NewElectrumBackendWithDialer(url, g.dial)
	if err != nil {
		return nil, err
	}
	g.backends[url] = b
	return b, nil
}

// ServeHTTP dispatches /v1/btc/{balance,utxos,history,fee,send}. The Electrum
// server URL comes from the X-Skywire-Btc-Backend header (the wallet shim sets
// it from localStorage['skywire-btc-backend']); addresses come from ?addrs=.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	url := strings.TrimSpace(r.Header.Get("X-Skywire-Btc-Backend"))
	if url == "" {
		writeErr(w, http.StatusBadGateway, "no BTC backend configured (set a BTC electrum server in wallet settings)")
		return
	}
	b, err := g.backendFor(url)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "connect electrum: "+err.Error())
		return
	}

	path := strings.TrimSuffix(r.URL.Path, "/")
	if i := strings.Index(path, "/v1/btc/"); i >= 0 {
		path = path[i:]
	}
	switch {
	case path == "/v1/btc/balance" && r.Method == http.MethodGet:
		g.balance(w, r, b)
	case path == "/v1/btc/utxos" && r.Method == http.MethodGet:
		g.utxos(w, r, b)
	case path == "/v1/btc/history" && r.Method == http.MethodGet:
		g.history(w, r, b)
	case path == "/v1/btc/fee" && r.Method == http.MethodGet:
		g.fee(w, r, b)
	case path == "/v1/btc/send" && r.Method == http.MethodPost:
		g.send(w, r, b)
	default:
		writeErr(w, http.StatusNotFound, "unknown BTC endpoint "+path)
	}
}

func (g *Gateway) balance(w http.ResponseWriter, r *http.Request, b btc.Backend) {
	addrs := addrsParam(r)
	if len(addrs) == 0 {
		writeErr(w, http.StatusBadRequest, "addrs parameter required")
		return
	}
	bals, err := b.GetBalance(addrs)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var total int64
	addrBalances := make(map[string]map[string]map[string]int64, len(bals))
	for addr, bal := range bals {
		total += bal.Confirmed
		addrBalances[addr] = map[string]map[string]int64{"confirmed": {"coins": bal.Confirmed}}
	}
	writeJSON(w, map[string]interface{}{
		"confirmed": map[string]int64{"coins": total},
		"predicted": map[string]int64{"coins": total},
		"addresses": addrBalances,
	})
}

func (g *Gateway) utxos(w http.ResponseWriter, r *http.Request, b btc.Backend) {
	addrs := addrsParam(r)
	if len(addrs) == 0 {
		writeErr(w, http.StatusBadRequest, "addrs parameter required")
		return
	}
	u, err := b.ListUnspent(addrs)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, u)
}

func (g *Gateway) history(w http.ResponseWriter, r *http.Request, b btc.Backend) {
	addrs := addrsParam(r)
	if len(addrs) == 0 {
		writeErr(w, http.StatusBadRequest, "addrs parameter required")
		return
	}
	h, err := b.GetHistory(addrs)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, h)
}

func (g *Gateway) fee(w http.ResponseWriter, r *http.Request, b btc.Backend) {
	blocks := 6
	if v := r.URL.Query().Get("blocks"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			blocks = n
		}
	}
	spb, err := b.EstimateFee(blocks)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]interface{}{"sat_per_byte": spb, "blocks": blocks})
}

// send broadcasts a client-signed raw transaction — the in-browser wallet signs
// with skycoin-lite WASM (keys never leave the browser), then broadcasts here.
// Body: {"rawtx":"<hex>"}.
func (g *Gateway) send(w http.ResponseWriter, r *http.Request, b btc.Backend) {
	var body struct {
		RawTx string `json:"rawtx"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.RawTx == "" {
		writeErr(w, http.StatusBadRequest, "rawtx required")
		return
	}
	txid, err := b.BroadcastTransaction(body.RawTx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"txid": txid})
}

func addrsParam(r *http.Request) []string {
	p := strings.TrimSpace(r.URL.Query().Get("addrs"))
	if p == "" {
		return nil
	}
	return strings.Split(p, ",")
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck
}
