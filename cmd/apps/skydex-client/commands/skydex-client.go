// Package commands cmd/apps/skydex-client/commands/skydex-client.go
//
// Thin skywire wrapper around the SkyDEX trading client, whose engine and UI
// live in the skycoin repo (github.com/skycoin/skycoin/cmd/skydex-client/
// commands) with no skywire dependency. The wrapper supplies the transport: a
// MarketDialer that dials the market's public key over appnet/dmsg via the
// visor and hands the framed connection to the engine.
package commands

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	skydexclient "github.com/skycoin/skycoin/cmd/skydex-client/commands"
	skymarket "github.com/skycoin/skycoin/src/skydex/market"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/internal/skydex-client/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
)

var (
	// uiAddr is the localhost address the trading UI is served on.
	uiAddr string
	// port is the app routing port (source port for dmsg dials to the market).
	// 0 = use the visor-assigned routing port.
	port uint16
	// marketPK is the default market public key shown pre-filled in the UI's
	// connect screen. The client does NOT connect automatically.
	marketPK string
	// marketPort is the market's dmsg routing port the client dials. It must
	// match the market's --port; defaults to skyenv.SkydexMarketPort.
	marketPort uint16
)

func init() {
	launcher.RegisterApp(skyenv.SkydexClientName, RunSkydexClient)
	RootCmd.Flags().StringVar(&uiAddr, "addr", skyenv.SkydexClientAddr, "address to serve the trading UI on")
	RootCmd.Flags().Uint16Var(&port, "port", 0, "routing port for communication between app and visor")
	RootCmd.Flags().StringVar(&marketPK, "market-pk", "", "default skydex-market public key (pre-filled in the UI; not auto-connected)")
	RootCmd.Flags().Uint16Var(&marketPort, "market-port", skyenv.SkydexMarketPort, "market dmsg routing port to dial (must match the market's --port)")
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
		if err := RunSkydexClient(context.Background(), args); err != nil {
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

// appnetDialer is the skywire MarketDialer: it dials the market's public key
// over appnet/dmsg via the visor and wraps the connection in exchange framing.
type appnetDialer struct {
	appCl      *app.Client
	marketPort routing.Port
}

func (d appnetDialer) Dial(ref string) (*skymarket.Conn, error) {
	var pk cipher.PubKey
	if err := pk.UnmarshalText([]byte(ref)); err != nil {
		return nil, errors.New("invalid market public key")
	}
	raw, err := d.appCl.Dial(appnet.Addr{
		Net:    appnet.TypeDmsg,
		PubKey: pk,
		Port:   d.marketPort,
	})
	if err != nil {
		return nil, fmt.Errorf("dial market: %w", err)
	}
	return skymarket.NewConn(raw), nil
}

// RunSkydexClient runs the skydex-client app: it serves the trading UI (from the
// skycoin engine) and dials the market over dmsg on demand.
func RunSkydexClient(ctx context.Context, args []string) error {
	if len(args) > 0 {
		fs := pflag.NewFlagSet("skydex-client", pflag.ContinueOnError)
		fs.StringVar(&uiAddr, "addr", skyenv.SkydexClientAddr, "trading UI address")
		fs.Uint16Var(&port, "port", 0, "routing port")
		fs.StringVar(&marketPK, "market-pk", "", "default market public key")
		fs.Uint16Var(&marketPort, "market-port", skyenv.SkydexMarketPort, "market dmsg routing port to dial")
		if err := fs.Parse(args); err != nil {
			return err
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	appCl := app.NewClient()
	defer appCl.Close()

	appCl.LogInfo("Build info: %s", buildinfo.Version())
	appCl.SetStatusOrLog(appserver.AppDetailedStatusStarting)

	if port != 0 {
		appCl.Client.SetAppPortOrLog(routing.Port(port))
	}

	dialer := appnetDialer{appCl: appCl, marketPort: routing.Port(marketPort)}

	// Keep asserting "Running" while the client is up. The SkyDEX client has no
	// persistent skywire connection while idle — it only dials a market on demand
	// — so the visor can't infer "running" from a connection summary, and the
	// visor's late one-time "Starting" write can otherwise leave it stuck orange.
	appCl.SetStatusOrLog(appserver.AppDetailedStatusRunning)
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

	cfg := skydexclient.Config{UIAddr: uiAddr, DefaultPK: marketPK}
	errCh := make(chan error, 1)
	go func() { errCh <- skydexclient.Run(ctx, cfg, dialer, appCl.Log()) }()

	termCh := make(chan os.Signal, 1)
	signal.Notify(termCh, os.Interrupt)

	select {
	case <-termCh:
		appCl.LogInfo("Received interrupt signal, shutting down gracefully...")
	case err := <-errCh:
		if err != nil {
			appCl.LogError("skydex-client engine stopped: %v", err)
		}
	case <-ctx.Done():
		appCl.LogInfo("Context canceled, shutting down...")
	}
	cancel()
	appCl.SetStatusOrLog(appserver.AppDetailedStatusStopped)

	return nil
}
