// Package commands cmd/apps/skynet-client/commands/skynet-client.go
package commands

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/internal/skynet"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
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
	// rawTCP enables raw TCP forwarding instead of HTTP
	rawTCP bool
	// appPort is the routing port for the app
	appPort uint16
)

func init() {
	launcher.RegisterApp("skynet-client", RunSkynetClient)
	RootCmd.Flags().StringVar(&srv, "srv", "", "remote server public key")
	RootCmd.Flags().IntVar(&remotePort, "remote", 0, "remote port to forward")
	RootCmd.Flags().IntVar(&localPort, "local", 0, "local port to listen on")
	RootCmd.Flags().BoolVar(&rawTCP, "raw-tcp", false, "use raw TCP forwarding instead of HTTP")
	RootCmd.Flags().Uint16Var(&appPort, "port", 0, "routing port for communication between app and visor")
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
		fs.BoolVar(&rawTCP, "raw-tcp", false, "raw TCP mode")
		fs.Uint16Var(&appPort, "port", 0, "routing port")
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

	appCl.Log().Infof("Connecting to %s:%d, forwarding to localhost:%d (raw_tcp=%v)",
		remotePK.Hex()[:16], remotePort, localPort, rawTCP)

	// Set routing port if specified
	if appPort != 0 {
		setAppPort(appCl, routing.Port(appPort))
	}

	setAppStatus(appCl, appserver.AppDetailedStatusStarting)

	// Connect to remote skynet server
	// Use SkyForwardingServerPort (47) which is the built-in visor service
	// This avoids noise handshake race conditions that occur with app-layer connections
	connApp := appnet.Addr{
		Net:    netType,
		PubKey: remotePK,
		Port:   routing.Port(skyenv.SkyForwardingServerPort),
	}

	appCl.Log().Infof("Dialing %s on port %d", remotePK.Hex()[:16], skyenv.SkyForwardingServerPort)

	conn, err := appCl.Dial(connApp)
	if err != nil {
		setAppError(appCl, fmt.Errorf("failed to connect to server: %w", err))
		return fmt.Errorf("failed to connect to server: %w", err)
	}

	// Create client
	client := skynet.NewClient(appCl.Log(), remotePK, remotePort, localPort, rawTCP)

	// Connect (send request to server)
	if err := client.Connect(conn); err != nil {
		setAppError(appCl, err)
		appCl.Log().WithError(err).Error("Failed to connect to remote server")
		return err
	}

	appCl.Log().Info("Connected to remote skynet server")
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
