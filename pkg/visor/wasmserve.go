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
	"encoding/json"
	"encoding/pem"
	"fmt"
	"html"
	"io/fs"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/tpviz"
	"github.com/skycoin/skywire/pkg/wallet/coins"
	"github.com/skycoin/skywire/pkg/wasmhv"
	"github.com/skycoin/skywire/pkg/wasmhv/ctlbridge"
	"github.com/skycoin/skywire/pkg/wasmhv/wasmbin"
)

// WasmServeConfig configures ServeWasm. Mirrors the `hv serve` flags.
type WasmServeConfig struct {
	Addr     string // HTTP listen address (e.g. ":8443")
	TLS      bool   // serve HTTPS (self-signed localhost cert, or TLSCert/TLSKey if set)
	TLSCert  string // optional cert file (PEM) — a locally-trusted *.mesh.localhost cert (mkcert) so B-origin iframes load without a per-host accept; empty = built-in self-signed
	TLSKey   string // optional key file (PEM), paired with TLSCert
	Harness  bool   // mount the /ctl/* operator control bridge (DEV ONLY)
	Wallet   bool   // serve the bundled skycoin-web wallet at /wallet/
	Variant  string // "" = build default; "go" | "tinygo"
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
	Log     *logging.Logger // nil → package default
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
	if !wasmbin.Embedded() {
		return fmt.Errorf("no embedded wasm-visor: rebuild skywire after `make embed-wasm-visor`")
	}
	// Pick which embedded wasm-visor to serve. Empty = the build default.
	variant := wasmbin.Default()
	if cfg.Variant != "" {
		variant = wasmbin.Variant(cfg.Variant)
		if !wasmbin.Has(variant) {
			return fmt.Errorf("wasm-visor variant %q not embedded (available: %v)", cfg.Variant, wasmbin.Available())
		}
	}
	wasm, err := wasmbin.GetVariant(variant)
	if err != nil {
		return fmt.Errorf("embedded wasm-visor: %w", err)
	}
	uiFS, err := HypervisorUIFS()
	if err != nil {
		return fmt.Errorf("hypervisor UI assets: %w", err)
	}
	indexB, err := fs.ReadFile(uiFS, "index.html")
	if err != nil {
		return fmt.Errorf("read index.html: %w", err)
	}
	// wasmVer fingerprints the WHOLE served build (wasm + Angular index +
	// binary version) so the page self-reloads on any newer deploy.
	vh := sha256.New()
	vh.Write(wasm)
	vh.Write(indexB)
	vh.Write([]byte(buildinfo.Version()))
	// Fold the served client JS into the fingerprint too, so a rebuild that
	// only touches hv-boot/worker/browse/autoupdate still bumps /wasm-version
	// (and the ETag below) and the page picks up the new assets.
	vh.Write(wasmhv.HvBootJS)
	vh.Write(wasmhv.WorkerJS)
	vh.Write(wasmhv.BrowseJS)
	vh.Write(wasmhv.AutoUpdateJS)
	vh.Write(wasmhv.BrowseSWJS)
	vh.Write(wasmhv.BrowseBootstrapHTML)
	vh.Write(wasmhv.BrowseResponderJS)
	wasmVer := hex.EncodeToString(vh.Sum(nil))[:16]
	// etag is the fingerprint as an HTTP entity tag. The version-bound assets
	// below are served no-cache + ETag so every load REVALIDATES: unchanged →
	// cheap 304, a rebuild-restart → the fresh bytes are fetched immediately.
	// (max-age caching defeats the rebuild-restart loop: the browser would keep
	// serving the previous 9.5MB blob from its HTTP cache while /wasm-version
	// already reports the new hash.)
	etag := `"` + wasmVer + `"`

	// Real-origin browse origin (RFC §4a/§4b): the browse iframe loads a mesh site
	// from an isolated origin B (<pkslug>.mesh.localhost) on THIS listener (host-
	// routed below). Compute scheme+port once so the injected client config and the
	// bootstrap substitutions agree; B and V share this listener + TLS cert.
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
	// B-origin scheme+port. B may live elsewhere than V: for the *.localhost local
	// model B and V share THIS listener + cert, so B inherits browseScheme/bport;
	// for a hosted suffix B is fronted by Caddy TLS on 443 (a separate process /
	// host — see ServeBrowseOrigin), so it's plain https with no explicit port.
	bScheme, bPort := browseScheme, bport
	if !strings.HasSuffix(suffix, ".localhost") {
		bScheme, bPort = "https", ""
	}
	// Injected so browse.js enters real-origin mode and builds <pk><suffix>[:<port>]
	// origins for the WinBox browser iframe.
	browseOriginJS := "window.__SKYWIRE_BROWSE_ORIGIN__={suffix:" + strconv.Quote(suffix) + ",scheme:" + strconv.Quote(bScheme) + ",port:" + strconv.Quote(bPort) + "};"
	index := injectWasmBoot(indexB, wasmVer, cfg.Harness, browseOriginJS)

	browseBootstrap := bytes.ReplaceAll(wasmhv.BrowseBootstrapHTML, []byte("__V_ORIGIN__"), []byte(browseScheme+"://"+vHostPort))
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
	// Both embedded blobs served from one origin so the PWA can switch
	// Go<->TinyGo at runtime via ?variant=. wasm_exec.js matches the blob.
	pickVariant := func(r *http.Request) wasmbin.Variant {
		if q := r.URL.Query().Get("variant"); q != "" && wasmbin.Has(wasmbin.Variant(q)) {
			return wasmbin.Variant(q)
		}
		return variant
	}
	// Per-variant ETag: ?variant= serves different bytes under the same URL, so
	// the variant is part of the tag to keep the browser's cache from crossing
	// them. For the default variant the tag tracks wasmVer (the blob's own hash),
	// so a rebuild-restart fetches the fresh 9.5MB blob; an unchanged one 304s.
	variantETag := func(r *http.Request) string { return `"` + wasmVer + "-" + string(pickVariant(r)) + `"` }
	mux.HandleFunc("/wasm-visor.wasm", func(w http.ResponseWriter, r *http.Request) {
		vtag := variantETag(r)
		w.Header().Set("Content-Type", "application/wasm")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", vtag)
		if r.Header.Get("If-None-Match") == vtag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		b, err := wasmbin.GetVariant(pickVariant(r))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(b) //nolint:errcheck
	})
	mux.HandleFunc("/wasm_exec.js", func(w http.ResponseWriter, r *http.Request) {
		vtag := variantETag(r)
		w.Header().Set("Content-Type", "text/javascript")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", vtag)
		if r.Header.Get("If-None-Match") == vtag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write(wasmbin.WasmExecJSVariant(pickVariant(r))) //nolint:errcheck
	})
	mux.HandleFunc("/wasm-variants.json", func(w http.ResponseWriter, _ *http.Request) {
		avail := wasmbin.Available()
		names := make([]string, len(avail))
		for i, v := range avail {
			names[i] = string(v)
		}
		b, _ := json.Marshal(map[string]interface{}{"available": names, "default": string(variant)}) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b) //nolint:errcheck
	})
	serveBytes("/hv-boot.js", "text/javascript", wasmhv.HvBootJS)
	serveBytes("/worker.js", "text/javascript", wasmhv.WorkerJS)
	serveBytes("/browse.js", "text/javascript", wasmhv.BrowseJS)
	serveBytes("/autoupdate.js", "text/javascript", wasmhv.AutoUpdateJS)
	serveBytes("/manifest.webmanifest", "application/manifest+json", wasmhv.PWAManifest)
	serveBytes("/icon-192.png", "image/png", wasmhv.PWAIcon192)
	serveBytes("/icon-512.png", "image/png", wasmhv.PWAIcon512)
	// Real-origin mesh browser (RFC §4b): the transport SW (served on the B origin,
	// scope "/") and the V-origin mesh-fetch responder. The B navigation shell is
	// host-routed below (it needs per-request substitution). browse-sw.js and
	// browse-responder.js are static — the same bytes work on every origin.
	serveBytes("/browse-sw.js", "text/javascript", wasmhv.BrowseSWJS)
	serveBytes("/browse-responder.js", "text/javascript", wasmhv.BrowseResponderJS)
	swJS := bytes.ReplaceAll(wasmhv.ServiceWorkerJS, []byte("__BUILD__"), []byte(wasmVer))
	mux.HandleFunc("/sw.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(swJS) //nolint:errcheck
	})
	mux.HandleFunc("/wasm-version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(wasmVer)) //nolint:errcheck
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
		// Cipher: the skycoin-web wallet keeps key material in the browser via its
		// window.SkycoinCipher globals, published by skycoin-lite/wasmcipher.Register()
		// which the embedded wasm-visor blob calls (no boot() needed). It falls back
		// to loading assets/scripts/skycoin-lite.wasm + wasm_exec.js when the globals
		// aren't already present — which is the case in THIS iframe, a window separate
		// from the SharedWorker running the visor. So serve the joint-compiled visor
		// blob AS the cipher (no separate ~1.8 MB skycoin-lite build), exactly like
		// `skywire skycoin web` does via cmd/skycoin's registerCipherWasm. Prefer the
		// smaller TinyGo pair when embedded (this page uses only the cipher).
		cipherVar := wasmbin.Default()
		if wasmbin.Has(wasmbin.TinyGo) {
			cipherVar = wasmbin.TinyGo
		}
		cipherGz := wasmbin.GetVariantGz(cipherVar)
		cipherExec := wasmbin.WasmExecJSVariant(cipherVar)
		mux.HandleFunc("/wallet/", func(w http.ResponseWriter, r *http.Request) {
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
			if r.URL.Path == "/wallet/assets/scripts/skycoin-lite.wasm" && len(cipherGz) > 0 {
				w.Header().Set("Content-Type", "application/wasm")
				w.Header().Set("Cache-Control", "no-cache")
				if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
					w.Header().Set("Content-Encoding", "gzip")
					_, _ = w.Write(cipherGz) //nolint:errcheck
					return
				}
				if wasm, derr := wasmbin.GetVariant(cipherVar); derr == nil {
					_, _ = w.Write(wasm) //nolint:errcheck
				}
				return
			}
			if r.URL.Path == "/wallet/assets/scripts/wasm_exec.js" && len(cipherExec) > 0 {
				w.Header().Set("Content-Type", "application/javascript")
				w.Header().Set("Cache-Control", "no-cache")
				_, _ = w.Write(cipherExec) //nolint:errcheck
				return
			}
			walletFiles.ServeHTTP(w, r)
		})
	}

	// Transport-graph visualizer (pkg/tpviz): mirror the native HV wiring so
	// the wasm visor's network-visualizer tab renders the real graph. An
	// ephemeral dmsg client sources the dmsg-only deployment discovery.
	tpvSrv := tpviz.NewServer(tpviz.DefaultConfig())
	go tpvSrv.Start()
	go func() {
		dlog := logging.MustGetLogger("wasm-serve-tpviz")
		pk, sk := cipher.GenerateKeyPair()
		dmsgC, _, dErr := dmsgclient.StartDmsgEmbedded(ctx, dlog, pk, sk, false)
		if dErr != nil {
			dlog.WithError(dErr).Warn("tpviz dmsg client failed to start; network-graph data unavailable")
			return
		}
		tpvSrv.SetDmsgHTTPClient(&http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC)})
		tpvSrv.SetCXOSubMgrFromDmsg(dmsgC)
		dlog.Info("tpviz deployment discovery sourced over dmsg")
	}()
	tpvH := tpvSrv.Handler()
	mux.Handle("/tp-viz/", http.StripPrefix("/tp-viz", tpvH))
	mux.Handle("/tp-viz", http.StripPrefix("/tp-viz", tpvH))
	// The tp-viz bundle fetches these with ABSOLUTE /api/* paths (they bypass the
	// Angular SkywireHttpBackend), plus opens the /ws/local-visor socket. Mirror
	// the full native-HV set; omitting /api/health + /api/tps/status left the
	// visualizer 404ing on every poll (and the WS failing).
	for _, p := range []string{
		"/api/transports", "/api/services", "/api/uptimes", "/api/local-visor",
		"/api/ip-groups", "/api/dmsg/servers", "/api/health", "/api/tps/status",
		"/api/tps/add-transport", "/api/tps/remove-transport", "/api/tps/refresh-transports",
		"/api/local/add-transport", "/api/local/remove-transport", "/ws/local-visor",
	} {
		mux.Handle(p, tpvH)
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write(index) //nolint:errcheck
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
	log.Infof("serving standalone wasm-visor (ui + %d-byte wasm + hv-boot) on %s", len(wasm), cfg.Addr)
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
// for any Host under Suffix it returns the SW bootstrap shell with __V_ORIGIN__ and
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

	// Precompute the bootstrap shell: the SW bootstrap that every browse origin B
	// serves. It registers browse-sw.js and points the SW at the visitor's in-tab
	// visor at VOrigin; the transcode substitutions match ServeWasm.
	bootstrap := bytes.ReplaceAll(wasmhv.BrowseBootstrapHTML, []byte("__V_ORIGIN__"), []byte(cfg.VOrigin))
	bootstrap = bytes.ReplaceAll(bootstrap, []byte("__SUFFIX__"), []byte(suffix))

	// Caddy only routes *.Suffix here, so no host check is needed: /browse-sw.js is
	// the transport SW, every other path is the bootstrap shell.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/browse-sw.js" {
			w.Header().Set("Content-Type", "text/javascript")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write(wasmhv.BrowseSWJS) //nolint:errcheck
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(bootstrap) //nolint:errcheck
	})

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

