// Package skynet cmd/skywire-cli/commands/skynet/root.go
package skynet

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routing"
)

const (
	skynetClientBinaryName = "skynet-client"
)

var (
	remotePort    int
	remotePk      string
	localPort     int
	rawTCP        bool
	clientAppPort uint16
	useInternal   bool
	useExternal   bool
	clientName    string // optional custom name for the client instance
)

func init() {
	RootCmd.PersistentFlags().StringVar(&clirpc.Addr, "rpc", "localhost:3435", "RPC server address")
	RootCmd.AddCommand(
		startCmd,
		stopCmd,
		statusCmd,
		srvCmd,
	)
	startCmd.Flags().StringVarP(&remotePk, "pk", "k", "", "remote server public key")
	startCmd.Flags().IntVarP(&remotePort, "remote", "r", 0, "remote port to forward")
	startCmd.Flags().IntVarP(&localPort, "local", "l", 0, "local port to listen on")
	startCmd.Flags().BoolVar(&rawTCP, "raw-tcp", false, "use raw TCP forwarding instead of HTTP")
	startCmd.Flags().Uint16Var(&clientAppPort, "port", 0, "routing port for communication between app and visor")
	startCmd.Flags().BoolVar(&useInternal, "internal", false, "force internal launcher")
	startCmd.Flags().BoolVar(&useExternal, "external", false, "force external launcher")
	startCmd.Flags().StringVarP(&clientName, "name", "n", "", "custom name for this client instance (default: skynet-client-<local-port>)")
	startCmd.MarkFlagsMutuallyExclusive("internal", "external")

	stopCmd.Flags().StringVarP(&clientName, "name", "n", "", "name of the client instance to stop")
}

// RootCmd contains skynet commands
var RootCmd = &cobra.Command{
	Use:   "skynet",
	Short: "Skynet port forwarding",
	Long: `Skynet provides port forwarding over the Skywire network.

Client commands connect to remote skynet servers and forward traffic to localhost.
Multiple client instances can run simultaneously with different configurations.
Each instance gets a unique name (e.g., skynet-client-8080, skynet-client-3435).

Server commands (srv) expose local ports over the network.`,
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start skynet client to connect to a remote server",
	Long:  `Connect to a remote skynet server and forward traffic to a local port.`,
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		// Validate required flags
		if remotePk == "" {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--pk flag (remote server public key) is required"))
		}
		if remotePort <= 0 || remotePort > 65535 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--remote flag must be a valid port (1-65535)"))
		}
		if localPort <= 0 || localPort > 65535 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--local flag must be a valid port (1-65535)"))
		}

		// Generate app name: custom name or skynet-client-<local-port>
		appName := clientName
		if appName == "" {
			appName = fmt.Sprintf("skynet-client-%d", localPort)
		}

		// Check if app exists, add it if not
		_, err = rpcClient.App(appName)
		if err != nil {
			// App not in config - add it automatically
			// appName is unique, binaryName is always "skynet-client"
			if addErr := rpcClient.AddApp(appName, skynetClientBinaryName); addErr != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to add %s app to config: %w", appName, addErr))
			}
		}

		// Configure the app
		arguments := map[string]any{
			"app":      skynetClientBinaryName,
			"--srv":    remotePk,
			"--remote": fmt.Sprintf("%d", remotePort),
			"--local":  fmt.Sprintf("%d", localPort),
		}

		if rawTCP {
			arguments["--raw-tcp"] = "true"
		}

		if clientAppPort != 0 {
			arguments["appPort"] = clientAppPort
		}

		err = rpcClient.DoCustomSetting(appName, arguments)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to configure %s: %w", appName, err))
		}

		// Determine launcher mode
		launcherMode := ""
		if useInternal {
			launcherMode = "internal"
		} else if useExternal {
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

		pkShort := remotePk
		if len(pkShort) > 16 {
			pkShort = pkShort[:16] + "..."
		}
		fmt.Printf("Starting %s -> %s:%d -> localhost:%d ", appName, pkShort, remotePort, localPort)

		// Poll for app status until running, errored, or stopped
		for {
			time.Sleep(time.Second)
			fmt.Print(".")

			states, err := rpcClient.Apps()
			if err != nil {
				fmt.Println()
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to get app status: %w", err))
			}

			for _, state := range states {
				if state.Name == appName {
					switch state.Status {
					case appserver.AppStatusRunning:
						fmt.Println("\nRunning!")
						return
					case appserver.AppStatusErrored:
						fmt.Printf("\nError: %s\n", state.DetailedStatus)
						os.Exit(1)
					case appserver.AppStatusStopped:
						if state.DetailedStatus != "" {
							fmt.Printf("\nStopped: %s\n", state.DetailedStatus)
						} else {
							fmt.Println("\nStopped unexpectedly")
						}
						os.Exit(1)
					}
					break
				}
			}
		}
	},
}

