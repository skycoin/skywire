// Package commands cmd/apps/exchange-market/commands/ui.go
package commands

import (
	"context"
	"embed"
	"net/http"
	"time"

	"github.com/skycoin/skywire/internal/exchange-market/app"
	"github.com/skycoin/skywire/internal/exchange-market/db"
)

//go:embed static
var uiFS embed.FS

// serveUI runs the market operator UI (configuration + monitoring) and its
// operator API on addr until ctx is canceled. See exchange-design.md §2.
func serveUI(ctx context.Context, appCl *app.Client, database *db.Database, addr string) {
	mux := http.NewServeMux()

	// The operator API is gated by a one-time code the market publishes to the
	// visor (visible on the hypervisor app list). Register the API on its own
	// mux so a single wrapper covers every route — including any added later,
	// which is the point: a new handler is authenticated by default.
	gate, err := newAuthGate(appCl.SetOTPOrLog)
	if err != nil {
		appCl.LogError("failed to initialize operator auth: %v", err)
		return
	}

	apiMux := http.NewServeMux()
	registerOperatorAPI(apiMux, database, appCl)

	// Exact patterns beat the "/api/" prefix in net/http's mux, so login stays
	// reachable without a token while everything else requires one.
	mux.Handle("/api/login", gate.loginHandler())
	mux.Handle("/api/", gate.requireAuth(apiMux))

	// Serve the embedded single-page operator UI. It is a single index.html, so
	// any non-/api path returns it.
	index, err := uiFS.ReadFile("static/index.html")
	if err != nil {
		appCl.LogError("failed to load operator UI: %v", err)
		return
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index) //nolint:errcheck
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() { //nolint
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx) //nolint:errcheck
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		appCl.LogError("operator UI server stopped: %v", err)
	}
}