// injectWasmBoot inserts the hv-boot.js bootstrap (+ browse/autoupdate,
// and either the harness ctl-bridge or the PWA manifest/SW) right after
// <head> so it runs before Angular's deferred module scripts.
func injectWasmBoot(index []byte, wasmVer string, harness bool, browseOriginJS string) []byte {
	s := string(index)
	tag := "\n"
	if browseOriginJS != "" {
		// Set BEFORE browse.js loads so the WinBox browser picks real-origin mode.
		tag += "<script>" + browseOriginJS + "</script>\n"
	}
	tag += "<script src=\"hv-boot.js\"></script>\n" +
		"<script src=\"browse.js\"></script>\n" +
		// Mesh-fetch responder: lets embedded <pk>.mesh.localhost browse iframes
		// reach THIS app's first-party skywireVisor (RFC §4b real-origin browser).
		"<script src=\"browse-responder.js\"></script>\n" +
		"<script>" + wasmhv.BrowseLauncherJS + "</script>\n" +
		"<script>window.__SKYWIRE_WASM_VERSION__=" + strconv.Quote(wasmVer) + ";</script>\n" +
		"<script src=\"autoupdate.js\"></script>\n"
	if harness {
		tag += "<script src=\"ctl-bridge.js\"></script>\n"
	} else {
		tag += "<link rel=\"manifest\" href=\"manifest.webmanifest\">\n" +
			"<meta name=\"theme-color\" content=\"#0e0c14\">\n" +
			"<link rel=\"apple-touch-icon\" href=\"icon-192.png\">\n" +
			"<script>if('serviceWorker' in navigator){window.addEventListener('load',function(){navigator.serviceWorker.register('sw.js').catch(function(e){console.warn('sw register failed',e)})})}</script>\n"
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

// randomHex returns n cryptographically-random bytes as a lowercase hex string.
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
