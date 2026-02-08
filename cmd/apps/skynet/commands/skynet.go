// Package commands cmd/apps/skynet/commands/skynet.go
package commands

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/internal/skynet"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

const (
	netType = appnet.TypeSkynet
)

var (
	// localPorts is a comma-separated list of local ports to expose
	localPorts string
	// whitelist is a comma-separated list of public keys allowed to connect
	whitelist string
	// appPort is the routing port for the app
	appPort uint16
)

func init() {
	launcher.RegisterApp("skynet", RunSkynet)
	RootCmd.Flags().StringVar(&localPorts, "ports", "", "comma-separated list of local ports to expose (e.g., '8080,9000')")
	RootCmd.Flags().StringVar(&whitelist, "whitelist", "", "comma-separated list of public keys allowed to connect (empty = allow all)")
	RootCmd.Flags().Uint16Var(&appPort, "port", 0, "routing port for communication between app and visor")
}

// RootCmd is the root command for skynet
var RootCmd = &cobra.Command{
	Use:                   "skynet",
	Short:                 "skywire port forwarding server application",
	Long:                  "Skynet exposes local TCP ports over skywire network with optional whitelist access control",
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, args []string) {
		ctx := context.Background()
		if err := RunSkynet(ctx, args); err != nil {
			log.Fatal(err)
		}
	},
}

// RunSkynet runs the skynet server app logic.
func RunSkynet(ctx context.Context, args []string) error {
	// Parse flags when called via internal launcher
	if len(args) > 0 {
		fs := pflag.NewFlagSet("skynet", pflag.ContinueOnError)
		fs.StringVar(&localPorts, "ports", "", "comma-separated list of local ports to expose")
		fs.StringVar(&whitelist, "whitelist", "", "comma-separated list of allowed public keys")
		fs.Uint16Var(&appPort, "port", 0, "routing port")
		if err := fs.Parse(args); err != nil {
			return fmt.Errorf("failed to parse flags: %w", err)
		}
	}

	appCl := app.NewClient(nil)
	defer appCl.Close()

	appCl.Log().Infof("Build info: %s", buildinfo.Version())

	// Parse local ports
	var ports []int
	if localPorts != "" {
		for _, p := range strings.Split(localPorts, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			var port int
			if _, err := fmt.Sscanf(p, "%d", &port); err != nil {
				setAppError(appCl, fmt.Errorf("invalid port: %s", p))
				return fmt.Errorf("invalid port: %s", p)
			}
			if port <= 0 || port > 65535 {
				setAppError(appCl, fmt.Errorf("port out of range: %d", port))
				return fmt.Errorf("port out of range: %d", port)
			}
			ports = append(ports, port)
		}
	}

	if len(ports) == 0 {
		err := fmt.Errorf("no ports specified, use --ports flag")
		setAppError(appCl, err)
		return err
	}

	// Parse whitelist
	var allowedPKs []cipher.PubKey
	if whitelist != "" {
		for _, pkStr := range strings.Split(whitelist, ",") {
			pkStr = strings.TrimSpace(pkStr)
			if pkStr == "" {
				continue
			}
			var pk cipher.PubKey
			if err := pk.Set(pkStr); err != nil {
				setAppError(appCl, fmt.Errorf("invalid public key in whitelist: %s", pkStr))
				return fmt.Errorf("invalid public key in whitelist: %s", pkStr)
			}
			allowedPKs = append(allowedPKs, pk)
		}
	}

	appCl.Log().Infof("Exposing ports: %v", ports)
	if len(allowedPKs) > 0 {
		appCl.Log().Infof("Whitelist enabled with %d public keys", len(allowedPKs))
	} else {
		appCl.Log().Info("Whitelist disabled - allowing all connections")
	}

	// Create server
	srv := skynet.NewServer(appCl.Log(), ports, allowedPKs)

	// Get routing port
	port := appCl.Config().RoutingPort
	if appPort != 0 {
		port = routing.Port(appPort)
		setAppPort(appCl, port)
	}

	// Listen on skynet
	l, err := appCl.Listen(netType, port)
	if err != nil {
		setAppError(appCl, err)
		appCl.Log().Errorf("Error listening network %v on port %d: %v", netType, port, err)
		return err
	}

	appCl.Log().Infof("Listening on skynet port %d", port)

	// Handle shutdown
	termCh := make(chan os.Signal, 1)
	signal.Notify(termCh, os.Interrupt)

	go func() {
		<-termCh
		appCl.Log().Info("Received interrupt, shutting down...")
		if err := srv.Close(); err != nil {
			appCl.Log().Errorf("Error closing server: %v", err)
		}
	}()

	defer setAppStatus(appCl, appserver.AppDetailedStatusStopped)

	// Serve
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
