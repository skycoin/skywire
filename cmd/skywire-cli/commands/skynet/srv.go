// Package skynet srv.go
package skynet

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routing"
)

const (
	skynetBinaryName = "skynet"
)

var (
	srvPorts       string
	srvWhitelist   string
	srvAppPort     uint16
	srvUseInternal bool
	srvUseExternal bool
	srvName        string // optional custom name for the server instance
)

func init() {
	srvCmd.AddCommand(
		srvStartCmd,
		srvStopCmd,
		srvStatusCmd,
	)
	srvStartCmd.Flags().StringVarP(&srvPorts, "ports", "p", "", "comma-separated list of local ports to expose (e.g., '8080,9000')")
	srvStartCmd.Flags().StringVarP(&srvWhitelist, "whitelist", "w", "", "comma-separated list of public keys allowed to connect (empty = allow all)")
	srvStartCmd.Flags().Uint16Var(&srvAppPort, "port", 0, "routing port for communication between app and visor")
	srvStartCmd.Flags().BoolVar(&srvUseInternal, "internal", false, "force internal launcher")
	srvStartCmd.Flags().BoolVar(&srvUseExternal, "external", false, "force external launcher")
	srvStartCmd.Flags().StringVarP(&srvName, "name", "n", "", "custom name for this server instance (default: skynet-<first-port>)")
	srvStartCmd.MarkFlagsMutuallyExclusive("internal", "external")

	srvStopCmd.Flags().StringVarP(&srvName, "name", "n", "", "name of the server instance to stop")
}

var srvCmd = &cobra.Command{
	Use:   "srv",
	Short: "Skynet port forwarding server",
	Long: `Control the skynet server application.

The skynet server exposes local TCP ports over the Skywire network.
Other visors can connect to these ports using the skynet client.

Multiple server instances can run simultaneously with different ports.
Each instance gets a unique name (e.g., skynet-3435, skynet-8080).

With whitelist support, you can restrict access to specific public keys.`,
}

var srvStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a skynet server instance",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		// Validate ports flag
		if srvPorts == "" {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--ports flag is required"))
		}

		// Validate and get first port for naming
		var firstPort int
		for _, portStr := range strings.Split(srvPorts, ",") {
			portStr = strings.TrimSpace(portStr)
			if portStr == "" {
				continue
			}
			var port int
			if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid port: %s", portStr))
			}
			if port <= 0 || port > 65535 {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("port out of range: %d", port))
			}
			if firstPort == 0 {
				firstPort = port
			}
		}

		// Generate app name: custom name or skynet-<first-port>
		appName := srvName
		if appName == "" {
			appName = fmt.Sprintf("skynet-%d", firstPort)
		}

		// Check if app exists, add it if not
		_, err = rpcClient.App(appName)
		if err != nil {
			// App not in config - add it automatically
			// appName is unique, binaryName is always "skynet"
			if addErr := rpcClient.AddApp(appName, skynetBinaryName); addErr != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to add %s app to config: %w", appName, addErr))
			}
		}

		// Configure the app
		arguments := map[string]any{
			"app":     skynetBinaryName,
			"--ports": srvPorts,
		}

		if srvWhitelist != "" {
			arguments["--whitelist"] = srvWhitelist
		}

		if srvAppPort != 0 {
			arguments["appPort"] = srvAppPort
		}

		err = rpcClient.DoCustomSetting(appName, arguments)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to configure %s: %w", appName, err))
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
			err = rpcClient.StartAppWithMode(appName, launcherMode)
		} else {
			err = rpcClient.StartApp(appName)
		}
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to start %s: %w", appName, err))
		}

		internal.PrintOutput(cmd.Flags(), "OK", fmt.Sprintf("Skynet server '%s' started, exposing ports: %s\n", appName, srvPorts))
	},
}

