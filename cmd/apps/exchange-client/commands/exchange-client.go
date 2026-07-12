// Package commands cmd/apps/exchange-client/commands/exchange-client.go
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

	"github.com/skycoin/skywire/internal/exchange-client/app"
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
	// marketPK is the public key of the market to connect to (optional; the
	// user can also set it from the UI settings once wired up).
	marketPK string
)

func init() {
	launcher.RegisterApp(skyenv.ExchangeClientName, RunExchangeClient)
	RootCmd.Flags().StringVar(&uiAddr, "addr", skyenv.ExchangeClientAddr, "address to serve the trading UI on")
	RootCmd.Flags().Uint16Var(&port, "port", 0, "routing port for communication between app and visor")
	RootCmd.Flags().StringVar(&marketPK, "market", "", "public key of the market to connect to")
}

// RootCmd is the root command for exchange-client.
var RootCmd = &cobra.Command{
	Use:                   "exchange-client",
	Short:                 "Skywire decentralized exchange client (trading UI)",
	Long:                  calvin.AsciiFont("exchange-client"),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, args []string) {
		ctx := context.Background()
		if err := RunExchangeClient(ctx, args); err != nil {
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

// RunExchangeClient runs the exchange-client app: it connects to the visor,
// serves the embedded trading UI on the configured address, and (later) dials
// the market over dmsg to proxy the UI's API calls.
func RunExchangeClient(ctx context.Context, args []string) error {
	if len(args) > 0 {
		fs := pflag.NewFlagSet("exchange-client", pflag.ContinueOnError)
		fs.StringVar(&uiAddr, "addr", skyenv.ExchangeClientAddr, "trading UI address")
		fs.Uint16Var(&port, "port", 0, "routing port")
		fs.StringVar(&marketPK, "market", "", "market public key")
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

	// Serve the embedded single-page trading UI.
	go serveUI(ctx, appCl, uiAddr)

	appCl.LogInfo("Exchange client UI available at http://%s", uiAddr)
	appCl.SetStatusOrLog(appserver.AppDetailedStatusRunning)

	termCh := make(chan os.Signal, 1)
	signal.Notify(termCh, os.Interrupt)

	select {
	case <-termCh:
		appCl.LogInfo("Received interrupt signal, shutting down gracefully...")
	case <-ctx.Done():
		appCl.LogInfo("Context canceled, shutting down...")
	}
	appCl.SetStatusOrLog(appserver.AppDetailedStatusStopped)
	return nil
}

// serveUI serves the embedded SPA on addr until ctx is canceled. Any path that
// isn't a real asset falls back to index.html so client-side routes resolve.
func serveUI(ctx context.Context, appCl *app.Client, addr string) {
	distFS, err := fs.Sub(uiFS, "static")
	if err != nil {
		appCl.LogError("failed to open embedded UI: %v", err)
		return
	}
	fileServer := http.FileServer(http.FS(distFS))

	mux := http.NewServeMux()
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
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx) //nolint:errcheck
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		appCl.LogError("UI server stopped: %v", err)
	}
}
