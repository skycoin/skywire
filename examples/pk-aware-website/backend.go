//go:build ignore

// Command backend is a minimal PK-aware website for the
// docs/examples/pk-aware-website demo. It binds to loopback only and reads
// the caller's skywire public key from the X-Skywire-Remote-PK header that
// the visor injects on an `inject_pk` forwarded port (optionally via Caddy).
//
// It shows three behaviors to illustrate per-PK auth:
//   - a known PK (in the allowlist below) gets a personalized "member" page,
//   - any other authenticated PK gets a generic signed-in page,
//   - no PK (clearnet, header stripped) gets the anonymous page.
//
// Run:  go run backend.go      # listens on 127.0.0.1:3000
//
// NOTE: bind loopback only. The header is trustworthy *because* the only way
// to reach this server is through the visor (or the loopback Caddy listener);
// see docs/guides/skynet-website-auth.md.
package main

import (
	"fmt"
	"log"
	"net/http"
)

// allowlist maps a full 66-hex public key to a display name. Replace these
// with the PKs you want to recognize (find yours with `skywire cli visor pk`).
var allowlist = map[string]string{
	// "0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c": "alice",
}

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		pk := r.Header.Get("X-Skywire-Remote-PK")
		transport := r.Header.Get("X-Skywire-Transport")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		switch {
		case pk == "":
			fmt.Fprint(w, page("Anonymous", "You are not connecting over skywire (no verified PK). "+
				"This is the public/clearnet view."))
		case allowlist[pk] != "":
			fmt.Fprint(w, page("Welcome, "+allowlist[pk],
				fmt.Sprintf("Recognized member.<br>PK: <code>%s</code><br>via: %s", pk, transport)))
		default:
			fmt.Fprint(w, page("Signed in",
				fmt.Sprintf("Authenticated skywire peer (not in the allowlist).<br>PK: <code>%s</code><br>via: %s", pk, transport)))
		}
	})

	addr := "127.0.0.1:3000"
	log.Printf("PK-aware demo backend listening on http://%s (loopback only)", addr)
	log.Fatal(http.ListenAndServe(addr, nil)) //nolint:gosec
}

func page(title, body string) string {
	return fmt.Sprintf(`<!doctype html><html><head><meta charset="utf-8"><title>%s</title></head>`+
		`<body style="font-family:sans-serif;max-width:40rem;margin:4rem auto"><h1>%s</h1><p>%s</p></body></html>`,
		title, title, body)
}
