// Package clihv cmd/skywire-cli/commands/hv/serve.go c4-vis-cli
package clihv

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	serveAddr         string
	serveHarness      bool
	serveHelpTerminal bool
	serveDocsPort     int
	serveTLS          bool
	serveTLSCert      string
	serveTLSKey       string
	servePassword     string
	serveWallet       bool
	serveBrowseSuffix string
	serveBrowseOrigin string
	serveVOrigin      string
	serveExecWasm     string
)

func init() {
	serveCmd.Flags().StringVarP(&serveAddr, "addr", "a", ":7999", "HTTP listen address")
	serveCmd.Flags().BoolVar(&serveHarness, "harness", false, "mount the /ctl/* operator control bridge (drive the in-tab visor from a shell); DEV ONLY — never expose publicly")
	serveCmd.Flags().BoolVar(&serveHelpTerminal, "desk-help-terminal", false, "open a desk terminal that has already run 'skywire --help' — costs a whole extra Go/wasm runtime of the full binary, and that memory is never returned")
	serveCmd.Flags().IntVar(&serveDocsPort, "desk-docs-port", 0, "run 'skywire doc serve' on this desk vnet port (0 = off) — same cost as above")
	serveCmd.Flags().BoolVar(&serveTLS, "tls", false, "serve over HTTPS with a self-signed localhost cert (a real https origin for local testing — wss works, ws:// is mixed-content-blocked exactly as in prod). Accept the browser cert warning once; the cert is persisted across restarts")
	serveCmd.Flags().StringVar(&serveTLSCert, "tls-cert", "", "PEM cert to serve TLS with instead of the self-signed localhost cert — e.g. a locally-trusted *.mesh.localhost cert (mkcert) so real-origin browse iframes load without a per-host accept. Requires --tls-key")
	serveCmd.Flags().StringVar(&serveTLSKey, "tls-key", "", "PEM key paired with --tls-cert")
	serveCmd.Flags().StringVar(&servePassword, "password", "", "gate the served PWA behind an access password (cookie login). Empty = open. Use over --tls / behind TLS so the password isn't sent in clear")
	serveCmd.Flags().BoolVar(&serveWallet, "wallet", true, "serve the bundled skycoin-web wallet at /wallet/ (custody stays browser-side — the host never sees keys). --wallet=false serves a wallet-less PWA")
	serveCmd.Flags().StringVar(&serveBrowseSuffix, "browse-suffix", "", fmt.Sprintf("browse-origin domain suffix for the real-origin browser (leading dot). Empty = .mesh.localhost (local); when --browse-origin is set (hosted mode) and this is empty it defaults to the deployment's browse_origin_suffix (%q from services-config.json)", deployment.Prod.BrowseOriginSuffix))
	serveCmd.Flags().StringVar(&serveBrowseOrigin, "browse-origin", "", "ALSO serve the browse-origin SW bootstrap on this second addr (e.g. 127.0.0.1:7998), for the hosted real-origin browser's B origins. Caddy routes *.<browse-suffix> here; this same process serves V on --addr and B here. Empty = off (V host-routes B on --addr, local mode)")
	serveCmd.Flags().StringVar(&serveVOrigin, "v-origin", "", "the PUBLIC origin of the visor app V that B's bootstrap postMessages to, e.g. https://theskywirenetwork.net. Only needed with --browse-origin behind a proxy; empty = derive from --addr (local)")
	serveCmd.Flags().StringVar(&serveExecWasm, "exec-wasm", "", "path to the full skywire CLI wasm module to serve at /skywire.wasm — the desk host, the tab's visor and the terminal's 'skywire' command (build: GOOS=js GOARCH=wasm go build -tags \"withoutsystray withoutgotop\" -o build/skywire.wasm .). Empty = the module embedded by the two-stage build (make build-embedded; every published binary). Without one, serve refuses to start")
	RootCmd.AddCommand(serveCmd)
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve the standalone wasm-visor desk over HTTP (keyless; reverse-proxy with Caddy)",
	Long: `Serve the keyless standalone wasm-VISOR desk over HTTP.

Serves the desk: the tab as a Linux host, whose terminal runs the ONE skywire
command module (/skywire.wasm — the desk host, the in-tab visor and every
'skywire' command) and whose nested browser opens the hypervisor UI that visor
serves. Everything comes from THIS binary — the module the two-stage build
embeds (make build-embedded; every published binary has it) or --exec-wasm — so
the served build reflects the running skywire version: restart the process
after an update (e.g. wire it to a systemd service that restarts on
auto-update) and put a reverse proxy (Caddy) in front on a subdomain. A plain
source build has no module and serve refuses to start.

The same surface can be hosted BY the visor itself (one binary, one process):
set hypervisor.wasm_serve.addr in the visor config. This command is the
standalone equivalent, sharing the implementation (pkg/visor.ServeWasm).

Keyless: no key is baked in; each visitor's browser mints + persists its own
ephemeral key (localStorage). That is what makes serving from a domain safe — the
page never asks anyone to type a secret key.`,
	Run: func(cmd *cobra.Command, _ []string) {
		// Hosted mode (a browse-origin bootstrap addr is set → Caddy fronts
		// *.<suffix>) with no explicit suffix sources the browse-origin domain
		// from the embedded deployment config instead of hardcoding it, so the
		// domain lives in exactly one place (services-config.json). Local mode
		// (no --browse-origin) keeps the .mesh.localhost default.
		browseSuffix := serveBrowseSuffix
		if browseSuffix == "" && serveBrowseOrigin != "" {
			browseSuffix = deployment.Prod.BrowseOriginSuffix
		}
		if err := visor.ServeWasm(cmd.Context(), visor.WasmServeConfig{
			Addr:             serveAddr,
			TLS:              serveTLS,
			TLSCert:          serveTLSCert,
			TLSKey:           serveTLSKey,
			Harness:          serveHarness,
			DeskHelpTerminal: serveHelpTerminal,
			DeskDocsPort:     serveDocsPort,
			Wallet:           serveWallet,
			Password:         servePassword,
			BrowseSuffix:     browseSuffix,
			BrowseOriginAddr: serveBrowseOrigin,
			VOrigin:          serveVOrigin,
			ExecWasmPath:     serveExecWasm,
		}); err != nil {
			cmd.PrintErrln("serve:", err)
			os.Exit(1)
		}
	},
}
