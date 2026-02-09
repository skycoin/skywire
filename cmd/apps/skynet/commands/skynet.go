// Package commands cmd/apps/skynet/commands/skynet.go
package commands

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	// localPorts is a comma-separated list of local ports to expose
	localPorts string
	// whitelist is a comma-separated list of public keys allowed to connect
	whitelist string
	// rpcAddr is the visor RPC address
	rpcAddr string
)

func init() {
	launcher.RegisterApp("skynet", RunSkynet)
	RootCmd.Flags().StringVar(&localPorts, "ports", "", "comma-separated list of local ports to expose (e.g., '8080,9000')")
	RootCmd.Flags().StringVar(&whitelist, "whitelist", "", "comma-separated list of public keys allowed to connect (not currently supported)")
	RootCmd.Flags().StringVar(&rpcAddr, "rpc", "localhost:3435", "visor RPC address")
}

// RootCmd is the root command for skynet
var RootCmd = &cobra.Command{
	Use:                   "skynet",
	Short:                 "skywire port forwarding server application",
	Long:                  "Skynet exposes local TCP ports over skywire network via the built-in sky_forwarding service",
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
		fs.StringVar(&whitelist, "whitelist", "", "comma-separated list of allowed public keys (not currently supported)")
		fs.StringVar(&rpcAddr, "rpc", "localhost:3435", "visor RPC address")
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

	// Warn about whitelist
	if whitelist != "" {
		appCl.Log().Warn("Whitelist is not currently supported with the built-in sky_forwarding service")
	}

	appCl.Log().Infof("Exposing ports: %v via built-in sky_forwarding", ports)

	setAppStatus(appCl, appserver.AppDetailedStatusStarting)

	// Create RPC client to visor
	rpcClient, err := createRPCClient(rpcAddr)
	if err != nil {
		setAppError(appCl, fmt.Errorf("failed to connect to visor RPC: %w", err))
		return fmt.Errorf("failed to connect to visor RPC: %w", err)
	}

	// Register all ports with the visor
	registeredPorts := []int{}
	for _, port := range ports {
		if err := rpcClient.RegisterHTTPPort(port); err != nil {
			appCl.Log().Errorf("Failed to register port %d: %v", port, err)
			// Deregister any ports we already registered
			for _, p := range registeredPorts {
				_ = rpcClient.DeregisterHTTPPort(p) //nolint:errcheck
			}
			setAppError(appCl, fmt.Errorf("failed to register port %d: %w", port, err))
			return fmt.Errorf("failed to register port %d: %w", port, err)
		}
		appCl.Log().Infof("Registered port %d with visor", port)
		registeredPorts = append(registeredPorts, port)
	}

	appCl.Log().Infof("All %d ports registered, server ready", len(ports))
	setAppStatus(appCl, appserver.AppDetailedStatusRunning)

	// Handle shutdown
	termCh := make(chan os.Signal, 1)
	signal.Notify(termCh, os.Interrupt)

	// Check if context is already done
	select {
	case <-ctx.Done():
		appCl.Log().Warnf("Context already canceled before wait: %v", ctx.Err())
		setAppStatus(appCl, appserver.AppDetailedStatusStopped)
		return nil
	default:
		appCl.Log().Info("Context is valid, waiting for shutdown signal...")
	}

	// Wait for shutdown or context cancellation
	select {
	case <-termCh:
		appCl.Log().Info("Received interrupt, shutting down...")
	case <-ctx.Done():
		appCl.Log().Infof("Context canceled, shutting down: %v", ctx.Err())
	}

	// Deregister all ports
	for _, port := range registeredPorts {
		if err := rpcClient.DeregisterHTTPPort(port); err != nil {
			appCl.Log().Errorf("Failed to deregister port %d: %v", port, err)
		} else {
			appCl.Log().Infof("Deregistered port %d", port)
		}
	}

	setAppStatus(appCl, appserver.AppDetailedStatusStopped)
	return nil
}

func createRPCClient(addr string) (visor.API, error) {
	const rpcDialTimeout = time.Second * 5
	conn, err := net.DialTimeout("tcp", addr, rpcDialTimeout)
	if err != nil {
		return nil, err
	}
	logger := logging.MustGetLogger("visor-rpc")
	return visor.NewRPCClient(logger, conn, visor.RPCPrefix, 0), nil
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
