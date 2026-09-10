// Package visor pkg/visor/wasmserve.go c3-vis-core
//
// ServeWasm mounts the standalone wasm-visor PWA (the same surface as
// `skywire cli hv serve`) as an HTTP server. Extracted here so BOTH the
// CLI command and the visor daemon can serve it from the one binary /
// one process: `skywire visor --wasm-serve :8443 [--wasm-serve-tls]
// [--wasm-serve-harness]` brings the harness up alongside the visor, and
// the operator's rebuild-restart loop re-embeds + re-serves the latest
// wasm every restart (no separate process to remember).
package visor

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"html"
	"io/fs"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/0magnet/realorigin"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/wallet/coins"
	"github.com/skycoin/skywire/pkg/wasmhv"
	"github.com/skycoin/skywire/pkg/wasmhv/ctlbridge"
	"github.com/skycoin/skywire/pkg/wasmhv/execwasm"
)

// WasmServeConfig configures ServeWasm. Mirrors the `hv serve` flags.
type WasmServeConfig struct {
	Addr     string // HTTP listen address (e.g. ":8443")
	TLS      bool   // serve HTTPS (self-signed localhost cert, or TLSCert/TLSKey if set)
	TLSCert  string // optional cert file (PEM) — a locally-trusted *.mesh.localhost cert (mkcert) so B-origin iframes load without a per-host accept; empty = built-in self-signed
	TLSKey   string // optional key file (PEM), paired with TLSCert
	Harness  bool   // mount the /ctl/* operator control bridge (DEV ONLY)
	Wallet   bool   // serve the bundled skycoin-web wallet at /wallet/
	Password string // optional access-password gate
	// BrowseSuffix is the browse-origin domain suffix (leading dot), e.g.
	// ".mesh.localhost" (local) or ".haltingstate.net" (hosted). Empty →
	// ".mesh.localhost".
	BrowseSuffix string
	// BrowseOriginAddr, when set, ALSO serves the browse-origin SW bootstrap on a
	// SECOND listener at this address (the same process serves V on Addr and the
	// isolated browse origins B here). A hosted deploy fronts this with Caddy on
	// *.<BrowseSuffix>. Empty = off (local mode host-routes B on the V listener).
	BrowseOriginAddr string
	// VOrigin is the PUBLIC origin of the visor app V (e.g.
	// "https://theskywirenetwork.net") that B's bootstrap postMessages to — used
	// only with BrowseOriginAddr behind a proxy. Empty = derive from Addr (local).
	VOrigin string
	// ExecWasmPath, when set, serves the file at /skywire.wasm: the FULL
	// skywire CLI compiled for GOOS=js (see docs/design) — the desk host, the
	// tab's visor and every command the page's terminal runs against the
	// shared in-memory filesystem (bottle jsfs.js + browseui skywire-exec.js).
	// Empty = the module embedded by the two-stage build (pkg/wasmhv/execwasm),
	// else the package location on disk. There is no page without it: ServeWasm
	// refuses to start rather than serve a desk with nothing to run. Build it with
	//   GOOS=js GOARCH=wasm go build -tags "withoutsystray withoutgotop" \
	//     -trimpath -ldflags "-s -w" -o build/skywire.wasm .
	ExecWasmPath string
	// DeskHelpTerminal opens a second terminal that has already run
	// `skywire --help`. Off by default: it costs a whole extra Go/wasm runtime
	// of the full binary, permanently — see deskWasmBootOpts.
	DeskHelpTerminal bool
	// DeskDocsPort runs `skywire doc serve` on this virtual-loopback port.
	// 0 (default) = off, for the same reason.
	DeskDocsPort int
	Log          *logging.Logger // nil → package default
}

