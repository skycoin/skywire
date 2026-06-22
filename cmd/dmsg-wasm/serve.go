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
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"sync"
	"time"
)

type tab struct {
	id     string
	events chan string // SSE command frames queued for this tab
	mu     sync.Mutex
	logs   []string
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

func main() {
	dir := flag.String("dir", "build/dmsg-wasm", "directory to serve")
	addr := flag.String("addr", ":8085", "listen address")
	flag.Parse()
	_ = mime.AddExtensionType(".wasm", "application/wasm")

	mux := http.NewServeMux()

	// SSE: a tab subscribes here to receive commands.
	mux.HandleFunc("/ctl/events", func(w http.ResponseWriter, r *http.Request) {
		t := getTab(r.URL.Query().Get("tab"))
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flush", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fmt.Fprint(w, ": connected\n\n")
		fl.Flush()
		log.Printf("tab connected: %s", t.id)
		for {
			select {
			case msg := <-t.events:
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

	// List connected tabs.
	mux.HandleFunc("/ctl/tabs", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ids := make([]string, 0, len(tabs))
		for id := range tabs {
			ids = append(ids, id)
		}
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(ids)
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
		mu.Lock()
		seq++
		c.ID = fmt.Sprintf("c%d", seq)
		ch := make(chan result, 1)
		pending[c.ID] = ch
		mu.Unlock()
		defer func() { mu.Lock(); delete(pending, c.ID); mu.Unlock() }()

		b, _ := json.Marshal(c)
		select {
		case t.events <- string(b):
		case <-time.After(5 * time.Second):
			http.Error(w, "tab not receiving", 504)
			return
		}
		select {
		case res := <-ch:
			_ = json.NewEncoder(w).Encode(res)
		case <-time.After(40 * time.Second):
			http.Error(w, "command timeout", 504)
		}
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
