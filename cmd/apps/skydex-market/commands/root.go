// Package commands cmd/apps/skydex-market/commands/root.go
//
// This is the thin skywire wrapper around the SkyDEX market engine, which lives
// in the skycoin repo (github.com/skycoin/skycoin/cmd/skydex-market/commands and
// src/skydex/*) and carries no skywire dependency. The wrapper's only job is to
// supply the skywire transport: it Listens on appnet.TypeDmsg via the visor,
// injects each client's authenticated dmsg public key as its identity, and hands
// the listener to the engine's Run. The engine does everything else (SQLite
// store, matching/escrow, jobs, operator UI). This mirrors how skywire wraps
// skycoin-web.
package commands

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"

	"github.com/sirupsen/logrus"
	skydexmarket "github.com/skycoin/skycoin/cmd/skydex-market/commands"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/internal/skydex-market/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
)

var (
	// dbPath is the path to the SQLite database file.
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

// RootCmd is the root command for skydex-market.
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
		if err := RunSkydexMarket(context.Background(), args); err != nil {
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

// appHost adapts the visor app client to the engine's Host: it supplies the
// logger, publishes the operator OTP to the visor app list, and reports the
// market's own identity (the visor public key).
type appHost struct{ appCl *app.Client }

func (h appHost) Log() logrus.FieldLogger { return h.appCl.Log() }
func (h appHost) PublishOTP(otp string)   { h.appCl.SetOTPOrLog(otp) }
func (h appHost) PubKey() string          { return h.appCl.VisorPubKey() }

// RunSkydexMarket runs the skydex-market app: it wires the visor's appnet dmsg
// transport to the skycoin exchange engine.
func RunSkydexMarket(ctx context.Context, args []string) error {
	if len(args) > 0 {
		fs := pflag.NewFlagSet("skydex-market", pflag.ContinueOnError)
		fs.StringVar(&dbPath, "db", "", "path to SQLite database file")
		fs.Uint16Var(&port, "port", 0, "routing port")
		fs.StringVar(&uiAddr, "addr", skyenv.SkydexMarketAddr, "operator UI address")
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

	// Resolve the dmsg listen port: default to the visor-assigned routing port,
	// overridden by --port when set (mirrors vpn-client / skychat).
	listenPort := routing.Port(appCl.RoutingPort())
	if port != 0 {
		listenPort = routing.Port(port)
		appCl.Client.SetAppPortOrLog(listenPort)
	}

	// Listen on appnet.TypeDmsg via the visor; clients Dial the market's public
	// key on the same port. The listener is a plain net.Listener as far as the
	// transport-agnostic engine is concerned.
	lis, err := appCl.Listen(appnet.TypeDmsg, listenPort)
	if err != nil {
		appCl.SetErrorOrLog(err)
		return err
	}

	// Over skywire the authenticated identity is the client's dmsg public key,
	// carried on the accepted connection's appnet.Addr — never trusted from the
	// payload.
	identify := func(conn net.Conn) (string, error) {
		raddr, ok := conn.RemoteAddr().(appnet.Addr)
		if !ok {
			return "", fmt.Errorf("rejecting conn with non-appnet remote addr %v", conn.RemoteAddr())
		}
		return raddr.PubKey.Hex(), nil
	}

	cfg := skydexmarket.Config{
		DBPath:  dbPath,
		WorkDir: appCl.WorkDir(),
		UIAddr:  uiAddr,
		OnReady: func() { appCl.SetStatusOrLog(appserver.AppDetailedStatusRunning) },
	}

	errCh := make(chan error, 1)
	go func() { errCh <- skydexmarket.Run(ctx, cfg, lis, identify, appHost{appCl}) }()

	termCh := make(chan os.Signal, 1)
	signal.Notify(termCh, os.Interrupt)

	select {
	case <-termCh:
		appCl.LogInfo("Received interrupt signal, shutting down gracefully...")
	case err := <-errCh:
		if err != nil {
			appCl.LogError("skydex-market engine stopped: %v", err)
			appCl.SetErrorOrLog(err)
		}
	case <-ctx.Done():
		appCl.LogInfo("Context canceled, shutting down...")
	}
	cancel()
	appCl.SetStatusOrLog(appserver.AppDetailedStatusStopped)

	return nil
}
