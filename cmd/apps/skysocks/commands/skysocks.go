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
func RunSkysocks(ctx context.Context, _ []string) error {
	appCl := app.NewClient(nil)
	defer appCl.Close()

	if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
		print(fmt.Sprintf("Failed to output build info: %v", err))
	}

	srv, err := skysocks.NewServer(passcode, appCl)
	if err != nil {
		setAppError(appCl, err)
		print(fmt.Sprintf("Failed to create a new server: %v\n", err))
		os.Exit(1)
	}

	port := appCl.Config().RoutingPort
	if appPort != 0 {
		port = routing.Port(appPort)
		setAppPort(appCl, port)
	}

	l, err := appCl.Listen(netType, port)
	if err != nil {
		setAppError(appCl, err)
		print(fmt.Sprintf("Error listening network %v on port %d: %v\n", netType, port, err))
		os.Exit(1)
	}

	fmt.Println("Starting serving proxy server")

	if runtime.GOOS == "windows" {
		ipcClient, err := ipc.StartClient(visorconfig.SkysocksName, nil)
		if err != nil {
			setAppError(appCl, err)
			print(fmt.Sprintf("Error creating ipc server for skysocks: %v\n", err))
			os.Exit(1)
		}
		go srv.ListenIPC(ipcClient)
	} else {
		termCh := make(chan os.Signal, 1)
		signal.Notify(termCh, os.Interrupt)

		go func() {
			<-termCh

			if err := srv.Close(); err != nil {
				print(fmt.Sprintf("%v\n", err))
				os.Exit(1)
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
			print(fmt.Sprintf("%v\n", err))
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
		print(fmt.Sprintf("Failed to set status %v: %v\n", status, err))
	}
}

func setAppError(appCl *app.Client, appErr error) {
	if err := appCl.SetError(appErr.Error()); err != nil {
		print(fmt.Sprintf("Failed to set error %v: %v\n", appErr, err))
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
		print(fmt.Sprintf("Failed to set port %v: %v\n", port, err))
	}
}
