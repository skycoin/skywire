// Package clihv cmd/skywire-cli/commands/hv/serve.go
package clihv

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"html"
	"io/fs"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
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
	serveAddr     string
	serveHarness  bool
	serveTLS      bool
	servePassword string
)

// walletDmsgFetchShim is injected into the bundled skycoin-web wallet's index
// (right after <base>, before the wallet's own scripts) by the /wallet/ handler.
// The wallet uses Angular's fetch backend (skycoin-web withFetch), so overriding
// window.fetch is enough to intercept its node API. Coin API calls
// (/api/v1|/api/v2) are routed over dmsg via the parent PWA's visor
// (skywireVisor.fetchDmsg) to the configured coin node
// (localStorage['skywire-coin-node'] = "<pk>:<port>" — the visor's type=coin
// forward). Everything else falls through to native fetch. No Service Worker
// needed, so this works under --harness and file:// too. Same-origin iframe, so
// it reads the parent's visor + the shared localStorage directly.
const walletDmsgFetchShim = `<script>(function(){` +
	`var rf=window.fetch?window.fetch.bind(window):null;` +
	`function p(u){try{return new URL(u,location.href).pathname;}catch(e){return String(u);}}` +
	`function q(u){try{return new URL(u,location.href).search;}catch(e){return "";}}` +
	`function isCoin(u){return /^\/api\/v[12]\//.test(p(u));}` +
	`function jr(s,o){return new Response(JSON.stringify(o),{status:s,headers:{"Content-Type":"application/json"}});}` +
	`window.fetch=function(input,init){` +
	`var url=(typeof input==="string")?input:(input&&input.url);` +
	`if(!isCoin(url))return rf?rf(input,init):Promise.reject(new Error("no fetch"));` +
	`init=init||{};` +
	`var v=window.parent&&window.parent.skywireVisor;` +
	`if(!v||!v.fetchDmsg)return Promise.resolve(jr(503,{error:"skywire visor not ready"}));` +
	`var node="";try{node=localStorage.getItem("skywire-coin-node")||"";}catch(e){}` +
	`if(!node)return Promise.resolve(jr(503,{error:"no coin node configured (set localStorage skywire-coin-node to <pk>:<port>)"}));` +
	`return Promise.resolve(v.fetchDmsg(node,init.method||"GET",p(url)+q(url),init.body||null)).then(function(r){` +
	`var b=(r&&typeof r.body==="string")?r.body:(r&&r.body?new TextDecoder().decode(r.body):"");` +
	`return new Response(b,{status:(r&&r.status)||502,headers:{"Content-Type":"application/json"}});` +
	`}).catch(function(e){return jr(502,{error:String(e)});});` +
	`};` +
	`})();</script>`

func init() {
	serveCmd.Flags().StringVarP(&serveAddr, "addr", "a", ":7999", "HTTP listen address")
	serveCmd.Flags().BoolVar(&serveHarness, "harness", false, "mount the /ctl/* operator control bridge (drive the in-tab visor from a shell); DEV ONLY — never expose publicly")
	serveCmd.Flags().BoolVar(&serveTLS, "tls", false, "serve over HTTPS with a self-signed localhost cert (a real https origin for local testing — wss works, ws:// is mixed-content-blocked exactly as in prod). Accept the browser cert warning once; the cert is persisted across restarts")
	serveCmd.Flags().StringVar(&servePassword, "password", "", "gate the served PWA behind an access password (cookie login). Empty = open. Use over --tls / behind TLS so the password isn't sent in clear")
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
		// worker.js hosts the wasm runtime off the main thread; hv-boot.js loads it
		// by name (new Worker('worker.js')) and proxies skywireVisor over postMessage.
		serveBytes("/worker.js", "text/javascript", wasmhv.WorkerJS)
		serveBytes("/browse.js", "text/javascript", wasmhv.BrowseJS)
		serveBytes("/autoupdate.js", "text/javascript", wasmhv.AutoUpdateJS)
		// PWA: manifest + icons make the served page installable; sw.js gives it
		// offline launch. Harmless to always serve these (they're only ACTIVATED
		// when injectBoot links them, which it skips under --harness). The service
		// worker must be no-store so a worker update is seen promptly.
		serveBytes("/manifest.webmanifest", "application/manifest+json", wasmhv.PWAManifest)
		serveBytes("/icon-192.png", "image/png", wasmhv.PWAIcon192)
		serveBytes("/icon-512.png", "image/png", wasmhv.PWAIcon512)
		// Stamp the build fingerprint into the worker so every new binary ships a
		// byte-different sw.js → the browser detects a new worker and re-precaches
		// the fresh shell (CACHE_VERSION embeds __BUILD__). no-store so the worker
		// update is checked promptly.
		swJS := bytes.ReplaceAll(wasmhv.ServiceWorkerJS, []byte("__BUILD__"), []byte(wasmVer))
		mux.HandleFunc("/sw.js", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/javascript")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(swJS) //nolint:errcheck // best-effort
		})
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

		// Skycoin-web thin-client wallet, bundled into the PWA (make embed-wallet)
		// and served under /wallet/ from the embedded static/wallet tree. The
		// vendored dist ships <base href="/"> (skycoin's own root deployment);
		// rewrite it to /wallet/ so the wallet's relative asset paths resolve under
		// this mount. Only registered when the wallet is actually embedded.
		if walletIndex, werr := fs.ReadFile(uiFS, "wallet/index.html"); werr == nil {
			// Rewrite the base href AND inject the dmsg fetch shim right after it,
			// so the shim runs before the wallet's own scripts. The shim overrides
			// window.fetch (the wallet uses Angular's fetch backend) to route the
			// node API (/api/v1|v2) over dmsg via the parent PWA's visor. This
			// works in every mode (no Service Worker needed — so it also works
			// under `hv serve --harness` and file:// single-file), unlike the
			// origin-wide SW interception.
			walletIndex = bytes.Replace(walletIndex,
				[]byte(`<base href="/">`),
				[]byte(`<base href="/wallet/">`+walletDmsgFetchShim), 1)
			mux.HandleFunc("/wallet/", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/wallet/" || r.URL.Path == "/wallet/index.html" {
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					w.Header().Set("Cache-Control", "no-cache")
					_, _ = w.Write(walletIndex) //nolint:errcheck // best-effort
					return
				}
				fileServer.ServeHTTP(w, r) // /wallet/<asset> → static/wallet/<asset>
			})
		}

		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" || r.URL.Path == "/index.html" {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "no-cache")
				_, _ = w.Write(index) //nolint:errcheck // best-effort write to the client
				return
			}
			fileServer.ServeHTTP(w, r)
		})

		var handler http.Handler = mux
		if servePassword != "" {
			handler = passwordGate(mux, servePassword)
			cmd.Println("access-password gate enabled (--password)")
		}
		srv := &http.Server{
			Addr:              serveAddr,
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
		}
		if serveTLS {
			cert, err := localhostTLSCert()
			if err != nil {
				cmd.PrintErrln("tls cert:", err)
				os.Exit(1)
			}
			srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
			cmd.Printf("serving standalone wasm-visor over HTTPS (self-signed localhost cert) on %s — open https://localhost%s and accept the cert warning once\n", serveAddr, serveAddr)
			if err := srv.ListenAndServeTLS("", ""); err != nil {
				cmd.PrintErrln("serve:", err)
				os.Exit(1)
			}
			return
		}
		cmd.Printf("serving standalone wasm-visor (ui + %d-byte wasm + hv-boot) on %s — reverse-proxy this with Caddy\n", len(wasm), serveAddr)
		if err := srv.ListenAndServe(); err != nil {
			cmd.PrintErrln("serve:", err)
			os.Exit(1)
		}
	},
}