// ServeWasm builds the standalone wasm-visor handler and serves it on
// cfg.Addr until ctx is canceled. Blocking — run it in a goroutine.
// Returns an error instead of exiting so the caller (visor daemon or CLI)
// decides how to react (the daemon logs and continues; the CLI fatals).
func ServeWasm(ctx context.Context, cfg WasmServeConfig) error {
	log := cfg.Log
	if log == nil {
		log = logging.MustGetLogger("wasm-serve")
	}
	// The ONE module everything served here runs out of: the desk host, the
	// tab's visor, every command the terminal executes. Explicit path, else
	// the module embedded by the two-stage build, else the package location
	// on disk (execModuleSource). Without one there is nothing to serve — a
	// plain source build stops here rather than put up a desk with no host.
	execWasmPath, haveExecWasm := execModuleSource(cfg.ExecWasmPath)
	if !haveExecWasm {
		return fmt.Errorf("no skywire command module embedded: run `make build-embedded` or pass --exec-wasm")
	}
	cfg.ExecWasmPath = execWasmPath
	uiFS, err := HypervisorUIFS()
	if err != nil {
		return fmt.Errorf("hypervisor UI assets: %w", err)
	}
	// wasmVer fingerprints the served BUILD (binary version + the client JS
	// below) so the page self-reloads on any newer deploy. The command module
	// is not hashed here: servedVersion folds its stamp in per request, since
	// the on-disk one is rebuilt under a running server.
	vh := sha256.New()
	vh.Write([]byte(buildinfo.Version()))
	// Fold the served client JS into the fingerprint too, so a rebuild that
	// only touches desk-boot/browse/autoupdate still bumps /wasm-version (and
	// the ETag below) and the page picks up the new assets.
	vh.Write(wasmhv.DeskBootJS())
	vh.Write(wasmhv.ExecWorkerJS())
	vh.Write(wasmhv.BrowseJS)
	vh.Write(wasmhv.AutoUpdateJS)
	vh.Write(realorigin.ServiceWorkerJS())
	vh.Write(wasmhv.BrowseBootstrapHTML)
	vh.Write(realorigin.ResponderJS())
	vh.Write(wasmhv.BrowseTransportJS)
	wasmVer := hex.EncodeToString(vh.Sum(nil))[:16]
	// etag is the fingerprint as an HTTP entity tag. The version-bound assets
	// below are served no-cache + ETag so every load REVALIDATES: unchanged →
	// cheap 304, a rebuild-restart → the fresh bytes are fetched immediately.
	// (max-age caching defeats the rebuild-restart loop: the browser would keep
	// serving the previous 9.5MB blob from its HTTP cache while /wasm-version
	// already reports the new hash.)
	etag := `"` + wasmVer + `"`

	// Real-origin browse origin (RFC §4a/§4b): a mesh site loads from an isolated
	// origin B (<pkslug>.mesh.localhost) on THIS listener (host-routed below).
	// B and V share this listener + TLS cert.
	browseScheme := "http"
	if cfg.TLS {
		browseScheme = "https"
	}
	bport := ""
	if _, p, e := net.SplitHostPort(cfg.Addr); e == nil {
		bport = p
	}
	vHostPort := "localhost"
	if bport != "" {
		vHostPort = "localhost:" + bport
	}
	// Browse-origin domain suffix. Empty = local single-listener default.
	suffix := cfg.BrowseSuffix
	if suffix == "" {
		suffix = ".mesh.localhost"
	}
	browseBootstrap := bytes.ReplaceAll(wasmhv.BrowseBootstrapHTML, []byte("__APP_ORIGIN__"), []byte(browseScheme+"://"+vHostPort))
	browseBootstrap = bytes.ReplaceAll(browseBootstrap, []byte("__SUFFIX__"), []byte(suffix))

	// Hosted two-port mode: when BrowseOriginAddr is set, this SAME process ALSO
	// runs the browse-origin bootstrap server on that second listener (Caddy fronts
	// it on *.<suffix>). V (this listener) and B (that one) are separate origins;
	// co-locating them in one process keeps the deploy to a single unit. Best-effort
	// — a bind failure is logged, not fatal to serving V.
	if cfg.BrowseOriginAddr != "" {
		vOrigin := cfg.VOrigin
		if vOrigin == "" {
			vOrigin = browseScheme + "://" + vHostPort
		}
		go func() {
			if err := ServeBrowseOrigin(ctx, BrowseOriginConfig{
				Addr:    cfg.BrowseOriginAddr,
				VOrigin: vOrigin,
				Suffix:  suffix,
				TLS:     cfg.TLS,
				TLSCert: cfg.TLSCert,
				TLSKey:  cfg.TLSKey,
				Log:     log,
			}); err != nil {
				log.WithError(err).Error("browse-origin bootstrap server failed")
			}
		}()
	}

	mux := http.NewServeMux()
	serveBytes := func(path, ct string, body []byte) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", ct)
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("ETag", etag)
			if r.Header.Get("If-None-Match") == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			_, _ = w.Write(body) //nolint:errcheck
		})
	}
	// Go's loader for the one module. It is std-Go's wasm_exec.js because the
	// module is a std-Go build; the loader and the module are a pair.
	serveBytes("/wasm_exec.js", "text/javascript", wasmhv.WasmExecJS)
	serveBytes("/browse.js", "text/javascript", wasmhv.BrowseJS)
	// The vnet service worker: real same-origin /vnet/<port>/ URLs into the
	// page's virtual loopback, so the nested browser renders in-page servers
	// (the hypervisor UI SPA) natively. Must live beside the pages (scope cap).
	serveBytes("/vnet-sw.js", "text/javascript", wasmhv.VNetSWJS())
	// The worker bundle every skywire COMMAND runs in. Its presence IS the
	// capability the desk probes for: where it answers, the visor's Go runtime
	// schedules on its own thread instead of the one that draws the page;
	// where it 404s, desk-boot keeps the in-page skywireExec unchanged.
	serveBytes("/skywire-worker.js", "text/javascript", wasmhv.ExecWorkerJS())
	// The full skywire CLI module (resolved at the top: ServeWasm does not
	// start without one) — the desk host, the tab's visor and every command
	// the terminal runs.
	mux.HandleFunc("/skywire.wasm", func(w http.ResponseWriter, r *http.Request) {
		serveExecWasm(w, r, execWasmPath)
	})
	// The desk — the ONE page, served AT THE ROOT below: the tab as a Linux
	// host. The page spawns no visor of its own; the terminal runs `skywire
	// autoconfig` which starts the FULL binary's visor in the foreground, a second terminal
	// shows the help, and once the hypervisor UI listens on the virtual
	// loopback the nested browser opens it maximized on top. A visor the
	// operator stopped stays stopped across reloads (desk-boot's session).
	serveBytes("/desk-boot.js", "text/javascript", wasmhv.DeskBootJS())
	deskPage := deskShellHTML(wasmDeskScripts(), deskWasmBootOpts(cfg.DeskHelpTerminal, cfg.DeskDocsPort))
	if cfg.Harness {
		// --harness injects ctl-bridge.js (its presence IS the harness
		// signal). On the desk it registers the tab and mirrors the foreground visor
		// instance's stderr ring to /ctl/log, so `curl /ctl/log?tab=<id>`
		// reads the in-terminal visor's log.
		deskPage = bytes.Replace(deskPage,
			[]byte(`<script src="/desk-boot.js"></script>`),
			[]byte("<script src=\"/ctl-bridge.js\"></script>\n<script src=\"/desk-boot.js\"></script>"), 1)
	}
	// The desk IS the root here too, matching the visor-attached hypervisor
	// (#4491). The old separate path redirects rather than serving a second
	// copy, so a bookmark or a docs link still lands somewhere sensible.
	mux.HandleFunc("/desk", func(w http.ResponseWriter, _ *http.Request) {
		// A hand-written RELATIVE Location. Reached through the vnet service
		// worker this page lives under a /vnet/<port>/ prefix the server never
		// sees, so an absolute "/" escapes it and lands on the OUTER server's
		// root — a different visor entirely. http.Redirect cannot do this: it
		// resolves a relative target server-side, back into the absolute "/".
		w.Header().Set("Location", "./")
		w.WriteHeader(http.StatusMovedPermanently)
	})
	serveBytes("/autoupdate.js", "text/javascript", wasmhv.AutoUpdateJS)
	serveBytes("/manifest.webmanifest", "application/manifest+json", wasmhv.PWAManifest)
	serveBytes("/icon-192.png", "image/png", wasmhv.PWAIcon192)
	serveBytes("/icon-512.png", "image/png", wasmhv.PWAIcon512)
	// Real-origin mesh browser. The transport worker (served on the B origin at
	// scope "/") and the responder that answers B's handshake on V both come from
	// github.com/0magnet/realorigin; browse-transport.js is the transport the
	// responder calls, and is the only skywire-specific piece. All three are
	// static — the same bytes work on every origin. The B navigation shell is
	// host-routed below, since it needs per-request substitution.
	serveBytes("/browse-sw.js", "text/javascript", realorigin.ServiceWorkerJS())
	serveBytes("/browse-responder.js", "text/javascript", realorigin.ResponderJS())
	serveBytes("/browse-transport.js", "text/javascript", wasmhv.BrowseTransportJS)
	swJS := wasmhv.ServiceWorkerFor(wasmVer, []string{"./", "manifest.webmanifest", "icon-192.png", "icon-512.png", "wasm_exec.js", "browse.js", "desk-boot.js", "skywire-worker.js", "autoupdate.js"})
	mux.HandleFunc("/sw.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(swJS) //nolint:errcheck
	})
	// /wasm-version is what autoupdate.js polls. It is wasmVer plus, when a
	// command module is served, that file's current stamp — stat'd per
	// request, because skywire.wasm is rebuilt in place under a running
	// server and a desk tab whose visor IS that module must reload to run the
	// new one. (An open desk sat 22 h on a blob rebuilt several times over.)
	mux.HandleFunc("/wasm-version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(servedVersion(wasmVer, cfg.ExecWasmPath))) //nolint:errcheck
	})
	// Distinct browser-tab favicon for the wasm-visor surface (violet mesh-cloud),
	// so it reads apart at a glance from a host-native hypervisor tab.
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/x-icon")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(wasmhv.FaviconICO) //nolint:errcheck
	})

	// --harness: the /ctl/* operator control bridge + the ctl-bridge.js the
	// page loads to connect to it. DEV ONLY — unauthenticated control/eval.
	if cfg.Harness {
		ctlbridge.New(log.Printf).Register(mux)
		serveBytes("/ctl-bridge.js", "text/javascript", wasmhv.CtlBridgeJS)
		log.Warn("wasm-serve harness: /ctl/* control bridge mounted (DEV ONLY — do not expose publicly)")
	}

	fileServer := http.FileServer(http.FS(uiFS))

	// Bundled skycoin-web wallet under /wallet/ (custody stays browser-side).
	walletFS, wfsErr := WalletUIFS()
	var walletIndex []byte
	var werr error
	if wfsErr == nil {
		walletIndex, werr = fs.ReadFile(walletFS, "index.html")
	} else {
		werr = wfsErr
	}
	if werr == nil && cfg.Wallet {
		walletFiles := http.StripPrefix("/wallet/", http.FileServer(http.FS(walletFS)))
		walletIndex = bytes.Replace(walletIndex,
			[]byte(`<base href="/">`),
			[]byte(`<base href="/wallet/">`+walletDmsgFetchShim()), 1)
		// The cipher assets the bundle instantiates come from the one skywire
		// command module: serveWalletCipherAsset (wallet_cipher.go).
		mux.HandleFunc("/wallet/", func(w http.ResponseWriter, r *http.Request) {
			// The wallet is an IFRAME surface for the HV UI's ☰ wallet, not a
			// first-class page: its node API is routed through
			// window.parent.skywireVisor, which only exists when it's framed by the
			// booted HV. A TOP-LEVEL navigation to a wallet HTML page
			// (Sec-Fetch-Dest: document) is out of context — bounce it into the HV
			// UI. Framed loads (Sec-Fetch-Dest: iframe / empty on older browsers)
			// and asset fetches (script/style/fetch/…) fall through and serve
			// normally. The standalone top-level wallet is the separate
			// `skywire skycoin web` path, not this one.
			isWalletDoc := r.URL.Path == "/wallet/" || r.URL.Path == "/wallet/index.html" || r.URL.Path == "/wallet/config"
			if isWalletDoc && r.Header.Get("Sec-Fetch-Dest") == "document" {
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
			if r.URL.Path == "/wallet/" || r.URL.Path == "/wallet/index.html" {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "no-cache")
				_, _ = w.Write(walletIndex) //nolint:errcheck
				return
			}
			if r.URL.Path == "/wallet/config" {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "no-cache")
				_, _ = w.Write([]byte(wasmhv.WalletConfigHTML)) //nolint:errcheck
				return
			}
			if serveWalletCipherAsset(w, r, strings.TrimPrefix(r.URL.Path, "/wallet/")) {
				return
			}
			walletFiles.ServeHTTP(w, r)
		})
	}

	// NOTHING mesh-facing is mounted here, and that is deliberate: this process
	// serves a page, and the visor that page talks about runs IN THE TAB with
	// its own dmsg client. This server holds no visor and no dmsg identity —
	// see WasmServeConfig, which is addresses, TLS and asset paths.
	//
	// It used to. tpviz was mounted so the Angular bundle's network-visualizer
	// tab, which fetches ABSOLUTE same-origin /api/… paths, would not 404, and
	// since there was no visor to source it from, a dmsg client was minted from
	// a throwaway keypair — registering a fresh identity in the PRODUCTION
	// discovery on every restart and abandoning it on exit, to read three
	// deployment services it never needed to be reachable from. /api/local-visor
	// answered "connected": false, because there is no local visor here to
	// describe. Removed in #4500.
	//
	// If the visualizer is to work on this page, the in-tab visor must serve it
	// over vnet; that needs the tpviz bundle's API base made consistent first
	// (half its call sites hardcode the absolute path). Do not re-add a mesh
	// client to this server to paper over that.

	// The Angular client-error reporter POSTs console errors here via
	// navigator.sendBeacon (a RAW request that bypasses the SkywireHttpBackend
	// gateway), so it must be served by this mux rather than the in-tab core —
	// otherwise every reported error itself 404s (and, ironically, gets reported).
	// In the browser the console already IS the log, so accept + discard.
	mux.HandleFunc("/api/client-log", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`)) //nolint:errcheck
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			// The desk, framed or not. This origin serves one page: the
			// dashboard it shows is the in-tab visor's, reached through the
			// vnet service worker at /vnet/<port>/ — a request that never
			// arrives here — so, unlike the visor-attached hypervisor
			// (uiHandler), the desk's own nested browser never frames THIS
			// root. What does frame it is an embedder of the whole surface,
			// and the desk is the surface.
			//
			// The page boots with the fingerprint /wasm-version answers RIGHT
			// NOW, so a poll compares like with like.
			_, _ = w.Write(renderServedVersion(deskPage, servedVersion(wasmVer, cfg.ExecWasmPath))) //nolint:errcheck
			return
		}
		fileServer.ServeHTTP(w, r)
	})

	var handler http.Handler = mux
	if cfg.Password != "" {
		handler = wasmPasswordGate(mux, cfg.Password, cfg.TLS)
		log.Info("wasm-serve access-password gate enabled")
	}
	// Host-route isolated browse origins B (<pkslug>.mesh.localhost): a request to
	// B for the transport SW falls through to the mux (same bytes, any host); every
	// other B request is served the real-origin bootstrap shell, which registers
	// the SW, injects the mesh page into B, and thereafter the SW intercepts
	// subresources. Non-B hosts (the visor origin V = localhost) are untouched.
	base := handler
	handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isMeshBrowseHost(r.Host, suffix) && r.URL.Path != "/browse-sw.js" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(browseBootstrap) //nolint:errcheck
			return
		}
		base.ServeHTTP(w, r)
	})
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	// Shut the server down when the visor's context is canceled.
	go func() {
		<-ctx.Done()
		_ = srv.Close() //nolint:errcheck
	}()

	if cfg.TLS {
		var cert tls.Certificate
		var err error
		if cfg.TLSCert != "" && cfg.TLSKey != "" {
			// Operator-supplied cert (e.g. a locally-trusted *.mesh.localhost cert
			// from mkcert, or a real wildcard cert) — B-origin iframes load with no
			// per-host accept.
			cert, err = tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
		} else {
			cert, err = wasmLocalhostTLSCert()
		}
		if err != nil {
			return fmt.Errorf("tls cert: %w", err)
		}
		srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
		log.Infof("serving standalone wasm-visor over HTTPS (self-signed localhost cert) on %s — open https://localhost%s and accept the cert warning once", cfg.Addr, cfg.Addr)
		if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	}
	log.Infof("serving the desk (skywire.wasm: %s) on %s", execModuleDesc(execWasmPath), cfg.Addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// BrowseOriginConfig configures ServeBrowseOrigin — the browse-origin bootstrap
// server for the hosted two-server model. It serves ONLY the embedded SW bootstrap
// (and browse-sw.js) for the isolated browse origins B (<pk><Suffix>); it is NEVER
// in the content path (the registered SW relays every fetch to the visitor's in-tab
// visor at VOrigin). Front it with Caddy on *.<Suffix>.
type BrowseOriginConfig struct {
	Addr    string          // HTTP listen address (loopback; Caddy fronts it)
	VOrigin string          // the visor app origin V, e.g. "https://theskywirenetwork.net"
	Suffix  string          // browse-origin domain suffix (leading dot), e.g. ".haltingstate.net"
	TLS     bool            // usually false — Caddy terminates TLS
	TLSCert string          // optional PEM cert (paired with TLSKey)
	TLSKey  string          // optional PEM key
	Log     *logging.Logger // nil → package default
}

// ServeBrowseOrigin runs the browse-origin bootstrap server (RFC §4b, hosted mode):
// for any Host under Suffix it returns the SW bootstrap shell with __APP_ORIGIN__ and
// __SUFFIX__ substituted; /browse-sw.js returns the transport SW. Blocking until ctx
// is canceled.
func ServeBrowseOrigin(ctx context.Context, cfg BrowseOriginConfig) error {
	log := cfg.Log
	if log == nil {
		log = logging.MustGetLogger("browse-origin")
	}
	if strings.TrimSpace(cfg.VOrigin) == "" {
		return fmt.Errorf("browse-origin: VOrigin is required")
	}
	suffix := strings.TrimSpace(cfg.Suffix)
	if suffix == "" {
		return fmt.Errorf("browse-origin: Suffix is required")
	}
	if !strings.HasPrefix(suffix, ".") {
		suffix = "." + suffix
	}

	// The worker, the shell's substitutions and the serve-the-shell-for-every-
	// other-path rule all come from realorigin. The shell itself is ours: the
	// library's is deliberately plain, and building a mesh route takes long
	// enough that the wait is worth narrating.
	//
	// The worker sits at /browse-sw.js rather than the library's default, because
	// /sw.js on this origin is already the PWA's app-shell worker.
	roCfg := realorigin.Config{
		Suffix:    suffix,
		AppOrigin: cfg.VOrigin,
		SWPath:    "/browse-sw.js",
		Shell:     wasmhv.BrowseBootstrapHTML,
	}
	handler, err := realorigin.Handler(roCfg)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		_ = srv.Close() //nolint:errcheck
	}()

	log.Infof("serving browse-origin bootstrap on %s (V-origin %s, suffix %s)", cfg.Addr, cfg.VOrigin, suffix)
	if cfg.TLS {
		var cert tls.Certificate
		var err error
		if cfg.TLSCert != "" && cfg.TLSKey != "" {
			cert, err = tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
		} else {
			cert, err = wasmLocalhostTLSCert()
		}
		if err != nil {
			return fmt.Errorf("tls cert: %w", err)
		}
		srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
		if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// coinNodeDefault is the deployment skycoin node the bundled wallet talks
// to by default, sourced from the embedded services-config.json.
func coinNodeDefault() string {
	if n := strings.TrimSpace(dmsg.Prod.SkycoinNode); n != "" {
		return n
	}
	return "https://node.skycoin.com.aong2hr4en7v6bnxr3az5hzruad7qsbv27xr5ajioyicfaor3n2mc.dmsg"
}

// walletDmsgFetchShim is injected into the bundled wallet's index to route
// its node API over dmsg via the parent PWA's visor. See the CLI serve
// command's original comment for the full rationale.
func walletDmsgFetchShim() string {
	return `<script>(function(){` +
		`var rf=window.fetch?window.fetch.bind(window):null;` +
		`var COINS=` + string(coins.JSON()) + `;` +
		`function p(u){try{return new URL(u,location.href).pathname;}catch(e){return String(u);}}` +
		`function q(u){try{return new URL(u,location.href).search;}catch(e){return "";}}` +
		`function coinMatch(pth){return /^\/coin\/(\d+)(\/[^?]*)?/.exec(pth);}` +
		`function isBtc(pth){return /\/v1\/btc\//.test(pth);}` +
		`function ls(k){try{return localStorage.getItem(k)||"";}catch(e){return "";}}` +
		`function ctOf(input,init){try{var h;if(init&&init.headers)h=new Headers(init.headers);else if(input&&input.headers&&input.headers.get)h=input.headers;if(h&&h.get){var c=h.get("content-type");if(c)return c;}}catch(e){}return "";}` +
		`function jr(s,o){return new Response(JSON.stringify(o),{status:s,headers:{"Content-Type":"application/json"}});}` +
		`function mkResp(r){var b=(r&&typeof r.body==="string")?r.body:(r&&r.body?new TextDecoder().decode(r.body):"");return new Response(b,{status:(r&&r.status)||502,headers:{"Content-Type":"application/json"}});}` +
		`window.fetch=function(input,init){` +
		`var url=(typeof input==="string")?input:(input&&input.url);` +
		`var pn=p(url);` +
		`if(/\/api\/v1\/coins$/.test(pn)){return Promise.resolve(new Response(JSON.stringify(COINS),{status:200,headers:{"Content-Type":"application/json"}}));}` +
		`var mc=coinMatch(pn);` +
		`var coin=!!mc||/^\/api\/v[12]\//.test(pn);` +
		`if(!coin)return rf?rf(input,init):Promise.reject(new Error("no fetch"));` +
		`var rel=mc?(mc[2]||"/"):pn;var btc=isBtc(pn);` +
		`init=init||{};` +
		`var v=window.parent&&window.parent.skywireVisor;` +
		`if(!v||!v.fetchDmsg)return Promise.resolve(jr(503,{error:"skywire visor not ready"}));` +
		`var m=init.method||"GET",pth=rel+q(url),t0=Date.now();` +
		`function wlog(s){try{var L=window.parent&&window.parent.skywireLog;if(L&&L.emit)L.emit("info",["[wallet] "+s]);}catch(e){}}` +
		`function done(tag){return function(r){wlog(tag+" → "+((r&&r.status)||"?")+" ("+(Date.now()-t0)+"ms)");return mkResp(r);};}` +
		`function fail(tag){return function(e){wlog(tag+" ✗ "+String((e&&e.message)||e));return jr(502,{error:String(e)});};}` +
		`if(btc){` +
		`if(!v.btcFetch)return Promise.resolve(jr(503,{error:"BTC gateway not available"}));` +
		`var back="";try{var _nu=JSON.parse(ls("nodeUrls")||"{}");back=(_nu&&_nu["-2"])||"";}catch(e){}if(!back)back=ls("skywire-btc-backend")||"";if(!back)back="ssl://electrum.blockstream.info:50002";` +
		`var btag="btc "+m+" "+pth;wlog(btag);` +
		`return Promise.resolve(v.btcFetch(back,m,pth,init.body||null,ls("skywire-btc-proxy")||ls("skywire-upstream-proxy"))).then(done(btag)).catch(fail(btag));` +
		`}` +
		`var _u;try{_u=new URL(url,location.href);}catch(e){_u=null;}` +
		`var node=(_u&&_u.host&&_u.host!==location.host)?(_u.protocol+"//"+_u.host):(ls("skywire-coin-node")||"` + coinNodeDefault() + `");` +
		`var ct=ctOf(input,init),hdrs=ct?{"Content-Type":ct}:null;` +
		`if(/^https?:\/\//i.test(node)&&!/\.dmsg\b/i.test(node)){` +
		`if(!v.fetchClearnet)return Promise.resolve(jr(503,{error:"skysocks clearnet gateway not available"}));` +
		`var up=ls("skywire-upstream-proxy")||"";` +
		`var full=node.replace(/\/+$/,"")+pth,ctag="node "+m+" "+full+" via skysocks "+(up?up.slice(0,8):"auto");wlog(ctag);` +
		`return Promise.resolve(v.fetchClearnet(up,m,full,init.body||null,"wallet",hdrs)).then(done(ctag)).catch(fail(ctag));` +
		`}` +
		`var host=node.replace(/^\w+:\/\//,""),dtag="node "+m+" dmsg://"+host+pth;wlog(dtag);` +
		`return Promise.resolve(v.fetchDmsg(host,m,pth,init.body||null,hdrs)).then(done(dtag)).catch(fail(dtag));` +
		`};` +
		`})();</script>`
}

// isMeshBrowseHost reports whether an HTTP Host header targets an isolated
// real-origin browse origin B (<...><suffix>[:port]) rather than the visor
// origin V. For the *.mesh.localhost local model browsers resolve *.localhost to
// loopback, so these hit the same wasm-serve listener and are distinguished only
// by Host.
func isMeshBrowseHost(h, suffix string) bool {
	host := h
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return strings.HasSuffix(host, suffix)
}

// wasmLocalhostTLSCert returns a self-signed localhost cert for
// --wasm-serve-tls, persisted under the temp dir and reused across
// restarts. NOT for production.
func wasmLocalhostTLSCert() (tls.Certificate, error) {
	dir := filepath.Join(os.TempDir(), "skywire-hv-serve-tls")
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")

	if c, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		// Reuse the cached cert only if it's unexpired AND already carries the
		// *.mesh.localhost SAN (real-origin browse origins B share this one cert
		// with the visor origin V); otherwise fall through and regenerate.
		if leaf, e := x509.ParseCertificate(c.Certificate[0]); e == nil && time.Now().Before(leaf.NotAfter) {
			for _, n := range leaf.DNSNames {
				if n == "*.mesh.localhost" {
					return c, nil
				}
			}
		}
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		// localhost = the visor origin V; *.mesh.localhost + mesh.localhost = the
		// isolated real-origin browse origins B (<pkslug>.mesh.localhost), so one
		// cert covers V and every B under the same wasm-serve listener.
		DNSNames:    []string{"localhost", "mesh.localhost", "*.mesh.localhost"},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	if err := os.MkdirAll(dir, 0o700); err == nil {
		_ = os.WriteFile(certPath, certPEM, 0o600) //nolint:errcheck
		_ = os.WriteFile(keyPath, keyPEM, 0o600)   //nolint:errcheck
	}
	return tls.X509KeyPair(certPEM, keyPEM)
}

// wasmPasswordGate wraps h behind a cookie-based access-password gate. The cookie
// carries a random per-process session token — NOT a hash of the password — so a
// leaked cookie can't be reversed to the password and there is no fast-hash of a
// secret for an offline attacker to grind. Both the password check and the cookie
// check run in constant time. The token is regenerated on each restart, so an open
// session re-authenticates after the visor restarts.
func wasmPasswordGate(h http.Handler, password string, secure bool) http.Handler {
	sessionToken, err := randomHex(32)
	if err != nil {
		// crypto/rand unavailable — fail closed rather than serve unguarded.
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "access gate unavailable", http.StatusServiceUnavailable)
		})
	}
	const cookieName = "skywire-hv-auth"
	loginPage := func(w http.ResponseWriter, msg string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprintf(w, `<!doctype html><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1"><title>skywire</title><body style="background:#0e0c14;color:#cdd2da;font:14px/1.5 system-ui;display:flex;min-height:90vh;align-items:center;justify-content:center"><form method=post action=/__login style="background:#15131c;border:1px solid #2a2342;border-radius:10px;padding:2em;max-width:300px"><h2 style="color:#9d7cff;margin-top:0">skywire</h2><p>%s</p><input name=p type=password placeholder=password autofocus style="width:100%%;box-sizing:border-box;padding:.6em;background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;border-radius:6px"><button style="margin-top:1em;width:100%%;padding:.6em;background:#9d7cff;color:#0e0c14;border:0;border-radius:6px;font-weight:bold;cursor:pointer">enter</button></form></body>`, html.EscapeString(msg)) //nolint:errcheck
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__login" && r.Method == http.MethodPost {
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			if r.ParseForm() == nil && subtle.ConstantTimeCompare([]byte(r.FormValue("p")), []byte(password)) == 1 {
				http.SetCookie(w, &http.Cookie{Name: cookieName, Value: sessionToken, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: secure, MaxAge: 7 * 24 * 3600}) //nolint:gosec
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
			loginPage(w, "wrong password")
			return
		}
		if c, err := r.Cookie(cookieName); err == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(sessionToken)) == 1 {
			h.ServeHTTP(w, r)
			return
		}
		loginPage(w, "access password required")
	})
}

