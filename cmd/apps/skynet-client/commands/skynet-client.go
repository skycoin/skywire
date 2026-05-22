// Package commands cmd/apps/skynet-client/commands/skynet-client.go
package commands

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skynet"
)

const (
	netType = appnet.TypeSkynet
)

var (
	// srv is the remote server public key
	srv string
	// remotePort is the port to connect to on the remote server
	remotePort int
	// localPort is the local port to listen on
	localPort int
	// appPort is the routing port for the app
	appPort uint16
	// routes is the number of parallel skynet mux routes to use for the
	// underlying app-level conn. 0 or 1 = single route (legacy Dial
	// path); N > 1 = router establishes N parallel mux routes and the
	// app sees a single conn whose payload is striped across them.
	routes int
	// minHops, when >= 2, forces the router to find non-direct paths
	// (rejecting any direct transport between this visor and the
	// remote). Needed in combination with routes > 1 to actually
	// exercise the mux>direct hypothesis end-to-end: without
	// min-hops, the router happily picks the direct transport for
	// every mux route, so N mux streams ride one TCP socket and
	// nothing is multiplexed across intermediates.
	minHops int
)

func init() {
	launcher.RegisterApp("skynet-client", RunSkynetClient)
	RootCmd.Flags().StringVar(&srv, "srv", "", "remote server public key")
	RootCmd.Flags().IntVar(&remotePort, "remote", 0, "remote port to forward")
	RootCmd.Flags().IntVar(&localPort, "local", 0, "local port to listen on")
	RootCmd.Flags().Uint16Var(&appPort, "port", 0, "routing port for communication between app and visor")
	RootCmd.Flags().IntVar(&routes, "routes", 0, "number of parallel skynet mux routes (0 or 1 = single route)")
	RootCmd.Flags().IntVar(&minHops, "min-hops", 0, "force routes through at least this many intermediates (>=2 rejects direct paths)")
}

// RootCmd is the root command for skynet-client
var RootCmd = &cobra.Command{
	Use:                   "skynet-client",
	Short:                 "skywire port forwarding client application",
	Long:                  "Skynet client connects to a remote skynet server and forwards traffic to localhost",
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, args []string) {
		ctx := context.Background()
		if err := RunSkynetClient(ctx, args); err != nil {
			log.Fatal(err)
		}
	},
}

