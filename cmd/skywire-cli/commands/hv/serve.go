// Package clihv cmd/skywire-cli/commands/hv/serve.go
package clihv

import (
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/wasmhv"
	"github.com/skycoin/skywire/pkg/wasmhv/wasmbin"
)

var (
	serveAddr   string
	serveSeedPK string
	serveSeedWS string
	serveDisc   string
)

func init() {
	serveCmd.Flags().StringVarP(&serveAddr, "addr", "a", ":7999", "HTTP listen address")
	serveCmd.Flags().StringVar(&serveSeedPK, "seed-pk", "", "seed dmsg server PK the browser connects to first")
	serveCmd.Flags().StringVar(&serveSeedWS, "seed-ws", "", "seed dmsg server ws:// URL")
	serveCmd.Flags().StringVar(&serveDisc, "disc", "", "dmsg discovery (dmsg://<pk>:80)")
	RootCmd.AddCommand(serveCmd)
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve the standalone wasm-visor over HTTP (keyless; reverse-proxy with Caddy)",
	Long: `Serve the keyless standalone wasm-VISOR as a single self-contained page over HTTP.

The page is built ONCE at startup from THIS binary's embedded wasm-visor + hypervisor
UI, so it always reflects the running skywire version. Restart the process to serve a
newer build — e.g. wire this to a systemd service that restarts on auto-update, and put
Caddy (or any reverse proxy) in front of it on a subdomain.

Keyless: no key is baked in; each visitor's browser mints + persists its own ephemeral
key (localStorage). That is what makes serving from a domain safe — the page never asks
anyone to type a secret key. (For a key-bearing or viewer build, use 'hv gen' and open
it from file://; never serve a key-bearing build from a domain.)`,
	Run: func(cmd *cobra.Command, _ []string) {
		if !wasmbin.Embedded() {
			cmd.PrintErrln("no embedded wasm-visor: rebuild skywire after `make embed-wasm-visor`")
			os.Exit(1)
		}
		wasm, err := wasmbin.Get()
		if err != nil {
			cmd.PrintErrln("embedded wasm-visor:", err)
			os.Exit(1)
		}
		uiFS, err := visor.HypervisorUIFS()
		if err != nil {
			cmd.PrintErrln("hypervisor UI assets:", err)
			os.Exit(1)
		}
		// Keyless standalone wasm-VISOR: visors run in the tab; each visitor's
		// browser mints its own ephemeral key. Uses the embedded std-Go wasm-visor
		// (Go's wasm_exec.js, also embedded).
		cfg := wasmhv.StandaloneConfig{
			Visor:      true,
			Standalone: true,
			SeedPK:     serveSeedPK,
			SeedWS:     serveSeedWS,
			Disc:       serveDisc,
		}
		html, err := wasmhv.GenerateStandalone(uiFS, wasmhv.WasmExecJS, wasm, wasmhv.OverrideJS, cfg)
		if err != nil {
			cmd.PrintErrln("generate:", err)
			os.Exit(1)
		}

		// Single self-contained page: serve the same bytes for every path (the SPA
		// router runs client-side; there are no separate asset requests).
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "public, max-age=300")
			_, _ = w.Write(html) //nolint:errcheck // best-effort write to the client
		})

		cmd.Printf("serving standalone wasm-visor (%d bytes) on %s — reverse-proxy this with Caddy\n", len(html), serveAddr)
		srv := &http.Server{
			Addr:              serveAddr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
		}
		if err := srv.ListenAndServe(); err != nil {
			cmd.PrintErrln("serve:", err)
			os.Exit(1)
		}
	},
}
