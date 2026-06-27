// Package ctlbridge is the operator control bridge for the wasm-visor harness.
//
// It lets an EXTERNAL operator (curl, or `skywire cli`) drive the in-browser
// wasm-visor tab(s) without anyone clicking: each tab connects back over SSE
// (GET /ctl/events) to receive commands and POSTs results (/ctl/result) + log
// lines (/ctl/log). Two tabs can be orchestrated headlessly — e.g. `listen` on
// one and dial from the other.
//
// The handler set is shared by two front-ends:
//   - the dev harness  cmd/dmsg-wasm/serve.go  (serves a -dir build)
//   - `skywire cli hv serve --harness`         (serves the embedded wasm + UI)
//
// so the production serving path can opt into the same debug surface behind a
// flag instead of duplicating it. The browser side is ctl-bridge.js (embedded
// in pkg/wasmhv as CtlBridgeJS); inject it onto the page to activate the tab.
package ctlbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/skycoin/skywire/pkg/wasmrpc"
)

type tab struct {
	id      string
	events  chan string // SSE command frames queued for this tab
	mu      sync.Mutex
	logs    []string
	rpcAddr string // local 127.0.0.1:<port> the wasmrpc bridge fronts (when connected)
}

// Command is one command queued for a tab over SSE.
type Command struct {
	ID     string        `json:"id"`
	Action string        `json:"action"`
	Args   []interface{} `json:"args"`
}

// Result is a tab's reply to a Command.
type Result struct {
	ID    string      `json:"id"`
	OK    bool        `json:"ok"`
	Value interface{} `json:"value"`
	Error string      `json:"error"`
}

// Bridge holds the live tab registry + pending-command table. Safe for
// concurrent use. Create with New, wire with Register.
type Bridge struct {
	logf    func(string, ...interface{})
	mu      sync.Mutex
	tabs    map[string]*tab
	pending map[string]chan Result
	seq     int
}

// New returns a ready Bridge. logf is used for connection/bridge diagnostics
// (pass log.Printf, or nil to discard).
func New(logf func(string, ...interface{})) *Bridge {
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	return &Bridge{
		logf:    logf,
		tabs:    map[string]*tab{},
		pending: map[string]chan Result{},
	}
}

func (b *Bridge) getTab(id string) *tab {
	b.mu.Lock()
	defer b.mu.Unlock()
	t := b.tabs[id]
	if t == nil {
		t = &tab{id: id, events: make(chan string, 8)}
		b.tabs[id] = t
	}
	return t
}

// SendCmd queues one command for a tab over SSE and waits for its result.
// Shared by the /ctl/cmd handler and scripted sequences (e.g. meshtest).
func (b *Bridge) SendCmd(tabID, action string, args ...interface{}) (Result, error) {
	b.mu.Lock()
	t := b.tabs[tabID]
	b.mu.Unlock()
	if t == nil {
		return Result{}, fmt.Errorf("no such tab %q (open it first)", tabID)
	}
	if args == nil {
		args = []interface{}{}
	}
	b.mu.Lock()
	b.seq++
	c := Command{ID: fmt.Sprintf("c%d", b.seq), Action: action, Args: args}
	ch := make(chan Result, 1)
	b.pending[c.ID] = ch
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.pending, c.ID); b.mu.Unlock() }()

	t.mu.Lock()
	evCh := t.events
	t.mu.Unlock()
	raw, _ := json.Marshal(c) //nolint:errcheck // marshaling a Command never fails
	select {
	case evCh <- string(raw):
	case <-time.After(5 * time.Second):
		return Result{}, fmt.Errorf("tab %q not receiving", tabID)
	}
	select {
	case res := <-ch:
		return res, nil
	case <-time.After(40 * time.Second):
		return Result{}, fmt.Errorf("tab %q command %q timeout", tabID, action)
	}
}

// Register mounts the /ctl/* handlers on mux.
func (b *Bridge) Register(mux *http.ServeMux) {
	mux.HandleFunc("/ctl/events", b.handleEvents)
	mux.HandleFunc("/ctl/result", b.handleResult)
	mux.HandleFunc("/ctl/log", b.handleLog)
	mux.HandleFunc("/ctl/clear", b.handleClear)
	mux.HandleFunc("/ctl/tabs", b.handleTabs)
	mux.HandleFunc("/ctl/rpc", b.handleRPC)
	mux.HandleFunc("/ctl/cmd", b.handleCmd)
}

