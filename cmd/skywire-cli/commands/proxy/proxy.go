// Package skysocksc cmd/skywire-cli/commands/skysocksc/skysocks.go
package skysocksc

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/app/appserver"
)

var pk string

func init() {
	RootCmd.PersistentFlags().StringVar(&clirpc.Addr, "rpc", "localhost:3435", "RPC server address")
	RootCmd.AddCommand(
		proxyStartCmd,
		proxyStopCmd,
		proxyStatusCmd,
		proxyListCmd,
	)
	version := buildinfo.Version()
	if version == "unknown" {
		version = ""
	}
	proxyStartCmd.Flags().StringVar(&pk, "pk", "", "skysocks server public key")
	proxyListCmd.Flags().BoolVarP(&isUnFiltered, "nofilter", "n", false, "provide unfiltered results")
	proxyListCmd.Flags().StringVarP(&ver, "ver", "v", version, "filter results by version")
	proxyListCmd.Flags().StringVarP(&country, "country", "c", "", "filter results by country")
	proxyListCmd.Flags().BoolVarP(&isStats, "stats", "s", false, "return only a count of the results")
}

var proxyStartCmd = &cobra.Command{
	Use:   "start",
	Short: "start the proxy client",
	Args:  cobra.MinimumNArgs(0),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.StartSkysocksClient(pk))
		internal.PrintOutput(cmd.Flags(), nil, "Starting.")
		startProcess := true
		for startProcess {
			time.Sleep(time.Second * 1)
			internal.PrintOutput(cmd.Flags(), nil, ".")
			states, err := rpcClient.Apps()
			internal.Catch(cmd.Flags(), err)

			type output struct {
				AppError string `json:"app_error,omitempty"`
			}

			for _, state := range states {
				if state.Name == "skysocks-client" {
					if state.Status == appserver.AppStatusRunning {
						startProcess = false
						internal.PrintOutput(cmd.Flags(), nil, fmt.Sprintln("\nRunning!"))
					}
					if state.Status == appserver.AppStatusErrored {
						startProcess = false
						out := output{
							AppError: state.DetailedStatus,
						}
						internal.PrintOutput(cmd.Flags(), out, fmt.Sprintln("\nError! > "+state.DetailedStatus))
					}
				}
			}
		}
	},
}

var proxyStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "stop the proxy client",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.StopSkysocksClient())
		internal.PrintOutput(cmd.Flags(), "OK", fmt.Sprintln("OK"))
	},
}

var proxyStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "proxy client status",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		states, err := rpcClient.Apps()
		internal.Catch(cmd.Flags(), err)

		var b bytes.Buffer
		w := tabwriter.NewWriter(&b, 0, 0, 5, ' ', tabwriter.TabIndent)
		internal.Catch(cmd.Flags(), err)
		type appState struct {
			Status string `json:"status"`
		}
		var jsonAppStatus appState
		for _, state := range states {
			if state.Name == "skysocks-client" {

				status := "stopped"
				if state.Status == appserver.AppStatusRunning {
					status = "running"
				}
				if state.Status == appserver.AppStatusErrored {
					status = "errored"
				}
				jsonAppStatus = appState{
					Status: status,
				}
				_, err = fmt.Fprintf(w, "%s\n", status)
				internal.Catch(cmd.Flags(), err)
			}
		}
		internal.Catch(cmd.Flags(), w.Flush())
		internal.PrintOutput(cmd.Flags(), jsonAppStatus, b.String())
	},
}

var proxyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List servers",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		if isUnFiltered {
			ver = ""
			country = ""
		}
		servers, err := rpcClient.ProxyServers(ver, country)
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		if len(servers) == 0 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("No Servers found"))
		}
		if isStats {
			internal.PrintOutput(cmd.Flags(), fmt.Sprintf("%d Servers\n", len(servers)), fmt.Sprintf("%d Servers\n", len(servers)))
		} else {

			var msg string
			for _, i := range servers {
				msg += strings.Replace(i.Addr.String(), ":44", "", 1)
				if i.Geo != nil {
					msg += fmt.Sprintf(" | %s\n", i.Geo.Country)
				} else {
					msg += "\n"
				}
			}

			internal.PrintOutput(cmd.Flags(), servers, msg)
		}
	},
}
