// Package commands vpn-server.go
package commands

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/internal/vpn"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

const (
	netType = appnet.TypeSkynet
)

var (
	localPKStr string
	localSKStr string
	passcode   string
	networkIfc string
	secure     bool
	appPort    uint16
)

func init() {
	launcher.RegisterApp("vpn-server", RunVPNServer)
	RootCmd.Flags().StringVar(&localPKStr, "pk", "", "local pubkey")
	RootCmd.Flags().StringVar(&localSKStr, "sk", "", "local seckey")
	RootCmd.Flags().StringVar(&passcode, "passcode", "", "passcode to authenticate connecting users")
	RootCmd.Flags().StringVar(&networkIfc, "netifc", "", "Default network interface for multiple available interfaces")
	RootCmd.Flags().BoolVar(&secure, "secure", true, "Forbid connections from clients to server local network")
	RootCmd.Flags().Uint16Var(&appPort, "port", 0, "routing port for communication between app and visor")
}

// RootCmd is the root command for skywire-cli
var RootCmd = &cobra.Command{
	Use:                   "vpn-server",
	Short:                 "skywire vpn server application",
	Long:                  calvin.AsciiFont("vpn-server"),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, args []string) {
		ctx := context.Background()
		if err := RunVPNServer(ctx, args); err != nil {
			log.Fatal(err)
		}
	},
}

// RunVPNServer runs the VPN server app logic.
func RunVPNServer(ctx context.Context, args []string) error {
	// Parse flags when called via internal launcher
	if len(args) > 0 {
		fs := pflag.NewFlagSet("vpn-server", pflag.ContinueOnError)
		fs.StringVar(&localPKStr, "pk", "", "local pubkey")
		fs.StringVar(&localSKStr, "sk", "", "local seckey")
		fs.StringVar(&passcode, "passcode", "", "passcode")
		fs.StringVar(&networkIfc, "netifc", "", "network interface")
		fs.BoolVar(&secure, "secure", true, "secure mode")
		fs.Uint16Var(&appPort, "port", 0, "routing port")
		if err := fs.Parse(args); err != nil {
			return fmt.Errorf("failed to parse flags: %w", err)
		}
	}

	appCl := app.NewClient(nil)
	defer appCl.Close()

	port := appCl.Config().RoutingPort
	if appPort != 0 {
		port = routing.Port(appPort)
		setAppPort(appCl, port)
	}

	if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
		print(fmt.Sprintf("Failed to output build info: %v\n", err))
	}

	if runtime.GOOS != "linux" {
		err := errors.New("OS is not supported")
		print(err)
		setAppErr(appCl, err)
		os.Exit(1)
	}

	localPK := cipher.PubKey{}
	if localPKStr != "" {
		if err := localPK.UnmarshalText([]byte(localPKStr)); err != nil {
			print(fmt.Sprintf("Invalid local PK: %v\n", err))
			setAppErr(appCl, err)
			os.Exit(1)
		}
	}

	localSK := cipher.SecKey{}
	if localSKStr != "" {
		if err := localSK.UnmarshalText([]byte(localSKStr)); err != nil {
			print(fmt.Sprintf("Invalid local SK: %v\n", err))
			setAppErr(appCl, err)
			os.Exit(1)
		}
	}

	osSigs := make(chan os.Signal, 2)

	sigs := []os.Signal{syscall.SIGTERM, syscall.SIGINT}
	for _, sig := range sigs {
		signal.Notify(osSigs, sig)
	}

	l, err := appCl.Listen(netType, port)
	if err != nil {
		print(fmt.Sprintf("Error listening network %v on port %d: %v\n", netType, port, err))
		setAppErr(appCl, err)
		os.Exit(1)
	}

	srvCfg := vpn.ServerConfig{
		Passcode:         passcode,
		Secure:           secure,
		NetworkInterface: networkIfc,
	}
	srv, err := vpn.NewServer(srvCfg, appCl)
	if err != nil {
		print(fmt.Sprintf("Error creating VPN server: %v\n", err))
		setAppErr(appCl, err)
		os.Exit(1)
	}
	defer func() {
		if err := srv.Close(); err != nil {
			print(fmt.Sprintf("Error closing server: %v\n", err))
		}
	}()

	errCh := make(chan error)
	go func() {
		if err := srv.Serve(l); err != nil {
			errCh <- err
		}

		close(errCh)
	}()

	defer setAppStatus(appCl, appserver.AppDetailedStatusStopped)

	select {
	case <-osSigs:
		return nil
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		if err != nil {
			print(fmt.Sprintf("Error serving: %v\n", err))
			return err
		}
	}

	return nil
}

func setAppErr(appCl *app.Client, err error) {
	if appErr := appCl.SetError(err.Error()); appErr != nil {
		print(fmt.Sprintf("Failed to set error %v: %v\n", err, appErr))
	}
}

func setAppStatus(appCl *app.Client, status appserver.AppDetailedStatus) {
	if err := appCl.SetDetailedStatus(string(status)); err != nil {
		print(fmt.Sprintf("Failed to set status %v: %v\n", status, err))
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
