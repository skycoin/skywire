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

// Notification coalescing tags. Events sharing a tag replace one another in a
// sink that supports it (an Android notification tag) instead of stacking.
//
// Only two events in this wrapper are worth notifying, because only these
// reach the skywire side at all: the market dial (which the wrapper performs,
// and which can outlive the user's attention because dmsg is slow) and an
// abnormal engine exit. Everything a trader would actually want —
// order filled, trade executed — is invisible here: the market protocol is
// strictly request/response with no unsolicited frames, the engine exposes no
// event stream, and order status is discovered only by the UI polling in the
// browser. A disconnect is equally invisible: the engine closes the conn
// internally and never tells the wrapper.
const (
	notifyTagMarket    = "skydex-market"
	notifyTagLifecycle = "skydex-lifecycle"
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
		// Deliberately NOT notified: parsing is instantaneous, so the user is
		// still looking at the connect form they just submitted, and the engine
		// already renders this error inline there.
		return nil, errors.New("invalid market public key")
	}
	raw, err := d.appCl.Dial(appnet.Addr{
		Net:    appnet.TypeDmsg,
		PubKey: pk,
		Port:   d.marketPort,
	})
	if err != nil {
		// The transport failed — worth surfacing because a connect can be
		// started from the UI and then left (backgrounded on a phone), so
		// nobody may be looking at the screen when it fails.
		d.appCl.NotifyOrLog("SkyDEX", "Could not reach market "+shortPK(ref), notifyTagMarket)
		return nil, fmt.Errorf("dial market: %w", err)
	}
	// Same tag as the failure above so a sink that coalesces (Android) replaces
	// a stale "could not reach" rather than stacking a second entry.
	d.appCl.NotifyOrLog("SkyDEX", "Connected to market "+shortPK(ref), notifyTagMarket)
	return skymarket.NewConn(raw), nil
}

// shortPK renders a market public key as a compact 8…4 label for a
// notification body — a full 66-char hex key is unreadable in a toast.
func shortPK(pk string) string {
	if len(pk) >= 14 {
		return pk[:8] + "…" + pk[len(pk)-4:]
	}
	return pk
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
		// The trigger is the engine returning while we still wanted it running
		// — NOT err != nil. The engine swallows its own fatal errors (a UI-port
		// bind failure is logged and Run still returns nil), so gating on err
		// would make this dead code and the app would vanish seconds after
		// start looking like a clean exit. ctx.Err() is the honest test: nil
		// means nobody asked it to stop. It also settles the shutdown race —
		// when ctx is canceled both this arm and ctx.Done() are ready, and if
		// select happens to pick this one we must stay quiet.
		//
		// Body stays generic: engine errors can carry local paths.
		if ctx.Err() == nil {
			appCl.NotifyOrLog("SkyDEX", "Trading client stopped unexpectedly", notifyTagLifecycle)
		}
	case <-ctx.Done():
		appCl.LogInfo("Context canceled, shutting down...")
	}
	cancel()
	appCl.SetStatusOrLog(appserver.AppDetailedStatusStopped)

	return nil
}
