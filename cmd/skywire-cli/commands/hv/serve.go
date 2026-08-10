// Package clihv cmd/skywire-cli/commands/hv/serve.go c4-vis-cli
package clihv

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/visor"
)

var (
	serveAddr     string
	serveHarness  bool
	serveTLS      bool
	serveTLSCert  string
	serveTLSKey   string
	servePassword string
	serveVariant  string
	serveWallet   bool
)

func init() {
	serveCmd.Flags().StringVarP(&serveAddr, "addr", "a", ":7999", "HTTP listen address")
	serveCmd.Flags().BoolVar(&serveHarness, "harness", false, "mount the /ctl/* operator control bridge (drive the in-tab visor from a shell); DEV ONLY — never expose publicly")
	serveCmd.Flags().BoolVar(&serveTLS, "tls", false, "serve over HTTPS with a self-signed localhost cert (a real https origin for local testing — wss works, ws:// is mixed-content-blocked exactly as in prod). Accept the browser cert warning once; the cert is persisted across restarts")
	serveCmd.Flags().StringVar(&serveTLSCert, "tls-cert", "", "PEM cert to serve TLS with instead of the self-signed localhost cert — e.g. a locally-trusted *.mesh.localhost cert (mkcert) so real-origin browse iframes load without a per-host accept. Requires --tls-key")
	serveCmd.Flags().StringVar(&serveTLSKey, "tls-key", "", "PEM key paired with --tls-cert")
	serveCmd.Flags().StringVar(&servePassword, "password", "", "gate the served PWA behind an access password (cookie login). Empty = open. Use over --tls / behind TLS so the password isn't sent in clear")
	serveCmd.Flags().StringVarP(&serveVariant, "variant", "W", "", "which embedded wasm-visor to serve: 'go' (larger, full crypto/tls+net/http) or 'tinygo' (~4x smaller — better for the PWA, with the documented TinyGo feature gaps). Empty = the build default. A standard-Go build embeds both; a TinyGo build has only 'tinygo'")
	serveCmd.Flags().BoolVar(&serveWallet, "wallet", true, "serve the bundled skycoin-web wallet at /wallet/ (custody stays browser-side — the host never sees keys). --wallet=false serves a wallet-less PWA")
	RootCmd.AddCommand(serveCmd)
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve the standalone wasm-visor over HTTP (keyless; reverse-proxy with Caddy)",
	Long: `Serve the keyless standalone wasm-VISOR over HTTP.

Serves the hypervisor UI + the embedded wasm-visor + hv-boot.js as separate files
(NOT the single-file 'hv gen' output — that inlines override.js and can't serve
Angular's lazy-loaded chunks). hv-boot.js boots the in-tab visor and the Angular
SkywireHttpBackend routes /api to it. Everything is built from THIS binary's
embedded wasm + UI, so the served build reflects the running skywire version —
restart the process after an update (e.g. wire it to a systemd service that
restarts on auto-update) and put a reverse proxy (Caddy) in front on a subdomain.

The same surface can be hosted BY the visor itself (one binary, one process):
set hypervisor.wasm_serve.addr in the visor config. This command is the
standalone equivalent, sharing the implementation (pkg/visor.ServeWasm).

Keyless: no key is baked in; each visitor's browser mints + persists its own
ephemeral key (localStorage). That is what makes serving from a domain safe — the
page never asks anyone to type a secret key.`,
	Run: func(cmd *cobra.Command, _ []string) {
		if err := visor.ServeWasm(cmd.Context(), visor.WasmServeConfig{
			Addr:     serveAddr,
			TLS:      serveTLS,
			TLSCert:  serveTLSCert,
			TLSKey:   serveTLSKey,
			Harness:  serveHarness,
			Wallet:   serveWallet,
			Variant:  serveVariant,
			Password: servePassword,
		}); err != nil {
			cmd.PrintErrln("serve:", err)
			os.Exit(1)
		}
	},
}
