// Package clihv cmd/skywire-cli/commands/hv/serve.go
package clihv

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/wasmhv"
	"github.com/skycoin/skywire/pkg/wasmhv/ctlbridge"
	"github.com/skycoin/skywire/pkg/wasmhv/wasmbin"
)

var (
	serveAddr    string
	serveHarness bool
)

func init() {
	serveCmd.Flags().StringVarP(&serveAddr, "addr", "a", ":7999", "HTTP listen address")
	serveCmd.Flags().BoolVar(&serveHarness, "harness", false, "mount the /ctl/* operator control bridge (drive the in-tab visor from a shell); DEV ONLY — never expose publicly")
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

Keyless: no key is baked in; each visitor's browser mints + persists its own
ephemeral key (localStorage). That is what makes serving from a domain safe — the
page never asks anyone to type a secret key.`,
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
		indexB, err := fs.ReadFile(uiFS, "index.html")
		if err != nil {
			cmd.PrintErrln("read index.html:", err)
			os.Exit(1)
		}
		// wasmVer fingerprints the embedded wasm so the page can detect a newer
		// build (after the skywire binary updates) and self-reload — see
		// autoupdate.js. Short SHA-256 prefix is plenty to spot a content change.
		sum := sha256.Sum256(wasm)
		wasmVer := hex.EncodeToString(sum[:])[:16]
		index := injectBoot(indexB, wasmVer, serveHarness)

		mux := http.NewServeMux()
		serveBytes := func(path, ct string, body []byte) {
			mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", ct)
				w.Header().Set("Cache-Control", "public, max-age=300")
				_, _ = w.Write(body) //nolint:errcheck // best-effort write to the client
			})
		}
		// The wasm-visor bootstrap files hv-boot.js fetches by name (resolved against
		// <base href="/">). These are NOT in the Angular FS, so serve them explicitly.
		serveBytes("/wasm-visor.wasm", "application/wasm", wasm)
		serveBytes("/wasm_exec.js", "text/javascript", wasmhv.WasmExecJS)
		serveBytes("/hv-boot.js", "text/javascript", wasmhv.HvBootJS)
		serveBytes("/browse.js", "text/javascript", wasmhv.BrowseJS)
		serveBytes("/autoupdate.js", "text/javascript", wasmhv.AutoUpdateJS)
		// /wasm-version is the build fingerprint autoupdate.js polls; no-cache so a
		// new binary is seen promptly (the wasm itself stays cacheable).
		mux.HandleFunc("/wasm-version", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write([]byte(wasmVer)) //nolint:errcheck // best-effort
		})

		// --harness mounts the same /ctl/* operator control bridge the dev harness
		// (cmd/dmsg-wasm/serve.go) exposes, plus the ctl-bridge.js the page loads to
		// connect to it. This lets a shell drive the in-tab visor (status, hvApi,
		// RPC bridge) against the embedded wasm + current UI. DEV ONLY: it opens an
		// unauthenticated control + eval surface — never enable it behind a public
		// reverse proxy.
		if serveHarness {
			ctlbridge.New(log.Printf).Register(mux)
			serveBytes("/ctl-bridge.js", "text/javascript", wasmhv.CtlBridgeJS)
			cmd.Println("harness: /ctl/* control bridge mounted (DEV ONLY — do not expose publicly)")
		}

		// Everything else is the built Angular UI (incl. lazy chunks, css, fonts,
		// assets). Routing is hash-based (#/...), so the server only ever sees "/"
		// plus real asset paths — no SPA rewrite needed.
		fileServer := http.FileServer(http.FS(uiFS))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" || r.URL.Path == "/index.html" {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "no-cache")
				_, _ = w.Write(index) //nolint:errcheck // best-effort write to the client
				return
			}
			fileServer.ServeHTTP(w, r)
		})

		cmd.Printf("serving standalone wasm-visor (ui + %d-byte wasm + hv-boot) on %s — reverse-proxy this with Caddy\n", len(wasm), serveAddr)
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

// injectBoot inserts the hv-boot.js bootstrap as a classic <script> right after
// <head>, so it runs before Angular's deferred module scripts — CFG.visor is set
// and boot() starts (CFG.ready) before SkywireHttpBackend's first /api call. It
// also loads browse.js + the launcher, which mount the dmsg/skynet browse+host
// overlay (a floating "skynet" button) once the wasm-visor is up.
func injectBoot(index []byte, wasmVer string, harness bool) []byte {
	s := string(index)
	tag := "\n<script src=\"hv-boot.js\"></script>\n" +
		"<script src=\"browse.js\"></script>\n" +
		"<script>" + wasmhv.BrowseLauncherJS + "</script>\n" +
		// Record the build this page loaded, then let autoupdate.js poll for a
		// newer one and self-reload (it only runs here, never in the native UI).
		"<script>window.__SKYWIRE_WASM_VERSION__=" + strconv.Quote(wasmVer) + ";</script>\n" +
		"<script src=\"autoupdate.js\"></script>\n"
	// --harness only: connect the tab to the /ctl/* control bridge so a shell can
	// drive it. Never injected on the public serving path.
	if harness {
		tag += "<script src=\"ctl-bridge.js\"></script>\n"
	}
	lower := strings.ToLower(s)
	if i := strings.Index(lower, "<head"); i >= 0 {
		if j := strings.IndexByte(s[i:], '>'); j >= 0 {
			pos := i + j + 1
			return []byte(s[:pos] + tag + s[pos:])
		}
	}
	return []byte(tag + s)
}
