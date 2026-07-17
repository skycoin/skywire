//go:build ignore

// Package main cmd/dmsg-wasm/serve.go c1-net-dmsg
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
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"mime"
	"net/http"

	"github.com/skycoin/skywire/pkg/wasmhv/ctlbridge"
)

func main() {
	dir := flag.String("dir", "build/dmsg-wasm", "directory to serve")
	addr := flag.String("addr", ":8085", "listen address")
	flag.Parse()
	_ = mime.AddExtensionType(".wasm", "application/wasm")

	mux := http.NewServeMux()

	// The /ctl/* control surface (SSE events, result, log, clear, tabs, rpc, cmd)
	// is shared with `cli hv serve --harness` — see pkg/wasmhv/ctlbridge.
	br := ctlbridge.New(log.Printf)
	br.Register(mux)

	// Mesh test: boot two wasm-visor tabs, form a WebRTC transport from a→b, and
	// verify both ends see a transport. One curl proves two browser visors meshing.
	// Harness-only (scripted sequence over the shared bridge).
	//
	//	curl -s 'localhost:8085/ctl/meshtest?a=<tabA>&b=<tabB>&seedpk=<pk>&seedws=<ws>&disc=<dmsg://pk:80>'
	mux.HandleFunc("/ctl/meshtest", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		a, b := q.Get("a"), q.Get("b")
		seedpk, seedws, disc := q.Get("seedpk"), q.Get("seedws"), q.Get("disc")
		var steps []map[string]interface{}
		record := func(name string, res ctlbridge.Result, err error) bool {
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
		bootA, err := br.SendCmd(a, "boot", "", seedpk, seedws, disc)
		if !record("boot:a", bootA, err) {
			finish(false, "tab a failed to boot")
			return
		}
		bootB, err := br.SendCmd(b, "boot", "", seedpk, seedws, disc)
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
		dial, err := br.SendCmd(a, "dialTransport", pkB, "webrtc")
		if !record("dialTransport:a→b(webrtc)", dial, err) {
			finish(false, "WebRTC transport a→b failed")
			return
		}

		// 3. both ends should now report >=1 transport.
		statA, errA := br.SendCmd(a, "status")
		record("status:a", statA, errA)
		statB, errB := br.SendCmd(b, "status")
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
