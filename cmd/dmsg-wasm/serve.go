//go:build ignore

// serve.go — static file server + control bridge for the dmsg-wasm harness.
//
// Beyond serving the page, it lets an EXTERNAL operator (curl) drive the browser
// tab(s): each tab connects back over SSE (GET /ctl/events) to receive commands
// and POSTs results (/ctl/result) + log lines (/ctl/log). So two tabs can be
// orchestrated headlessly — e.g. `listen` on one and `webrtcDial` from the other
// — without anyone clicking. No Python, no npm.
//
//	make tinygo-dmsg-wasm && go run cmd/dmsg-wasm/serve.go
//	# open two browser tabs at http://localhost:8085/ , then from a shell:
//	curl -s localhost:8085/ctl/tabs
//	curl -s -XPOST 'localhost:8085/ctl/cmd?tab=<id>' -d '{"action":"connect","args":["","<seedPK>","<seedWS>","<discAddr>"]}'
//	curl -s 'localhost:8085/ctl/log?tab=<id>'
//
// To PROVE two wasm-visor instances meshing in one shot (serving build/wasm-visor):
//
//	go run cmd/dmsg-wasm/serve.go -dir build/wasm-visor
//	# open two tabs, note their ids from /ctl/tabs, then:
//	curl -s 'localhost:8085/ctl/meshtest?a=<tabA>&b=<tabB>&seedpk=<pk>&seedws=<ws>&disc=<dmsg://pk:80>'
//	# → boots both, dials a WebRTC transport a→b, verifies both see a transport.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
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

type command struct {
	ID     string        `json:"id"`
	Action string        `json:"action"`
	Args   []interface{} `json:"args"`
}

type result struct {
	ID    string      `json:"id"`
	OK    bool        `json:"ok"`
	Value interface{} `json:"value"`
	Error string      `json:"error"`
}

var (
	mu      sync.Mutex
	tabs    = map[string]*tab{}
	pending = map[string]chan result{}
	seq     int
)

func getTab(id string) *tab {
	mu.Lock()
	defer mu.Unlock()
	t := tabs[id]
	if t == nil {
		t = &tab{id: id, events: make(chan string, 8)}
		tabs[id] = t
	}
	return t
}

// sendCmd queues one command for a tab over SSE and waits for its result. Shared
// by /ctl/cmd (single command) and /ctl/meshtest (a scripted sequence).
func sendCmd(tabID, action string, args ...interface{}) (result, error) {
	mu.Lock()
	t := tabs[tabID]
	mu.Unlock()
	if t == nil {
		return result{}, fmt.Errorf("no such tab %q (open it first)", tabID)
	}
	if args == nil {
		args = []interface{}{}
	}
	mu.Lock()
	seq++
	c := command{ID: fmt.Sprintf("c%d", seq), Action: action, Args: args}
	ch := make(chan result, 1)
	pending[c.ID] = ch
	mu.Unlock()
	defer func() { mu.Lock(); delete(pending, c.ID); mu.Unlock() }()

	t.mu.Lock()
	evCh := t.events
	t.mu.Unlock()
	b, _ := json.Marshal(c)
	select {
	case evCh <- string(b):
	case <-time.After(5 * time.Second):
		return result{}, fmt.Errorf("tab %q not receiving", tabID)
	}
	select {
	case res := <-ch:
		return res, nil
	case <-time.After(40 * time.Second):
		return result{}, fmt.Errorf("tab %q command %q timeout", tabID, action)
	}
}

