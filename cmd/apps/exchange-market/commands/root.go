// Package commands cmd/apps/exchange-market/commands/root.go
package commands

import (
	"context"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/internal/exchange-market/app"
	"github.com/skycoin/skywire/internal/exchange-market/db"
	"github.com/skycoin/skywire/internal/exchange-market/protocol"
	"github.com/skycoin/skywire/internal/exchange-market/server"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
)

var (
	// dbPath is the path to the SQLite database file
	dbPath string
	// port is the routing port for communication between app and visor
	port uint16
)

func init() {
	launcher.RegisterApp("exchange-market", RunExchangeMarket)
	RootCmd.Flags().StringVar(&dbPath, "db", "", "path to SQLite database file (default: <work-dir>/exchange-market.db)")
	RootCmd.Flags().Uint16Var(&port, "port", 0, "routing port for communication between app and visor")
}

// RootCmd is the root command for exchange-market
var RootCmd = &cobra.Command{
	Use:                   "exchange-market",
	Short:                 "Skywire decentralized exchange market backend",
	Long:                  calvin.AsciiFont("exchange-market"),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, args []string) {
		ctx := context.Background()
		if err := RunExchangeMarket(ctx, args); err != nil {
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

// RunExchangeMarket runs the exchange-market app logic.
func RunExchangeMarket(ctx context.Context, args []string) error {
	// Parse flags when called via internal launcher
	if len(args) > 0 {
		fs := pflag.NewFlagSet("exchange-market", pflag.ContinueOnError)
		fs.StringVar(&dbPath, "db", "", "path to SQLite database file")
		fs.Uint16Var(&port, "port", 0, "routing port")
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
	appCl.LogInfo("Successfully started exchange-market.")
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

	// Start the dmsg transport server. The market Listens on appnet.TypeDmsg
	// via the visor; clients Dial the market's public key on the same port.
	srv := server.New(database, appCl.Log(), protocol.Port(port))
	go func() {
		if err := srv.ListenAndServe(ctx, appCl); err != nil {
			appCl.LogError("dmsg server stopped: %v", err)
			appCl.SetErrorOrLog(err)
			cancel()
		}
	}()

	// TODO: Start background jobs (in next phase)

	appCl.LogInfo("Exchange market is ready and waiting for client connections.")
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