var srvStopCmd = &cobra.Command{
	Use:   "stop [name]",
	Short: "Stop a skynet server instance",
	Long: `Stop a skynet server instance by name.

If no name is provided, stops all running skynet server instances.

Examples:
  skywire cli skynet srv stop skynet-3435
  skywire cli skynet srv stop --name skynet-8080
  skywire cli skynet srv stop  # stops all skynet-* servers`,
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		// Get name from args or flag
		appName := srvName
		if appName == "" && len(args) > 0 {
			appName = args[0]
		}

		if appName != "" {
			// Stop specific instance
			err = rpcClient.StopApp(appName)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to stop %s: %w", appName, err))
			}
			internal.PrintOutput(cmd.Flags(), "OK", fmt.Sprintf("Skynet server '%s' stopped\n", appName))
		} else {
			// Stop all skynet-* servers
			states, err := rpcClient.Apps()
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to get apps: %w", err))
			}

			stopped := 0
			for _, state := range states {
				if strings.HasPrefix(state.Name, "skynet-") && state.Status == appserver.AppStatusRunning {
					if err := rpcClient.StopApp(state.Name); err != nil {
						fmt.Fprintf(os.Stderr, "Warning: failed to stop %s: %v\n", state.Name, err)
					} else {
						stopped++
						fmt.Printf("Stopped %s\n", state.Name)
					}
				}
			}

			if stopped == 0 {
				internal.PrintOutput(cmd.Flags(), "OK", "No running skynet servers found\n")
			} else {
				internal.PrintOutput(cmd.Flags(), "OK", fmt.Sprintf("Stopped %d skynet server(s)\n", stopped))
			}
		}
	},
}

var srvStatusCmd = &cobra.Command{
	Use:   "status [name]",
	Short: "Show skynet server status",
	Long: `Show status of skynet server instances.

If no name is provided, shows all skynet server instances.

Examples:
  skywire cli skynet srv status
  skywire cli skynet srv status skynet-3435`,
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		states, err := rpcClient.Apps()
		internal.Catch(cmd.Flags(), err)

		// Filter name from args
		filterName := ""
		if len(args) > 0 {
			filterName = args[0]
		}

		var b bytes.Buffer
		w := tabwriter.NewWriter(&b, 0, 0, 3, ' ', tabwriter.TabIndent)

		type serverStatus struct {
			Name      string       `json:"name"`
			Status    string       `json:"status"`
			AutoStart bool         `json:"autostart"`
			Port      routing.Port `json:"port"`
			Args      []string     `json:"args,omitempty"`
			Details   string       `json:"details,omitempty"`
		}

		var allStatus []serverStatus
		found := false

		for _, state := range states {
			// Match skynet-* apps or exact filter match
			isSkynetServer := strings.HasPrefix(state.Name, "skynet-") || state.Name == "skynet"
			matchesFilter := filterName == "" || state.Name == filterName

			if isSkynetServer && matchesFilter {
				found = true
				status := "stopped"
				if state.Status == appserver.AppStatusRunning {
					status = "running"
				}
				if state.Status == appserver.AppStatusErrored {
					status = "errored"
				}

				jsonStatus := serverStatus{
					Name:      state.Name,
					Status:    status,
					AutoStart: state.AutoStart,
					Port:      state.Port,
					Args:      state.Args,
					Details:   state.DetailedStatus,
				}
				allStatus = append(allStatus, jsonStatus)

				_, err = fmt.Fprintf(w, "Name:\t%s\n", state.Name)
				internal.Catch(cmd.Flags(), err)
				_, err = fmt.Fprintf(w, "Status:\t%s\n", status)
				internal.Catch(cmd.Flags(), err)
				_, err = fmt.Fprintf(w, "AutoStart:\t%t\n", state.AutoStart)
				internal.Catch(cmd.Flags(), err)
				_, err = fmt.Fprintf(w, "Port:\t%d\n", state.Port)
				internal.Catch(cmd.Flags(), err)

				// Show configured ports and whitelist from args
				for i, arg := range state.Args {
					if arg == "--ports" && i+1 < len(state.Args) {
						_, err = fmt.Fprintf(w, "Exposing:\t%s\n", state.Args[i+1])
						internal.Catch(cmd.Flags(), err)
					}
					if arg == "--whitelist" && i+1 < len(state.Args) {
						_, err = fmt.Fprintf(w, "Whitelist:\t%s\n", state.Args[i+1])
						internal.Catch(cmd.Flags(), err)
					}
				}

				if state.DetailedStatus != "" {
					_, err = fmt.Fprintf(w, "Details:\t%s\n", state.DetailedStatus)
					internal.Catch(cmd.Flags(), err)
				}
				_, _ = fmt.Fprintln(w, "---")
			}
		}

		if !found {
			if filterName != "" {
				internal.PrintOutput(cmd.Flags(), nil, fmt.Sprintf("Skynet server '%s' not found\n", filterName))
			} else {
				internal.PrintOutput(cmd.Flags(), nil, "No skynet servers configured\n")
			}
			return
		}

		internal.Catch(cmd.Flags(), w.Flush())
		internal.PrintOutput(cmd.Flags(), allStatus, b.String())
	},
}
