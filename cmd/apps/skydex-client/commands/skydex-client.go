// Package commands cmd/apps/skydex-client/commands/skydex-client.go
package commands

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/internal/skydex-client/app"
	"github.com/skycoin/skywire/internal/skydex-market/protocol"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
)

//go:embed static
var uiFS embed.FS

var (
	// uiAddr is the localhost address the trading UI is served on.
	uiAddr string
	// port is the app routing port (source port for dmsg dials to the market).
	// 0 = use the visor-assigned routing port.
	port uint16
	// marketPK is the default market public key shown pre-filled in the UI's
	// connect screen. The client does NOT connect automatically; the user
	// confirms/edits it and clicks Connect.
	marketPK string
	// marketPort is the market's dmsg routing port the client dials. It must
	// match the market's --port; defaults to the protocol default (8050).
	marketPort uint16
)

func init() {
	launcher.RegisterApp(skyenv.SkydexClientName, RunSkydexClient)
	RootCmd.Flags().StringVar(&uiAddr, "addr", skyenv.SkydexClientAddr, "address to serve the trading UI on")
	RootCmd.Flags().Uint16Var(&port, "port", 0, "routing port for communication between app and visor")
	RootCmd.Flags().StringVar(&marketPK, "market-pk", "", "default skydex-market public key (pre-filled in the UI; not auto-connected)")
	RootCmd.Flags().Uint16Var(&marketPort, "market-port", uint16(protocol.DefaultPort), "market dmsg routing port to dial (must match the market's --port)")
}

// RootCmd is the root command for skydex-client.
var RootCmd = &cobra.Command{
	Use:                   "skydex-client",
	Short:                 "SkyDEX - Client (Skywire decentralized exchange trading UI)",
	Long:                  calvin.AsciiFont("skydex-client"),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, args []string) {
		ctx := context.Background()
		if err := RunSkydexClient(ctx, args); err != nil {
			log.Fatal(err)
		}
	},
}

// Execute executes the root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}

// RunSkydexClient runs the skydex-client app: it connects to the visor,
// serves the embedded trading UI on the configured address, and (later) dials
// the market over dmsg to proxy the UI's API calls.
func RunSkydexClient(ctx context.Context, args []string) error {
	if len(args) > 0 {
		fs := pflag.NewFlagSet("skydex-client", pflag.ContinueOnError)
		fs.StringVar(&uiAddr, "addr", skyenv.SkydexClientAddr, "trading UI address")
		fs.Uint16Var(&port, "port", 0, "routing port")
		fs.StringVar(&marketPK, "market-pk", "", "default market public key")
		fs.Uint16Var(&marketPort, "market-port", uint16(protocol.DefaultPort), "market dmsg routing port to dial")
		if err := fs.Parse(args); err != nil {
			return err
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Connect to the visor. A nil config makes app.NewClient read the proc
	// config the visor injects via the environment.
	appCl := app.NewClient()
	defer appCl.Close()

	appCl.LogInfo("Build info: %s", buildinfo.Version())
	appCl.SetStatusOrLog(appserver.AppDetailedStatusStarting)

	// Honor a --port override for the app's routing port.
	if port != 0 {
		appCl.Client.SetAppPortOrLog(routing.Port(port))
	}

	// The session drives the (manual) connection to a market over dmsg. It is
	// seeded with the configured default market public key but does NOT connect
	// until the user clicks Connect in the UI.
	sess := newSession(appCl, marketPK, protocol.Port(marketPort))
	defer sess.close()

	// Serve the embedded single-page trading UI plus the local control API.
	go serveUI(ctx, appCl, sess, uiAddr)

	appCl.LogInfo("SkyDEX - Client UI available at http://%s", uiAddr)
	appCl.SetStatusOrLog(appserver.AppDetailedStatusRunning)

	// Keep asserting "Running" while the client is up. Unlike the market (which
	// listens) or other client apps (which hold a session), the SkyDEX client
	// has no persistent skywire connection while idle — it only dials a market on
	// demand — so the visor can't infer "running" from a connection summary. The
	// visor also writes a one-time "Starting" right after launching the app (see
	// the hypervisor start handler); because this app reports Running very early,
	// that late write can overwrite it and leave the app stuck showing
	// "connecting" (orange) in the manager UI. A light periodic re-assert keeps the
	// status correct and self-heals any such overwrite.
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				appCl.SetStatusOrLog(appserver.AppDetailedStatusRunning)
			}
		}
	}()

	termCh := make(chan os.Signal, 1)
	signal.Notify(termCh, os.Interrupt)

	select {
	case <-termCh:
		appCl.LogInfo("Received interrupt signal, shutting down gracefully...")
	case <-ctx.Done():
		appCl.LogInfo("Context canceled, shutting down...")
	}
	// Stop the Running heartbeat before reporting Stopped so it can't re-assert
	// Running afterwards.
	cancel()
	appCl.SetStatusOrLog(appserver.AppDetailedStatusStopped)
	return nil
}

// serveUI serves the embedded SPA plus the local control API on addr until ctx
// is canceled. Any path that isn't a real asset falls back to index.html so
// client-side routes resolve.
func serveUI(ctx context.Context, appCl *app.Client, sess *session, addr string) {
	distFS, err := fs.Sub(uiFS, "static")
	if err != nil {
		appCl.LogError("failed to open embedded UI: %v", err)
		return
	}
	fileServer := http.FileServer(http.FS(distFS))

	mux := http.NewServeMux()
	registerAPI(mux, sess)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if _, statErr := fs.Stat(distFS, name); statErr != nil {
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
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
		appCl.LogError("UI server stopped: %v", err)
	}
}
