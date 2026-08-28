package realorigin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Config describes the browse-origin server: the static half of the substrate.
//
// It never proxies content. It serves the service worker at one path and the
// bootstrap shell at every other, and that is all. Every fetch the rendered page
// makes is relayed to the visitor's own app tab and satisfied by the transport
// there — so a public deployment of this carries, sees and stores none of the
// traffic, and nothing about it scales with how much anyone browses.
type Config struct {
	// Addr is the listen address for ListenAndServe. Ignored by Handler.
	Addr string

	// Suffix is the browse-origin domain suffix, with or without a leading dot
	// (".mesh.localhost" locally, or a registrable domain you hold). Required.
	//
	// Hosted, this must be a DIFFERENT registrable domain from the app's, not a
	// subdomain of it: different hosts make the two cross-origin, but only
	// different registrable domains make untrusted content fully cross-site from
	// the app.
	Suffix string

	// AppOrigin is the origin of the app that holds the credentials and answers
	// the handshake, e.g. "https://app.example". Required.
	AppOrigin string

	// SWPath is where the service worker is served. Defaults to "/sw.js". It must
	// stay at the root of the origin, since the worker claims the whole scope.
	SWPath string

	// Shell replaces the bootstrap shell served for navigations. Empty uses the
	// built-in one, which is deliberately plain: it says what is happening and
	// prints whatever progress the transport reports, and no more.
	//
	// Override it when the wait is worth dressing — a slow transport that sets up
	// a route before it can fetch anything has a real story to tell, and a bare
	// spinner wastes it. A replacement must speak the same bridge protocol:
	// handshake to window.parent with {type:'realorigin-hello', shortid}, register
	// the worker, relay 'realorigin-fetch' between the worker and the app, and
	// write the response into the document. Start from web/bootstrap.html.
	//
	// __APP_ORIGIN__ and __SUFFIX__ are substituted here exactly as they are in
	// the built-in shell.
	Shell []byte

	// Worker replaces the transport service worker served at SWPath. Empty uses
	// the built-in one.
	//
	// The built-in worker is deliberately small and names no transport, which is
	// what makes it auditable on the untrusted origin. A replacement inherits that
	// responsibility: it runs on B, so whatever it can reach, untrusted content can
	// reach through it. It must speak the same bridge protocol — relay each
	// non-navigation fetch to a controlling client as {type:realorigin-fetch}
	// over a MessagePort and answer with the returned {status, headers, body}.
	//
	// A worker that needs companion files (a wasm module and its loader, say)
	// serves them through Assets.
	Worker []byte

	// Assets are extra paths served verbatim on the browse origin, e.g.
	// {"/sw.wasm": mod, "/wasm_exec.js": loader}. Keys are absolute paths and win
	// over the shell; SWPath still wins over both.
	//
	// Every byte here is served to the UNTRUSTED origin, so put nothing in it that
	// the browsed content should not have.
	Assets map[string][]byte
}

const defaultSWPath = "/sw.js"

var (
	// ErrNoSuffix is returned when Config.Suffix is empty.
	ErrNoSuffix = errors.New("realorigin: Suffix is required")
	// ErrNoAppOrigin is returned when Config.AppOrigin is empty.
	ErrNoAppOrigin = errors.New("realorigin: AppOrigin is required")
)

// Handler returns the browse-origin handler.
//
// Every path except the worker's serves the same shell, which is what makes
// navigation work: the worker deliberately does not intercept navigations, so
// each one lands here and the shell fetches that path and writes it into the
// origin. Serving the shell only at "/" would break every link on every page.
func Handler(cfg Config) (http.Handler, error) {
	if normalizeSuffix(cfg.Suffix) == "" {
		return nil, ErrNoSuffix
	}
	if cfg.AppOrigin == "" {
		return nil, ErrNoAppOrigin
	}
	swPath := cfg.SWPath
	if swPath == "" {
		swPath = defaultSWPath
	}

	src := bootstrapHTML
	if len(cfg.Shell) > 0 {
		src = cfg.Shell
	}
	shell := bytes.ReplaceAll(src, []byte("__APP_ORIGIN__"), []byte(cfg.AppOrigin))
	shell = bytes.ReplaceAll(shell, []byte("__SUFFIX__"), []byte(normalizeSuffix(cfg.Suffix)))
	shell = bytes.ReplaceAll(shell, []byte("__SW_PATH__"), []byte(swPath))
	// The worker needs no substitution: it learns nothing from configuration,
	// which is the same reason it can be reused across every transport.
	worker := swJS
	if len(cfg.Worker) > 0 {
		worker = cfg.Worker
	}

	// Copy the asset map so a later mutation by the caller cannot change what an
	// already-running server hands to the untrusted origin.
	var assets map[string][]byte
	if len(cfg.Assets) > 0 {
		assets = make(map[string][]byte, len(cfg.Assets))
		for p, b := range cfg.Assets {
			if !strings.HasPrefix(p, "/") {
				p = "/" + p
			}
			if p == swPath {
				return nil, fmt.Errorf("realorigin: asset %q collides with SWPath", p)
			}
			assets[p] = b
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == swPath {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			// The worker is the one thing that must never be stale: a cached old
			// worker keeps answering with an old bridge protocol.
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write(worker) //nolint:errcheck // a short write to a hung client is the client's problem; there is no recovery here
			return
		}
		if b, ok := assets[r.URL.Path]; ok {
			w.Header().Set("Content-Type", assetContentType(r.URL.Path))
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write(b) //nolint:errcheck // as above
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(shell) //nolint:errcheck // as above
	}), nil
}

// ResponderJS returns the script to serve from the APP origin, first-party.
//
// It has to be first-party there. Put it in a cross-origin helper iframe and
// Storage Partitioning lands the helper in a different partition from the app's
// own workers, where it cannot reach the client it exists to call.
func ResponderJS() []byte { return responderJS }

// BootstrapHTML returns the built-in shell, as a starting point for a Config.Shell
// that wants a richer interstitial without reimplementing the bridge.
func BootstrapHTML() []byte { return bootstrapHTML }

// ListenAndServe runs the browse-origin server until ctx is canceled.
func (cfg Config) ListenAndServe(ctx context.Context) error {
	h, err := Handler(cfg)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		_ = srv.Close() //nolint:errcheck // shutting down on context cancellation; the error has nowhere to go
	}()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// ServiceWorkerJS returns the transport worker, for an embedder that serves the
// browse origin itself rather than through Handler — a host-routed setup where B
// and the app share one listener, say. Serve it at Config.SWPath.
func ServiceWorkerJS() []byte { return swJS }

// assetContentType maps an asset path to a content type. WebAssembly is the one
// that matters: instantiateStreaming refuses a module that does not arrive as
// application/wasm, and the failure looks like a corrupt module rather than a
// header problem.
func assetContentType(p string) string {
	switch {
	case strings.HasSuffix(p, ".wasm"):
		return "application/wasm"
	case strings.HasSuffix(p, ".js"):
		return "text/javascript; charset=utf-8"
	case strings.HasSuffix(p, ".json"):
		return "application/json"
	case strings.HasSuffix(p, ".css"):
		return "text/css; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
