// Package commands cmd/apps/skysocks/skysocks.go
package commands

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"

	ipc "github.com/james-barrow/golang-ipc"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/internal/skysocks"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

const (
	netType = appnet.TypeSkynet
)

var (
	passcode string
	appPort  uint16
)

func init() {
	launcher.RegisterApp("skysocks", RunSkysocks)
	RootCmd.Flags().StringVar(&passcode, "passcode", "", "passcode to authenticate connecting users")
	RootCmd.Flags().Uint16Var(&appPort, "port", 0, "routing port for communication between app and visor")
}

// RootCmd is the root command for skysocks
var RootCmd = &cobra.Command{
	Use:                   "skysocks",
	Short:                 "skywire socks5 proxy server application",
	Long:                  calvin.AsciiFont("skysocks"),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, args []string) {
		ctx := context.Background()
		if err := RunSkysocks(ctx, args); err != nil {
			log.Fatal(err)
		}
	},
}

// RunSkysocks runs the skysocks server app logic.
func RunSkysocks(ctx context.Context, args []string) error {
	// Parse flags when called via internal launcher
	if len(args) > 0 {
		fs := pflag.NewFlagSet("skysocks", pflag.ContinueOnError)
		fs.StringVar(&passcode, "passcode", "", "passcode to authenticate")
		fs.Uint16Var(&appPort, "port", 0, "routing port")
		if err := fs.Parse(args); err != nil {
			return fmt.Errorf("failed to parse flags: %w", err)
		}
	}

	appCl := app.NewClient(nil)
	defer appCl.Close()

	appCl.Log().Infof("Build info: %s", buildinfo.Version())

	srv, err := skysocks.NewServer(passcode, appCl)
	if err != nil {
		setAppError(appCl, err)
		appCl.Log().Errorf("Failed to create a new server: %v", err)
		return err
	}

	port := appCl.Config().RoutingPort
	if appPort != 0 {
		port = routing.Port(appPort)
		setAppPort(appCl, port)
	}

	l, err := appCl.Listen(netType, port)
	if err != nil {
		setAppError(appCl, err)
		appCl.Log().Errorf("Error listening network %v on port %d: %v", netType, port, err)
		return err
	}

	appCl.Log().Info("Starting serving proxy server")

	if runtime.GOOS == "windows" {
		ipcClient, err := ipc.StartClient(visorconfig.SkysocksName, nil)
		if err != nil {
			setAppError(appCl, err)
			appCl.Log().Errorf("Error creating ipc server for skysocks: %v", err)
			return err
		}
		go srv.ListenIPC(ipcClient)
	} else {
		termCh := make(chan os.Signal, 1)
		signal.Notify(termCh, os.Interrupt)

		go func() {
			<-termCh

			if err := srv.Close(); err != nil {
				appCl.Log().Errorf("Error closing server: %v", err)
			}
		}()
	}
	defer setAppStatus(appCl, appserver.AppDetailedStatusStopped)

	serveCh := make(chan error, 1)
	go func() {
		serveCh <- srv.Serve(l)
	}()

	select {
	case err := <-serveCh:
		if err != nil {
			appCl.Log().Errorf("Serve error: %v", err)
			return err
		}
	case <-ctx.Done():
		if err := srv.Close(); err != nil {
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