var stopCmd = &cobra.Command{
	Use:   "stop [name]",
	Short: "Stop a skynet client instance",
	Long: `Stop a skynet client instance by name.

If no name is provided, stops all running skynet client instances.

Examples:
  skywire cli skynet stop skynet-client-8080
  skywire cli skynet stop --name skynet-client-3435
  skywire cli skynet stop  # stops all skynet-client-* clients`,
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		// Get name from args or flag
		appName := clientName
		if appName == "" && len(args) > 0 {
			appName = args[0]
		}

		if appName != "" {
			// Stop specific instance
			err = rpcClient.StopApp(appName)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to stop %s: %w", appName, err))
			}
			internal.PrintOutput(cmd.Flags(), "OK", fmt.Sprintf("Skynet client '%s' stopped\n", appName))
		} else {
			// Stop all skynet-client-* clients
			states, err := rpcClient.Apps()
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to get apps: %w", err))
			}

			stopped := 0
			for _, state := range states {
				if strings.HasPrefix(state.Name, "skynet-client-") && state.Status == appserver.AppStatusRunning {
					if err := rpcClient.StopApp(state.Name); err != nil {
						fmt.Fprintf(os.Stderr, "Warning: failed to stop %s: %v\n", state.Name, err)
					} else {
						stopped++
						fmt.Printf("Stopped %s\n", state.Name)
					}
				}
			}

			if stopped == 0 {
				internal.PrintOutput(cmd.Flags(), "OK", "No running skynet clients found\n")
			} else {
				internal.PrintOutput(cmd.Flags(), "OK", fmt.Sprintf("Stopped %d skynet client(s)\n", stopped))
			}
		}
	},
}

var statusCmd = &cobra.Command{
	Use:   "status [name]",
	Short: "Show skynet client status",
	Long: `Show status of skynet client instances.

If no name is provided, shows all skynet client instances.

Examples:
  skywire cli skynet status
  skywire cli skynet status skynet-client-8080`,
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

		type clientStatus struct {
			Name      string       `json:"name"`
			Status    string       `json:"status"`
			AutoStart bool         `json:"autostart"`
			Port      routing.Port `json:"port"`
			Args      []string     `json:"args,omitempty"`
			Details   string       `json:"details,omitempty"`
		}

		var allStatus []clientStatus
		found := false

		for _, state := range states {
			// Match skynet-client-* apps or exact filter match
			isSkynetClient := strings.HasPrefix(state.Name, "skynet-client-") || state.Name == "skynet-client"
			matchesFilter := filterName == "" || state.Name == filterName

			if isSkynetClient && matchesFilter {
				found = true
				status := "stopped"
				if state.Status == appserver.AppStatusRunning {
					status = "running"
				}
				if state.Status == appserver.AppStatusErrored {
					status = "errored"
				}

				jsonStatus := clientStatus{
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

				// Show connection info from args
				for i, arg := range state.Args {
					if arg == "--srv" && i+1 < len(state.Args) {
						pk := state.Args[i+1]
						if len(pk) > 16 {
							pk = pk[:16] + "..."
						}
						_, err = fmt.Fprintf(w, "Server:\t%s\n", pk)
						internal.Catch(cmd.Flags(), err)
					}
					if arg == "--remote" && i+1 < len(state.Args) {
						_, err = fmt.Fprintf(w, "RemotePort:\t%s\n", state.Args[i+1])
						internal.Catch(cmd.Flags(), err)
					}
					if arg == "--local" && i+1 < len(state.Args) {
						_, err = fmt.Fprintf(w, "LocalPort:\t%s\n", state.Args[i+1])
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
				internal.PrintOutput(cmd.Flags(), nil, fmt.Sprintf("Skynet client '%s' not found\n", filterName))
			} else {
				internal.PrintOutput(cmd.Flags(), nil, "No skynet clients configured\n")
			}
			return
		}

		internal.Catch(cmd.Flags(), w.Flush())
		internal.PrintOutput(cmd.Flags(), allStatus, b.String())
	},
}
