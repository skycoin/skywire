// Package dmsghttp pkg/dmsghttp/debug.go
package dmsghttp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"time"

	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

// DefaultDebugPort is the dmsg port used for serving debug/pprof endpoints.
const DefaultDebugPort = uint16(81)

// DebugMux returns an http.ServeMux with standard pprof endpoints registered.
func DebugMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	for _, p := range []string{"heap", "goroutine", "threadcreate", "block", "mutex", "allocs"} {
		mux.Handle("/debug/pprof/"+p, pprof.Handler(p))
	}
	return mux
}

// WhitelistMiddleware wraps an http.Handler with public-key-based access control.
// When serving over dmsg, RemoteAddr is in the format "<pk>:<port>".
// If whitelistedPKs is empty, all requests are allowed.
func WhitelistMiddleware(whitelistedPKs []cipher.PubKey, next http.Handler) http.Handler {
	if len(whitelistedPKs) == 0 {
		return next
	}
	allowed := make(map[string]struct{}, len(whitelistedPKs))
	for _, pk := range whitelistedPKs {
		allowed[pk.String()] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
func ServeDebug(ctx context.Context, dmsgC *dmsg.Client, log *logging.Logger, whitelistPKs []cipher.PubKey) error {
	handler := WhitelistMiddleware(whitelistPKs, DebugMux())

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