// execModuleDesc names where the served command module comes from, for the
// startup log: the path when it is a file, else the embedded copy's stamp.
func execModuleDesc(path string) string {
	if path != "" {
		return path
	}
	return "embedded " + execwasm.Stamp()
}

// randomHex returns n cryptographically-random bytes as a lowercase hex string.
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// deskWasmBootOpts renders the skywireDeskBoot options object for the
// wasm-served desk (`hv serve --exec-wasm`'s /desk): the tab as a Linux host.
// Assets come from the routes ServeWasm already exposes: /browse.js is the full
// desk bundle, /skywire.wasm the ONE command module — the desk host (`skywire
// desk-host`), the tab's visor and every command the terminal runs — and
// /wasm_exec.js its loader. (ServeWasm does not start without that module.)
//
// helpTerminal and the docs server are OFF unless asked for, because each one
// is a whole extra Go/wasm runtime of the full skywire binary and that memory
// never comes back. Measured on a live desk: three instances — `autoconfig`,
// `doc serve` and an already-EXITED `--help` — held a 1.3 GB renderer, of which
// the JS heap was 27 MB; a forced GC returned 36 MB. WebAssembly.Memory only
// ever grows and Go's wasm runtime never hands pages back, so every instance's
// peak becomes a permanent floor for the tab, and exiting does not release it.
//
// The visor is worth that cost. A terminal whose sole content is the output of
// `skywire --help`, printed once and then exited, is not — and the docs are a
// server that most sessions never open. Both stay available on request.
func deskWasmBootOpts(helpTerminal bool, docsPort int) string {
	return fmt.Sprintf(`{
  persistDB: 'skywire-desk',
  wasmURL: '/skywire.wasm',
  wasmExecURL: '/wasm_exec.js',
  autostartVisor: true,
  helpTerminal: %t,
  docsPort: %d,
  hvWindow: true,
}`, helpTerminal, docsPort)
}

