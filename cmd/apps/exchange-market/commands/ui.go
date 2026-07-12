// Package commands cmd/apps/exchange-market/commands/ui.go
package commands

import (
	"context"
	"net/http"
	"time"

	"github.com/skycoin/skywire/internal/exchange-market/app"
)

// serveUI runs the market operator UI on addr until ctx is canceled.
//
// The full operator UI (explorer/fullnode/wallet configuration and
// order/product/ban monitoring — see exchange-design.md §2) is not built yet;
// for now this serves a minimal status page so the address is reachable and the
// hypervisor "open UI" link resolves. It is replaced by the real UI later.
func serveUI(ctx context.Context, appCl *app.Client, addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(marketUIPlaceholder)) //nolint:errcheck
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

const marketUIPlaceholder = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Exchange Market</title>
  <style>
    body { background:#101F34; color:#fff; font-family:system-ui,sans-serif;
           display:flex; align-items:center; justify-content:center; height:100vh; margin:0; }
    .card { text-align:center; }
    h1 { color:#0273FF; margin-bottom:.25rem; }
    p { opacity:.8; }
  </style>
</head>
<body>
  <div class="card">
    <h1>Exchange Market</h1>
    <p>The market backend is running.</p>
    <p>Operator UI (configuration &amp; monitoring) coming soon.</p>
  </div>
</body>
</html>`
