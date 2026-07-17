// Package dmsghttp pkg/dmsg/dmsghttp/debug.go c1-net-dmsg
package dmsghttp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// DefaultDebugPort is the dmsg port historically used for serving debug/pprof on
// its own listener. Prefer WithDebug to fold the same endpoints onto the
// service's main dmsg :80 handler (one port for /health + /metrics + /debug/*).
const DefaultDebugPort = uint16(81)

// DebugMux returns an http.ServeMux with the standard pprof endpoints, plus
// /debug/log serving logSource() when it is non-nil — the service's recent log
// output from an in-memory ring buffer (no disk file, no -s flag).
func DebugMux(logSource func() []byte) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	for _, p := range []string{"heap", "goroutine", "threadcreate", "block", "mutex", "allocs"} {
		mux.Handle("/debug/pprof/"+p, pprof.Handler(p))
	}
	if logSource != nil {
		mux.HandleFunc("/debug/log", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write(logSource()) //nolint:errcheck
		})
	}
	return mux
}

// WithDebug returns a handler that serves the survey-gated debug endpoints
// (/debug/pprof/* and, when logSource != nil, /debug/log) on the SAME listener
// as next, delegating every other path to next. This consolidates pprof + logs
// onto the service's main dmsg :80 instead of a separate debug port (matching
// the visor's logserver). Only /debug/* is whitelist-gated; next is unchanged.
func WithDebug(next http.Handler, whitelistPKs []cipher.PubKey, logSource func() []byte) http.Handler {
	debug := WhitelistMiddleware(whitelistPKs, DebugMux(logSource))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/debug/") {
			debug.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// WhitelistMiddleware wraps an http.Handler with public-key-based access control.
// When serving over dmsg, RemoteAddr is in the format "<pk>:<port>".
//
// An empty whitelist denies every request with 401. Pprof endpoints expose
// process internals (heap, goroutine, cmdline) and let any allowed caller
// pin CPU via /debug/pprof/profile, so fail-open on misconfiguration is
// not acceptable. The stock production binary embeds a non-empty
// deployment.Prod.SurveyWhitelist; only forks or SKYDEPLOY-overridden
// services configs without a survey_whitelist field hit this path, and
// for those denying access is the safe default.
func WhitelistMiddleware(whitelistedPKs []cipher.PubKey, next http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(whitelistedPKs))
	for _, pk := range whitelistedPKs {
		allowed[pk.String()] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(allowed) == 0 {
			http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
			return
		}
		remotePK, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			http.Error(w, "500 Internal Server Error", http.StatusInternalServerError)
			return
		}
		if _, ok := allowed[remotePK]; !ok {
			http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ServeDebug serves pprof endpoints over dmsg on DefaultDebugPort, gated by the
// provided whitelist public keys. It blocks until the context is canceled or
// an error occurs.
//
// Deprecated: prefer WithDebug, which folds pprof + /debug/log onto the
// service's main dmsg :80 handler instead of a separate :81 listener.
//
// An empty whitelist is logged at WARN and the middleware will reject every
// request — see WhitelistMiddleware for rationale.
func ServeDebug(ctx context.Context, dmsgC *dmsg.Client, log *logging.Logger, whitelistPKs []cipher.PubKey) error {
	if len(whitelistPKs) == 0 {
		log.Warn("ServeDebug: empty whitelist — pprof endpoint will reject all requests (set survey_whitelist in services config to allow callers)")
	} else {
		log.WithField("whitelisted_pks", len(whitelistPKs)).Info("ServeDebug: pprof access restricted to whitelisted PKs")
	}
	handler := WhitelistMiddleware(whitelistPKs, DebugMux(nil))

	lis, err := dmsgC.Listen(DefaultDebugPort)
	if err != nil {
		return fmt.Errorf("debug dmsg listen on port %d: %w", DefaultDebugPort, err)
	}

	log.WithField("dmsg_addr", fmt.Sprintf("dmsg://%v", lis.Addr().String())).
		Info("Serving debug/pprof over dmsg")

	srv := &http.Server{
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      60 * time.Second, // pprof profile collection takes 30s
		IdleTimeout:       30 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		MaxHeaderBytes:    1 << 14, // 16KB
		Handler:           handler,
	}

	done := make(chan struct{})
	go func() { //nolint:gosec
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second) //nolint:gosec
			defer cancel()
			if shutdownErr := srv.Shutdown(shutdownCtx); shutdownErr != nil {
				log.WithError(shutdownErr).Error("debug server shutdown error")
			}
		case <-done:
		}
	}()

	err = srv.Serve(lis)
	close(done)
	return err
}