func main() {
	dir := flag.String("dir", "build/dmsg-wasm", "directory to serve")
	addr := flag.String("addr", ":8085", "listen address")
	flag.Parse()
	_ = mime.AddExtensionType(".wasm", "application/wasm")

	mux := http.NewServeMux()

	// SSE: a tab subscribes here to receive commands. On reconnect (page reload,
	// same id), install a FRESH events channel as the tab's current one so
	// commands go to the live connection — not a lingering old reader.
	mux.HandleFunc("/ctl/events", func(w http.ResponseWriter, r *http.Request) {
		t := getTab(r.URL.Query().Get("tab"))
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
		fmt.Fprint(w, ": connected\n\n")
		fl.Flush()
		log.Printf("tab connected: %s", t.id)
		for {
			select {
			case msg := <-ch:
				fmt.Fprintf(w, "data: %s\n\n", msg)
				fl.Flush()
			case <-r.Context().Done():
				return
			case <-time.After(15 * time.Second):
				fmt.Fprint(w, ": ping\n\n")
				fl.Flush()
			}
		}
	})

	// A tab posts a command result back here.
	mux.HandleFunc("/ctl/result", func(w http.ResponseWriter, r *http.Request) {
		var res result
		if err := json.NewDecoder(r.Body).Decode(&res); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		mu.Lock()
		ch := pending[res.ID]
		mu.Unlock()
		if ch != nil {
			select {
			case ch <- res:
			default:
			}
		}
		w.WriteHeader(204)
	})

	// A tab streams its log lines here (POST); GET reads them back.
	mux.HandleFunc("/ctl/log", func(w http.ResponseWriter, r *http.Request) {
		t := getTab(r.URL.Query().Get("tab"))
		if r.Method == http.MethodPost {
			b, _ := io.ReadAll(r.Body)
			t.mu.Lock()
			t.logs = append(t.logs, string(b))
			t.mu.Unlock()
			w.WriteHeader(204)
			return
		}
		t.mu.Lock()
		out := append([]string(nil), t.logs...)
		t.mu.Unlock()
		for _, l := range out {
			fmt.Fprintln(w, l)
		}
	})

	// Clear a tab's buffered logs (so each test reads fresh output).
	mux.HandleFunc("/ctl/clear", func(w http.ResponseWriter, r *http.Request) {
		t := getTab(r.URL.Query().Get("tab"))
		t.mu.Lock()
		t.logs = nil
		t.mu.Unlock()
		w.WriteHeader(204)
	})

	// List connected tabs, each with its RPC bridge address (if connected) so a
	// shell can do: skywire --rpc <rpc> visor info
	mux.HandleFunc("/ctl/tabs", func(w http.ResponseWriter, r *http.Request) {
		type tabInfo struct {
			ID  string `json:"id"`
			RPC string `json:"rpc,omitempty"`
		}
		mu.Lock()
		out := make([]tabInfo, 0, len(tabs))
		for id, t := range tabs {
			t.mu.Lock()
			out = append(out, tabInfo{ID: id, RPC: t.rpcAddr})
			t.mu.Unlock()
		}
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(out)
	})

	// RPC bridge: a wasm-visor tab dials this WebSocket and serves its visor RPC
	// over it; we front it with a local 127.0.0.1:<port> that `skywire cli --rpc`
	// targets. One bridge per tab; closed when the tab disconnects. See
	// pkg/wasmrpc.
	mux.HandleFunc("/ctl/rpc", func(w http.ResponseWriter, r *http.Request) {
		t := getTab(r.URL.Query().Get("tab"))
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			log.Printf("rpc bridge accept (%s): %v", t.id, err)
			return
		}
		c.SetReadLimit(-1)
		conn := websocket.NetConn(context.Background(), c, websocket.MessageBinary)
		bridge, err := wasmrpc.NewBridge(conn, 0, func(m string) { log.Printf("[rpc %s] %s", t.id, m) })
		if err != nil {
			log.Printf("rpc bridge (%s): %v", t.id, err)
			_ = c.Close(websocket.StatusInternalError, "bridge setup failed")
			return
		}
		t.mu.Lock()
		t.rpcAddr = bridge.Addr()
		t.mu.Unlock()
		log.Printf("tab %s RPC bridge UP at %s — drive it: skywire --rpc %s visor info", t.id, bridge.Addr(), bridge.Addr())
		<-r.Context().Done()
		_ = bridge.Close()
		t.mu.Lock()
		t.rpcAddr = ""
		t.mu.Unlock()
		log.Printf("tab %s RPC bridge closed", t.id)
	})

	// Operator drives a tab: queue a command, wait for its result.
	mux.HandleFunc("/ctl/cmd", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("tab")
		mu.Lock()
		t := tabs[id]
		mu.Unlock()
		if t == nil {
			http.Error(w, "no such tab (open it first)", 404)
			return
		}
		var c command
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		res, err := sendCmd(id, c.Action, c.Args...)
		if err != nil {
			http.Error(w, err.Error(), 504)
			return
		}
		_ = json.NewEncoder(w).Encode(res)
	})

	// Mesh test: boot two wasm-visor tabs, form a WebRTC transport from a→b, and
	// verify both ends see a transport. One curl proves two browser visors meshing.
	//
	//	curl -s 'localhost:8085/ctl/meshtest?a=<tabA>&b=<tabB>&seedpk=<pk>&seedws=<ws>&disc=<dmsg://pk:80>'
	mux.HandleFunc("/ctl/meshtest", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		a, b := q.Get("a"), q.Get("b")
		seedpk, seedws, disc := q.Get("seedpk"), q.Get("seedws"), q.Get("disc")
		var steps []map[string]interface{}
		record := func(name string, res result, err error) bool {
			s := map[string]interface{}{"step": name}
			if err != nil {
				s["error"] = err.Error()
			} else {
				s["ok"] = res.OK
				s["value"] = res.Value
				if res.Error != "" {
					s["error"] = res.Error
				}
			}
			steps = append(steps, s)
			return err == nil && res.OK
		}
		finish := func(pass bool, msg string) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"pass": pass, "summary": msg, "steps": steps})
		}

		// 1. boot both tabs (ephemeral identities).
		bootA, err := sendCmd(a, "boot", "", seedpk, seedws, disc)
		if !record("boot:a", bootA, err) {
			finish(false, "tab a failed to boot")
			return
		}
		bootB, err := sendCmd(b, "boot", "", seedpk, seedws, disc)
		if !record("boot:b", bootB, err) {
			finish(false, "tab b failed to boot")
			return
		}
		pkB, _ := bootB.Value.(string)
		if pkB == "" {
			finish(false, "tab b boot did not return a public key")
			return
		}

		// 2. dial a WebRTC transport a → b (signaling rides dmsg).
		dial, err := sendCmd(a, "dialTransport", pkB, "webrtc")
		if !record("dialTransport:a→b(webrtc)", dial, err) {
			finish(false, "WebRTC transport a→b failed")
			return
		}

		// 3. both ends should now report >=1 transport.
		statA, errA := sendCmd(a, "status")
		record("status:a", statA, errA)
		statB, errB := sendCmd(b, "status")
		record("status:b", statB, errB)
		ta := transportCount(statA.Value)
		tb := transportCount(statB.Value)
		pass := ta >= 1 && tb >= 1
		finish(pass, fmt.Sprintf("transports: a=%d b=%d (want >=1 each)", ta, tb))
	})

	// Static files with no-cache headers, so a reload always fetches the freshly
	// built wasm/page (lets the operator drive reloads without cache-busting).
	fs := http.FileServer(http.Dir(*dir))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/favicon.ico" {
			w.WriteHeader(204)
			return
		}
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
		fs.ServeHTTP(w, r)
	})

	log.Printf("serving %s at http://localhost%s/ (control bridge at /ctl/*)", *dir, *addr)
	log.Fatal(http.ListenAndServe(*addr, mux)) //nolint:gosec
}

// transportCount extracts the "transports" count from a wasm-visor status value
// (skywireVisor.status() → {..., transports: N}). JSON numbers decode to float64.
func transportCount(v interface{}) int {
	m, ok := v.(map[string]interface{})
	if !ok {
		return 0
	}
	switch n := m["transports"].(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}
