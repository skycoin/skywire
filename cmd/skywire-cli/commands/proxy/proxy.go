// Package skysocksc cmd/skywire-cli/commands/skysocksc/skysocks.go
package skysocksc

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/servicedisc"
)

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
	proxyStartCmd.Flags().StringVarP(&pk, "pk", "k", "", "server public key")
	proxyListCmd.Flags().StringVarP(&pk, "pk", "k", "", "check proxy service discovery for public key")
	proxyListCmd.Flags().IntVarP(&count, "num", "n", 0, "number of results to return")
	proxyListCmd.Flags().BoolVarP(&isUnFiltered, "unfilter", "u", false, "provide unfiltered results")
	proxyListCmd.Flags().StringVarP(&ver, "ver", "v", version, "filter results by version")
	proxyListCmd.Flags().StringVarP(&country, "country", "c", "", "filter results by country")
	proxyListCmd.Flags().BoolVarP(&isStats, "stats", "s", false, "return only a count of the results")
}

var proxyStartCmd = &cobra.Command{
	Use:   "start",
	Short: "start the proxy client",
	//	Args:  cobra.MinimumNArgs(0),
	Run: func(cmd *cobra.Command, args []string) {
		//check that a valid public key is provided
		err := pubkey.Set(pk)
		if err != nil {
			if len(args) > 0 {
				err := pubkey.Set(args[0])
				if err != nil {
					internal.PrintFatalError(cmd.Flags(), err)
				}
			} else {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Invalid or missing public key"))
			}
		}
		//connect to RPC
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		//TODO: implement operational timeout
		internal.Catch(cmd.Flags(), rpcClient.StartSkysocksClient(pubkey.String()))
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
	Run: func(cmd *cobra.Command, args []string) {
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
	Short: "proxy client(s) status",
	Run: func(cmd *cobra.Command, args []string) {
		//TODO: check status of multiple clients
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
	Long:  "List proxy servers from service discovery\n http://sd.skycoin.com/api/services?type=proxy\n http://sd.skycoin.com/api/services?type=proxy&country=US",
	Run: func(cmd *cobra.Command, args []string) {
		if pk != "" {
			err := pubkey.Set(pk)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Invalid or missing public key"))
			}
		}
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
			var results []string
			limit := len(servers)
			if count > 0 && count < limit {
				limit = count
			}
			if pk != "" {
				for _, server := range servers {
					if strings.Replace(server.Addr.String(), ":44", "", 1) == pk {
						results = append(results, server.Addr.String())
					}
				}
			} else {
				for _, server := range servers {
					results = append(results, server.Addr.String())
				}
			}
			rand.Shuffle(len(results), func(i, j int) {
				results[i], results[j] = results[j], results[i]
			})
			for i := 0; i < limit && i < len(results); i++ {
				msg += strings.Replace(results[i], ":44", "", 1)
				if server := findServerByPK(servers, results[i]); server != nil && server.Geo != nil {
					if server.Geo.Country != "" {
						msg += fmt.Sprintf(" | %s\n", server.Geo.Country)
					} else {
						msg += "\n"
					}
				} else {
					msg += "\n"
				}
			}
			internal.PrintOutput(cmd.Flags(), servers, msg)
		}
	},
}

func findServerByPK(servers []servicedisc.Service, addr string) *servicedisc.Service {
	for _, server := range servers {
		if server.Addr.String() == addr {
			return &server
		}
	}
	return nil
}
