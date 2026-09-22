//go:build !js

// Package cliwisp cmd/skywire-cli/commands/wisp/serve_http.go c4-vis-cli
//
// Serving Wisp the way the protocol defines it: an HTTP listener that upgrades
// to a WebSocket. Its js twin serves the same sessions over the page's virtual
// loopback instead, because in a tab neither half of this file can exist —
// there is no HTTP listener and websocket.Accept is //go:build !js.
package cliwisp

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/wisp"
)

// serveWisp mounts the server on an HTTP listener and blocks.
func serveWisp(cmd *cobra.Command, srv *wisp.Server, egress wisp.Egress) {
	path := wispPath
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	mux := http.NewServeMux()
	// Both spellings: LinuxOnTab points at /wisp, while wisp-js refuses any
	// endpoint URL without a trailing slash. Serving only one of them turns
	// the other client's connect into a 404 that looks like the server is
	// down.
	mux.Handle(path, srv)
	mux.Handle(path+"/", srv)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok") //nolint:errcheck,gosec // health probe
	})

	scheme := "ws"
	if wispTLSCert != "" {
		scheme = "wss"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "serving wisp on %s://%s%s\n", scheme, wispAddr, path)
	fmt.Fprintf(&b, "  egress: %s\n", egress.Describe())
	fmt.Fprintf(&b, "  buffer: %d packets per stream\n", wispBuffer)
	internal.PrintOutput(cmd.Flags(), b.String(), b.String())

	httpSrv := &http.Server{
		Addr:              wispAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	var err error
	if wispTLSCert != "" || wispTLSKey != "" {
		if wispTLSCert == "" || wispTLSKey == "" {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--tls-cert and --tls-key must be given together"))
		}
		err = httpSrv.ListenAndServeTLS(wispTLSCert, wispTLSKey)
	} else {
		err = httpSrv.ListenAndServe()
	}
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
}