// wasmDeskScripts is the script include list of the wasm-served desk: the Go
// loader, the desk bundle, the served-build stamp with its poller, then
// desk-boot. autoupdate.js reloads the tab once /wasm-version moves — which a
// rebuilt skywire.wasm now makes it do — and the exec worker the tab's visor
// runs in dies with the page, so the reload boots the new module. The stamp
// is the servedVersionToken; the root handler fills it per request.
func wasmDeskScripts() string {
	return `<script src="/wasm_exec.js"></script>` + "\n" +
		`<script src="/browse.js"></script>` + "\n" +
		`<script>window.__SKYWIRE_WASM_VERSION__="` + servedVersionToken + `";</script>` + "\n" +
		`<script src="/autoupdate.js"></script>` + "\n" +
		`<script src="/desk-boot.js"></script>`
}

// deskShellTemplate is the shared skeleton of the converged desk page (/desk):
// boot overlay + error capture + the skywireDeskBoot call. ONE skeleton behind
// both serving contexts — `hv serve`'s wasm desk and the native hypervisor's
// /desk (hypervisor_handlers_browse.go) — so the two pages cannot drift.
// __DESK_SCRIPTS__ carries the context's script includes, ending with the
// /desk-boot.js tag the harness injection keys on; __DESK_OPTS__ carries its
// skywireDeskBoot options object. onStatus stays in the skeleton (it drives
// the shared overlay) and is merged UNDER the context opts so a context could
// still override it.
const deskShellTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<link rel="manifest" href="manifest.webmanifest">
<meta name="theme-color" content="#0e0c14">
<link rel="apple-touch-icon" href="icon-192.png">
<title>skywire</title>
<style>
  html,body{margin:0;height:100%;background:#0e0c14;color:#cdd2da;font:14px/1.5 system-ui,sans-serif}
  #install{position:fixed;right:14px;bottom:14px;z-index:20;display:none;padding:8px 14px;border-radius:8px;border:1px solid #2a2342;background:#1b1626;color:#cdd2da;font:13px system-ui;cursor:pointer}
  #boot{position:fixed;inset:0;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:.8em;z-index:10}
  #boot .t{color:#9d7cff;font:600 18px system-ui}
  #boot .s{color:#9aa0a6;font:12px monospace;max-width:44em;text-align:center}
  #boot.gone{display:none}
</style>
</head>
<body>
<button id="install" title="Install this desk as an app: it opens on its own and keeps working when this address is out of reach">Install Skywire</button>
<div id="boot">
  <div class="t">skywire</div>
  <div class="s" id="boot-msg">loading…</div>
</div>
<script>
// PWA: a service worker keeps the desk shell (and, once fetched, the wasm
// modules) so the installed app opens off the LAN too — its visor then falls
// back to dmsg over wss. The install prompt is offered as a button, not forced.
if ('serviceWorker' in navigator) {
  window.addEventListener('load', function () {
    navigator.serviceWorker.register('sw.js').catch(function (e) { console.warn('sw register failed', e); });
  });
}
(function () {
  var btn = document.getElementById('install'), deferred = null;
  window.addEventListener('beforeinstallprompt', function (e) { e.preventDefault(); deferred = e; if (btn) btn.style.display = 'block'; });
  window.addEventListener('appinstalled', function () { deferred = null; if (btn) btn.style.display = 'none'; });
  if (btn) btn.addEventListener('click', function () { if (!deferred) return; deferred.prompt(); deferred.userChoice.then(function () { deferred = null; btn.style.display = 'none'; }); });
})();
</script>
<script>
window.__errs = [];
Error.stackTraceLimit = 300;
addEventListener('error', function (e) {
  if (window.__errs.length < 20) {
    var s = String((e.error && e.error.stack) || e.message || e);
    window.__errs.push(s.slice(0, 600) + '\n…\n' + s.slice(-2400));
  }
});
</script>
__DESK_SCRIPTS__
<script>
skywireDeskBoot(Object.assign({
  onStatus: function (s) { var el = document.getElementById('boot-msg'); if (el) el.textContent = s; },
}, __DESK_OPTS__)).then(function () {
  document.getElementById('boot').className = 'gone';
}).catch(function (e) {
  var el = document.getElementById('boot-msg');
  if (el) el.textContent = 'boot failed: ' + ((e && e.message) || e);
});
</script>
</body>
</html>
`

// deskShellHTML renders the shared desk skeleton for one serving context.
func deskShellHTML(scriptsHTML, deskOptsJS string) []byte {
	out := strings.ReplaceAll(deskShellTemplate, "__DESK_SCRIPTS__", scriptsHTML)
	return []byte(strings.ReplaceAll(out, "__DESK_OPTS__", deskOptsJS))
}

// DefaultExecWasmPath returns the package location of the skywire command
// module (skyenv.ExecWasmPath) when that file exists, else "". It is the
// fallback for an empty ExecWasmPath, so a host whose updater installed the
// module serves the desk without any flag or config change.
func DefaultExecWasmPath() string {
	p := skyenv.ExecWasmPath()
	if p == "" {
		return ""
	}
	if st, err := os.Stat(p); err != nil || st.IsDir() {
		return ""
	}
	return p
}