// RunSkynetClient runs the skynet client app logic.
func RunSkynetClient(ctx context.Context, args []string) error {
	// Parse flags when called via internal launcher
	if len(args) > 0 {
		fs := pflag.NewFlagSet("skynet-client", pflag.ContinueOnError)
		fs.StringVar(&srv, "srv", "", "remote server public key")
		fs.IntVar(&remotePort, "remote", 0, "remote port")
		fs.IntVar(&localPort, "local", 0, "local port")
		fs.Uint16Var(&appPort, "port", 0, "routing port")
		fs.IntVar(&routes, "routes", 0, "number of parallel skynet mux routes")
		fs.IntVar(&minHops, "min-hops", 0, "minimum hop count (>=2 rejects direct paths)")
		if err := fs.Parse(args); err != nil {
			return fmt.Errorf("failed to parse flags: %w", err)
		}
	}

	appCl := app.NewClient(nil)
	defer appCl.Close()

	appCl.Log().Infof("Build info: %s", buildinfo.Version())

	// Validate required flags
	if srv == "" {
		err := fmt.Errorf("--srv flag (remote server public key) is required")
		setAppError(appCl, err)
		return err
	}

	var remotePK cipher.PubKey
	if err := remotePK.Set(srv); err != nil {
		setAppError(appCl, fmt.Errorf("invalid server public key: %w", err))
		return fmt.Errorf("invalid server public key: %w", err)
	}

	if remotePort <= 0 || remotePort > 65535 {
		err := fmt.Errorf("--remote flag (remote port) must be 1-65535")
		setAppError(appCl, err)
		return err
	}

	if localPort <= 0 || localPort > 65535 {
		err := fmt.Errorf("--local flag (local port) must be 1-65535")
		setAppError(appCl, err)
		return err
	}

	appCl.Log().Infof("Setting up accept loop on localhost:%d → %s:%d",
		localPort, remotePK.Hex(), remotePort)

	// Set routing port if specified
	if appPort != 0 {
		setAppPort(appCl, routing.Port(appPort))
	}

	setAppStatus(appCl, appserver.AppDetailedStatusStarting)

	// Build a dial factory the client calls per-accept. The factory
	// captures appCl + remote endpoint so each local connection gets
	// its own independent remote tunnel — necessary because the
	// skynet wire is raw bytes with no per-connection framing, so
	// concurrent (or even tightly-sequenced) local conns sharing a
	// single remoteConn would interleave their payloads. Using
	// SkyForwardingServerPort (47, built-in visor service) avoids
	// the noise-handshake race condition that hits app-layer dials.
	connApp := appnet.Addr{
		Net:    netType,
		PubKey: remotePK,
		Port:   routing.Port(skyenv.SkyForwardingServerPort),
	}

	switch {
	case routes > 1 && minHops > 1:
		appCl.Log().Infof("Per-accept dial shape: %s on port %d with %d parallel mux routes, min-hops=%d",
			remotePK.Hex(), skyenv.SkyForwardingServerPort, routes, minHops)
	case routes > 1:
		appCl.Log().Infof("Per-accept dial shape: %s on port %d with %d parallel mux routes",
			remotePK.Hex(), skyenv.SkyForwardingServerPort, routes)
	case minHops > 1:
		appCl.Log().Infof("Per-accept dial shape: %s on port %d with min-hops=%d",
			remotePK.Hex(), skyenv.SkyForwardingServerPort, minHops)
	default:
		appCl.Log().Infof("Per-accept dial shape: %s on port %d",
			remotePK.Hex(), skyenv.SkyForwardingServerPort)
	}

	dialRemote := func() (net.Conn, error) {
		if routes > 1 || minHops > 1 {
			return appCl.DialWithOptions(connApp, routes, minHops)
		}
		return appCl.Dial(connApp)
	}

	// Create client with the dial factory; Serve binds the local
	// listener and runs the accept loop.
	client := skynet.NewClient(appCl.Log(), remotePK, remotePort, localPort, dialRemote)

	appCl.Log().Info("Skynet client ready — waiting for local connections")
	setAppStatus(appCl, appserver.AppDetailedStatusRunning)

	// Handle shutdown
	termCh := make(chan os.Signal, 1)
	signal.Notify(termCh, os.Interrupt)

	go func() {
		<-termCh
		appCl.Log().Info("Received interrupt, shutting down...")
		if err := client.Close(); err != nil {
			appCl.Log().Errorf("Error closing client: %v", err)
		}
	}()

	defer setAppStatus(appCl, appserver.AppDetailedStatusStopped)

	// Serve (accept local connections)
	serveCh := make(chan error, 1)
	go func() {
		serveCh <- client.Serve()
	}()

	select {
	case err := <-serveCh:
		if err != nil {
			appCl.Log().Errorf("Serve error: %v", err)
			return err
		}
	case <-ctx.Done():
		if err := client.Close(); err != nil {
			return err
		}
	}

	return nil
}

func setAppStatus(appCl *app.Client, status appserver.AppDetailedStatus) {
	if err := appCl.SetDetailedStatus(string(status)); err != nil {
		appCl.Log().Errorf("Failed to set status %v: %v", status, err)
	}
}

func setAppError(appCl *app.Client, appErr error) {
	if err := appCl.SetError(appErr.Error()); err != nil {
		appCl.Log().Errorf("Failed to set error %v: %v", appErr, err)
	}
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}

func setAppPort(appCl *app.Client, port routing.Port) {
	if err := appCl.SetAppPort(port); err != nil {
		appCl.Log().Errorf("Failed to set port %v: %v", port, err)
	}
}