// handleEvents — SSE: a tab subscribes here to receive commands. On reconnect
// (page reload, same id), install a FRESH events channel as the tab's current
// one so commands go to the live connection — not a lingering old reader.
func (b *Bridge) handleEvents(w http.ResponseWriter, r *http.Request) {
	t := b.getTab(r.URL.Query().Get("tab"))
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no flush", 500)
		return
	}
	ch := make(chan string, 8)
	t.mu.Lock()
	t.events = ch
	t.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprint(w, ": connected\n\n") //nolint:errcheck // best-effort SSE write
	fl.Flush()
	b.logf("tab connected: %s", t.id)
	for {
		select {
		case msg := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", msg) //nolint:errcheck // best-effort SSE write
			fl.Flush()
		case <-r.Context().Done():
			return
		case <-time.After(15 * time.Second):
			fmt.Fprint(w, ": ping\n\n") //nolint:errcheck // best-effort SSE write
			fl.Flush()
		}
	}
}

// handleResult — a tab posts a command result back here.
func (b *Bridge) handleResult(w http.ResponseWriter, r *http.Request) {
	var res Result
	if err := json.NewDecoder(r.Body).Decode(&res); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	b.mu.Lock()
	ch := b.pending[res.ID]
	b.mu.Unlock()
	if ch != nil {
		select {
		case ch <- res:
		default:
		}
	}
	w.WriteHeader(204)
}

// handleLog — a tab streams its log lines here (POST); GET reads them back.
func (b *Bridge) handleLog(w http.ResponseWriter, r *http.Request) {
	t := b.getTab(r.URL.Query().Get("tab"))
	if r.Method == http.MethodPost {
		raw, _ := io.ReadAll(r.Body) //nolint:errcheck // best-effort read of a log line
		t.mu.Lock()
		t.logs = append(t.logs, string(raw))
		t.mu.Unlock()
		w.WriteHeader(204)
		return
	}
	t.mu.Lock()
	out := append([]string(nil), t.logs...)
	t.mu.Unlock()
	for _, l := range out {
		fmt.Fprintln(w, l) //nolint:errcheck // best-effort write to the client
	}
}

// handleClear — clear a tab's buffered logs (so each test reads fresh output).
func (b *Bridge) handleClear(w http.ResponseWriter, r *http.Request) {
	t := b.getTab(r.URL.Query().Get("tab"))
	t.mu.Lock()
	t.logs = nil
	t.mu.Unlock()
	w.WriteHeader(204)
}

// handleTabs — list connected tabs, each with its RPC bridge address (if
// connected) so a shell can do: skywire --rpc <rpc> visor info
func (b *Bridge) handleTabs(w http.ResponseWriter, _ *http.Request) {
	type tabInfo struct {
		ID  string `json:"id"`
		RPC string `json:"rpc,omitempty"`
	}
	b.mu.Lock()
	out := make([]tabInfo, 0, len(b.tabs))
	for id, t := range b.tabs {
		t.mu.Lock()
		out = append(out, tabInfo{ID: id, RPC: t.rpcAddr})
		t.mu.Unlock()
	}
	b.mu.Unlock()
	_ = json.NewEncoder(w).Encode(out) //nolint:errcheck // best-effort write to the client
}

// handleRPC — RPC bridge: a wasm-visor tab dials this WebSocket and serves its
// visor RPC over it; we front it with a local 127.0.0.1:<port> that
// `skywire cli --rpc` targets. One bridge per tab; closed when the tab
// disconnects. See pkg/wasmrpc.
func (b *Bridge) handleRPC(w http.ResponseWriter, r *http.Request) {
	t := b.getTab(r.URL.Query().Get("tab"))
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		b.logf("rpc bridge accept (%s): %v", t.id, err)
		return
	}
	c.SetReadLimit(-1)
	conn := websocket.NetConn(context.Background(), c, websocket.MessageBinary)
	bridge, err := wasmrpc.NewBridge(conn, 0, func(m string) { b.logf("[rpc %s] %s", t.id, m) })
	if err != nil {
		b.logf("rpc bridge (%s): %v", t.id, err)
		_ = c.Close(websocket.StatusInternalError, "bridge setup failed") //nolint:errcheck // best-effort close
		return
	}
	t.mu.Lock()
	t.rpcAddr = bridge.Addr()
	t.mu.Unlock()
	b.logf("tab %s RPC bridge UP at %s — drive it: skywire --rpc %s visor info", t.id, bridge.Addr(), bridge.Addr())
	<-r.Context().Done()
	_ = bridge.Close() //nolint:errcheck // best-effort close on tab disconnect
	t.mu.Lock()
	t.rpcAddr = ""
	t.mu.Unlock()
	b.logf("tab %s RPC bridge closed", t.id)
}

// handleCmd — operator drives a tab: queue a command, wait for its result.
func (b *Bridge) handleCmd(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("tab")
	b.mu.Lock()
	t := b.tabs[id]
	b.mu.Unlock()
	if t == nil {
		http.Error(w, "no such tab (open it first)", 404)
		return
	}
	var c Command
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	res, err := b.SendCmd(id, c.Action, c.Args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusGatewayTimeout)
		return
	}
	_ = json.NewEncoder(w).Encode(res) //nolint:errcheck // best-effort write to the client
}
