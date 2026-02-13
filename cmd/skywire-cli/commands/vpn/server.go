// Package clivpn cmd/skywire-cli/commands/vpn/server.go
package clivpn

import (
	"bytes"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routing"
)

const (
	vpnServerAppName = "vpn-server"
)

var (
	vpnSrvWhitelist   string
	vpnSrvNetifc      string
	vpnSrvSecure      bool
	vpnSrvAppPort     uint16
	vpnSrvUseInternal bool
	vpnSrvUseExternal bool
)

func init() {
	RootCmd.AddCommand(vpnServerCmd)
	vpnServerCmd.AddCommand(
		vpnSrvStartCmd,
		vpnSrvStopCmd,
		vpnSrvStatusCmd,
	)
	vpnSrvStartCmd.Flags().StringVarP(&vpnSrvWhitelist, "whitelist", "w", "", "comma-separated list of public keys allowed to connect (empty = allow all)")
	vpnSrvStartCmd.Flags().StringVarP(&vpnSrvNetifc, "netifc", "i", "", "network interface for VPN traffic")
	vpnSrvStartCmd.Flags().BoolVar(&vpnSrvSecure, "secure", true, "forbid connections from clients to server local network")
	vpnSrvStartCmd.Flags().Uint16Var(&vpnSrvAppPort, "port", 0, "routing port for communication between app and visor")
	vpnSrvStartCmd.Flags().BoolVar(&vpnSrvUseInternal, "internal", false, "force internal launcher")
	vpnSrvStartCmd.Flags().BoolVar(&vpnSrvUseExternal, "external", false, "force external launcher")
	vpnSrvStartCmd.MarkFlagsMutuallyExclusive("internal", "external")
}

var vpnServerCmd = &cobra.Command{
	Use:   "server",
	Short: "VPN server control",
	Long: `Control the VPN server application.

The VPN server provides a full VPN tunnel over the Skywire network.
Other visors can connect to this VPN using vpn-client.

Note: VPN server is only supported on Linux.`,
}

var vpnSrvStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the VPN server",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		// Check if app exists
		_, err = rpcClient.App(vpnServerAppName)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("vpn-server app not found in visor config: %w", err))
		}

		// Configure the app with custom settings if provided
		if vpnSrvWhitelist != "" || vpnSrvNetifc != "" || vpnSrvAppPort != 0 || !vpnSrvSecure {
			arguments := map[string]any{
				"app": "vpn-server",
			}
			if vpnSrvWhitelist != "" {
				arguments["--whitelist"] = vpnSrvWhitelist
			}
			if vpnSrvNetifc != "" {
				arguments["--netifc"] = vpnSrvNetifc
			}
			if !vpnSrvSecure {
				arguments["--secure"] = "false"
			}
			if vpnSrvAppPort != 0 {
				arguments["appPort"] = vpnSrvAppPort
			}
			err = rpcClient.DoCustomSetting(vpnServerAppName, arguments)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to configure vpn-server: %w", err))
			}
		}

		// Determine launcher mode
		launcherMode := ""
		if vpnSrvUseInternal {
			launcherMode = "internal"
		} else if vpnSrvUseExternal {
			launcherMode = "external"
		}

		// Start the app
		if launcherMode != "" {
			err = rpcClient.StartAppWithMode(vpnServerAppName, launcherMode)
		} else {
			err = rpcClient.StartApp(vpnServerAppName)
		}
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to start vpn-server: %w", err))
		}

		internal.PrintOutput(cmd.Flags(), "OK", "VPN server started\n")
	},
}

var vpnSrvStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the VPN server",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		err = rpcClient.StopApp(vpnServerAppName)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to stop vpn-server: %w", err))
		}

		internal.PrintOutput(cmd.Flags(), "OK", "VPN server stopped\n")
	},
}

var vpnSrvStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show VPN server status",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		states, err := rpcClient.Apps()
		internal.Catch(cmd.Flags(), err)

		var b bytes.Buffer
		w := tabwriter.NewWriter(&b, 0, 0, 3, ' ', tabwriter.TabIndent)

		type serverStatus struct {
			Name      string       `json:"name"`
			Status    string       `json:"status"`
			AutoStart bool         `json:"autostart"`
			Port      routing.Port `json:"port"`
			Details   string       `json:"details,omitempty"`
		}

		var jsonStatus serverStatus
		found := false

		for _, state := range states {
			if state.Name == vpnServerAppName {
				found = true
				status := "stopped"
				if state.Status == appserver.AppStatusRunning {
					status = "running"
				}
				if state.Status == appserver.AppStatusErrored {
					status = "errored"
				}

				jsonStatus = serverStatus{
					Name:      state.Name,
					Status:    status,
					AutoStart: state.AutoStart,
					Port:      state.Port,
					Details:   state.DetailedStatus,
				}

				_, err = fmt.Fprintf(w, "Name:\t%s\n", state.Name)
				internal.Catch(cmd.Flags(), err)
				_, err = fmt.Fprintf(w, "Status:\t%s\n", status)
				internal.Catch(cmd.Flags(), err)
				_, err = fmt.Fprintf(w, "AutoStart:\t%t\n", state.AutoStart)
				internal.Catch(cmd.Flags(), err)
				_, err = fmt.Fprintf(w, "Port:\t%d\n", state.Port)
				internal.Catch(cmd.Flags(), err)
				if state.DetailedStatus != "" {
					_, err = fmt.Fprintf(w, "Details:\t%s\n", state.DetailedStatus)
					internal.Catch(cmd.Flags(), err)
				}
				break
			}
		}

		if !found {
			internal.PrintOutput(cmd.Flags(), nil, "VPN server not configured\n")
			return
		}

		internal.Catch(cmd.Flags(), w.Flush())
		internal.PrintOutput(cmd.Flags(), jsonStatus, b.String())
	},
}