// localhostTLSCert returns a self-signed cert for localhost / 127.0.0.1 / ::1,
// used by `hv serve --tls` to present a real https:// origin for local testing.
// It is persisted under the temp dir and reused across restarts (so the browser
// only has to accept it once) and regenerated when missing or expired. NOT for
// production — a real deployment terminates TLS at a CA-fronted reverse proxy.
func localhostTLSCert() (tls.Certificate, error) {
	dir := filepath.Join(os.TempDir(), "skywire-hv-serve-tls")
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")

	if c, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		if leaf, e := x509.ParseCertificate(c.Certificate[0]); e == nil && time.Now().Before(leaf.NotAfter) {
			return c, nil
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
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
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
		_ = os.WriteFile(certPath, certPEM, 0o600) //nolint:errcheck // cache best-effort; in-memory cert is returned regardless
		_ = os.WriteFile(keyPath, keyPEM, 0o600)   //nolint:errcheck
	}
	return tls.X509KeyPair(certPEM, keyPEM)
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
	} else {
		// PWA: link the manifest + theme color and register the service worker so
		// the served page is installable + offline-capable. Skipped under --harness
		// so a dev's browser never caches the wasm/UI across rebuilds.
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

// passwordGate wraps h behind a cookie-based access-password gate for the served
// PWA. The cookie value is the access token (sha256 of a fixed-prefix + password);
// POST /__login with the right password sets it. This is NOT user-management (the
// hypervisor handles that) — just one shared access password so a deployed
// standalone PWA isn't open to anyone who has the URL. Use behind TLS.
func passwordGate(h http.Handler, password string) http.Handler {
	tok := func(p string) string { s := sha256.Sum256([]byte("skywire-hv:" + p)); return hex.EncodeToString(s[:]) }
	token := tok(password)
	const cookieName = "skywire-hv-auth"
	loginPage := func(w http.ResponseWriter, msg string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprintf(w, `<!doctype html><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1"><title>skywire</title><body style="background:#0e0c14;color:#cdd2da;font:14px/1.5 system-ui;display:flex;min-height:90vh;align-items:center;justify-content:center"><form method=post action=/__login style="background:#15131c;border:1px solid #2a2342;border-radius:10px;padding:2em;max-width:300px"><h2 style="color:#9d7cff;margin-top:0">skywire</h2><p>%s</p><input name=p type=password placeholder=password autofocus style="width:100%%;box-sizing:border-box;padding:.6em;background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;border-radius:6px"><button style="margin-top:1em;width:100%%;padding:.6em;background:#9d7cff;color:#0e0c14;border:0;border-radius:6px;font-weight:bold;cursor:pointer">enter</button></form></body>`, html.EscapeString(msg)) //nolint:errcheck
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__login" && r.Method == http.MethodPost {
			// Cap the login POST body — a password form is tiny; this stops a
			// crafted request from forcing unbounded form parsing (gosec G120).
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			if r.ParseForm() == nil && tok(r.FormValue("p")) == token {
				// Secure is conditional (serveTLS): the gate also serves on
				// plain-http localhost, where forcing Secure would drop the
				// cookie. HttpOnly + SameSite=Strict are always set.
				http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: serveTLS, MaxAge: 7 * 24 * 3600}) //nolint:gosec
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
			loginPage(w, "wrong password")
			return
		}
		if c, err := r.Cookie(cookieName); err == nil && c.Value == token {
			h.ServeHTTP(w, r)
			return
		}
		loginPage(w, "access password required")
	})
}
