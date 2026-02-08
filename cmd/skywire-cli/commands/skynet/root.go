// Package skynet cmd/skywire-cli/commands/skynet/root.go
package skynet

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
	skynetClientAppName = "skynet-client"
)

var (
	remotePort     int
	remotePk       string
	localPort      int
	rawTCP         bool
	clientAppPort  uint16
	useInternal    bool
	useExternal    bool
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
	startCmd.MarkFlagsMutuallyExclusive("internal", "external")
}

// RootCmd contains skynet commands
var RootCmd = &cobra.Command{
	Use:   "skynet",
	Short: "Skynet port forwarding",
	Long: `Skynet provides port forwarding over the Skywire network.

Client commands connect to remote skynet servers and forward traffic to localhost.
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

		// Check if app exists
		_, err = rpcClient.App(skynetClientAppName)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("skynet-client app not found in visor config: %w", err))
		}

		// Configure the app
		arguments := map[string]any{
			"app":      "skynet-client",
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

		err = rpcClient.DoCustomSetting(skynetClientAppName, arguments)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to configure skynet-client: %w", err))
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
			err = rpcClient.StartAppWithMode(skynetClientAppName, launcherMode)
		} else {
			err = rpcClient.StartApp(skynetClientAppName)
		}
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to start skynet-client: %w", err))
		}

		internal.PrintOutput(cmd.Flags(), "OK", fmt.Sprintf("Skynet client started: %s:%d -> localhost:%d\n", remotePk[:16]+"...", remotePort, localPort))
	},
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the skynet client",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		err = rpcClient.StopApp(skynetClientAppName)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to stop skynet-client: %w", err))
		}

		internal.PrintOutput(cmd.Flags(), "OK", "Skynet client stopped\n")
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show skynet client status",
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

		type clientStatus struct {
			Name      string       `json:"name"`
			Status    string       `json:"status"`
			AutoStart bool         `json:"autostart"`
			Port      routing.Port `json:"port"`
			Args      []string     `json:"args,omitempty"`
			Details   string       `json:"details,omitempty"`
		}

		var jsonStatus clientStatus
		found := false

		for _, state := range states {
			if state.Name == skynetClientAppName {
				found = true
				status := "stopped"
				if state.Status == appserver.AppStatusRunning {
					status = "running"
				}
				if state.Status == appserver.AppStatusErrored {
					status = "errored"
				}

				jsonStatus = clientStatus{
					Name:      state.Name,
					Status:    status,
					AutoStart: state.AutoStart,
					Port:      state.Port,
					Args:      state.Args,
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
				break
			}
		}

		if !found {
			internal.PrintOutput(cmd.Flags(), nil, "Skynet client not configured\n")
			return
		}

		internal.Catch(cmd.Flags(), w.Flush())
		internal.PrintOutput(cmd.Flags(), jsonStatus, b.String())
	},
}
