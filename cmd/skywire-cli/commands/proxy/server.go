// Package skysocksc cmd/skywire-cli/commands/proxy/server.go
package skysocksc

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
	serverAppName = "skysocks"
)

var (
	srvWhitelist   string
	srvAppPort     uint16
	srvUseInternal bool
	srvUseExternal bool
)

func init() {
	RootCmd.AddCommand(serverCmd)
	serverCmd.AddCommand(
		srvStartCmd,
		srvStopCmd,
		srvStatusCmd,
	)
	srvStartCmd.Flags().StringVarP(&srvWhitelist, "whitelist", "w", "", "comma-separated list of public keys allowed to connect (empty = allow all)")
	srvStartCmd.Flags().Uint16Var(&srvAppPort, "port", 0, "routing port for communication between app and visor")
	srvStartCmd.Flags().BoolVar(&srvUseInternal, "internal", false, "force internal launcher")
	srvStartCmd.Flags().BoolVar(&srvUseExternal, "external", false, "force external launcher")
	srvStartCmd.MarkFlagsMutuallyExclusive("internal", "external")
}

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Skysocks server (SOCKS5 proxy server)",
	Long: `Control the skysocks server application.

The skysocks server provides a SOCKS5 proxy over the Skywire network.
Other visors can connect to this proxy using skysocks-client.`,
}

var srvStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the skysocks server",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		// Check if app exists
		_, err = rpcClient.App(serverAppName)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("skysocks app not found in visor config: %w", err))
		}

		// Configure the app with custom settings if provided
		if srvWhitelist != "" || srvAppPort != 0 {
			arguments := map[string]any{
				"app": "skysocks",
			}
			if srvWhitelist != "" {
				arguments["--whitelist"] = srvWhitelist
			}
			if srvAppPort != 0 {
				arguments["appPort"] = srvAppPort
			}
			err = rpcClient.DoCustomSetting(serverAppName, arguments)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to configure skysocks: %w", err))
			}
		}

		// Determine launcher mode
		launcherMode := ""
		if srvUseInternal {
			launcherMode = "internal"
		} else if srvUseExternal {
			launcherMode = "external"
		}

		// Start the app
		if launcherMode != "" {
			err = rpcClient.StartAppWithMode(serverAppName, launcherMode)
		} else {
			err = rpcClient.StartApp(serverAppName)
		}
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to start skysocks: %w", err))
		}

		internal.PrintOutput(cmd.Flags(), "OK", "Skysocks server started\n")
	},
}

var srvStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the skysocks server",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		err = rpcClient.StopApp(serverAppName)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to stop skysocks: %w", err))
		}

		internal.PrintOutput(cmd.Flags(), "OK", "Skysocks server stopped\n")
	},
}

var srvStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show skysocks server status",
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
			if state.Name == serverAppName {
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
			internal.PrintOutput(cmd.Flags(), nil, "Skysocks server not configured\n")
			return
		}

		internal.Catch(cmd.Flags(), w.Flush())
		internal.PrintOutput(cmd.Flags(), jsonStatus, b.String())
	},
}
