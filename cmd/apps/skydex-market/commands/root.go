// Package commands cmd/apps/skydex-market/commands/root.go
package commands

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/internal/skydex-market/app"
	"github.com/skycoin/skywire/internal/skydex-market/chain"
	"github.com/skycoin/skywire/internal/skydex-market/db"
	"github.com/skycoin/skywire/internal/skydex-market/jobs"
	"github.com/skycoin/skywire/internal/skydex-market/server"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
)

var (
	// dbPath is the path to the SQLite database file
	dbPath string
	// port is the routing port the market listens on (dmsg). 0 = use the
	// visor-assigned routing port.
	port uint16
	// uiAddr is the localhost address the operator UI is served on.
	uiAddr string
)

func init() {
	launcher.RegisterApp(skyenv.SkydexMarketName, RunSkydexMarket)
	RootCmd.Flags().StringVar(&dbPath, "db", "", "path to SQLite database file (default: <work-dir>/skydex-market.db)")
	RootCmd.Flags().Uint16Var(&port, "port", 0, "routing port for communication between app and visor")
	RootCmd.Flags().StringVar(&uiAddr, "addr", skyenv.SkydexMarketAddr, "address to serve the operator UI on")
}

// RootCmd is the root command for skydex-market
var RootCmd = &cobra.Command{
	Use:                   "skydex-market",
	Short:                 "SkyDEX - Market (Skywire decentralized exchange backend)",
	Long:                  calvin.AsciiFont("skydex-market"),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, args []string) {
		ctx := context.Background()
		if err := RunSkydexMarket(ctx, args); err != nil {
			log.Fatal(err)
		}
	},
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}

// RunSkydexMarket runs the skydex-market app logic.
func RunSkydexMarket(ctx context.Context, args []string) error {
	// Parse flags when called via internal launcher
	if len(args) > 0 {
		fs := pflag.NewFlagSet("skydex-market", pflag.ContinueOnError)
		fs.StringVar(&dbPath, "db", "", "path to SQLite database file")
		fs.Uint16Var(&port, "port", 0, "routing port")
		fs.StringVar(&uiAddr, "addr", skyenv.SkydexMarketAddr, "operator UI address")
		if err := fs.Parse(args); err != nil {
			return err
		}
	}

	// Wrap context with cancel to allow graceful shutdown
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Initialize app client (connects to visor)
	appCl := app.NewClient()
	defer appCl.Close()

	appCl.LogInfo("Build info: %s", buildinfo.Version())
	appCl.LogInfo("Successfully started skydex-market.")
	appCl.SetStatusOrLog(appserver.AppDetailedStatusStarting)

	// Initialize database
	database, err := db.New(dbPath, appCl.WorkDir())
	if err != nil {
		appCl.SetErrorOrLog(err)
		return err
	}
	defer database.Close() //nolint

	appCl.LogInfo("Database initialized successfully at: %s", database.Path())

	// Run database migrations
	if err := database.Migrate(); err != nil {
		appCl.SetErrorOrLog(err)
		return err
	}
	appCl.LogInfo("Database migrations completed.")

	// Initialize default market config if not exists
	if err := database.InitDefaultConfig(); err != nil {
		appCl.SetErrorOrLog(err)
		return err
	}
	appCl.LogInfo("Market configuration initialized.")

	// Resolve the dmsg listen port: default to the visor-assigned routing port,
	// overridden by --port when set (mirrors vpn-client / skychat).
	listenPort := routing.Port(appCl.RoutingPort())
	if port != 0 {
		listenPort = routing.Port(port)
		appCl.Client.SetAppPortOrLog(listenPort)
	}

	// Start the dmsg transport server. The market Listens on appnet.TypeDmsg
	// via the visor; clients Dial the market's public key on the same port.
	srv := server.New(database, appCl.Log(), listenPort)
	go func() {
		if err := srv.ListenAndServe(ctx, appCl); err != nil {
			appCl.LogError("dmsg server stopped: %v", err)
			appCl.SetErrorOrLog(err)
			cancel()
		}
	}()

	// Serve the operator UI (config + monitoring) on the localhost address.
	go serveUI(ctx, appCl, database, uiAddr)

	// Start the background jobs (escrow/listing checks, expiry, cleanup, bans).
	// The chain backend reads its per-sell-coin escrow config and per-currency
	// explorer config from the DB, so operator changes take effect without a
	// restart. Coins with no fullnode URL yet simply never confirm.
	chainBackend := chain.New(database)
	if coins, _ := database.AvailableSellCoins(); len(coins) > 0 { //nolint
		appCl.LogInfo("Chain backend ready — sell coins: %s (external payments via configured explorers)", strings.Join(coins, ", "))
	} else {
		appCl.LogInfo("Chain backend ready — no sell coins enabled yet (add one in the operator UI)")
	}
	go jobs.NewRunner(database, chainBackend, appCl.Log()).Run(ctx)

	appCl.LogInfo("SkyDEX - Market is ready and waiting for client connections.")
	appCl.LogInfo("Operator UI available at http://%s", uiAddr)
	appCl.SetStatusOrLog(appserver.AppDetailedStatusRunning)

	// Handle shutdown signals
	termCh := make(chan os.Signal, 1)
	signal.Notify(termCh, os.Interrupt)

	// Give the app a moment to stabilize before declaring ready
	time.Sleep(100 * time.Millisecond)

	select {
	case <-termCh:
		appCl.LogInfo("Received interrupt signal, shutting down gracefully...")
		appCl.SetStatusOrLog(appserver.AppDetailedStatusStopped)
		cancel()
	case <-ctx.Done():
		appCl.LogInfo("Context canceled, shutting down...")
		appCl.SetStatusOrLog(appserver.AppDetailedStatusStopped)
	}

	return nil
}
