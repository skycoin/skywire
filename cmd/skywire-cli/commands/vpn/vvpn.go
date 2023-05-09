// Package clivpn cmd/skywire-cli/commands/vpn/vvpn.go
package clivpn

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
	"github.com/skycoin/skywire/pkg/visor"
)

func init() {
	RootCmd.AddCommand(
		vpnStartCmd,
		vpnStopCmd,
		vpnStatusCmd,
		vpnListCmd,
	)
	version := buildinfo.Version()
	if version == "unknown" {
		version = ""
	}
	vpnStartCmd.Flags().StringVarP(&pk, "pk", "k", "", "server public key")
	vpnListCmd.Flags().StringVarP(&pk, "pk", "k", "", "check proxy service discovery for public key")
	vpnListCmd.Flags().IntVarP(&count, "num", "n", 0, "number of results to return")
	vpnListCmd.Flags().BoolVarP(&isUnFiltered, "unfilter", "u", false, "provide unfiltered results")
	vpnListCmd.Flags().StringVarP(&ver, "ver", "v", version, "filter results by version")
	vpnListCmd.Flags().StringVarP(&country, "country", "c", "", "filter results by country")
	vpnListCmd.Flags().BoolVarP(&isStats, "stats", "s", false, "return only a count of the results")
}

var vpnListCmd = &cobra.Command{
	Use:   "list",
	Short: "List servers",
	Long:  "List vpn servers from service discovery\n http://sd.skycoin.com/api/services?type=vpn\n http://sd.skycoin.com/api/services?type=vpn&country=US",
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
		servers, err := rpcClient.VPNServers(ver, country)
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
					if strings.Replace(server.Addr.String(), ":3", "", 1) == pk {
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
				msg += strings.Replace(results[i], ":3", "", 1)
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

var vpnStartCmd = &cobra.Command{
	Use:   "start <public-key>",
	Short: "start the vpn for <public-key>",
	//	Args:  cobra.MinimumNArgs(1),
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
		internal.Catch(cmd.Flags(), pubkey.Set(args[0]))
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.StartVPNClient(pubkey))
		internal.PrintOutput(cmd.Flags(), nil, "Starting.")
		startProcess := true
		for startProcess {
			time.Sleep(time.Second * 1)
			internal.PrintOutput(cmd.Flags(), nil, ".")
			states, err := rpcClient.Apps()
			internal.Catch(cmd.Flags(), err)

			type output struct {
				CurrentIP string `json:"current_ip,omitempty"`
				AppError  string `json:"app_error,omitempty"`
			}

			for _, state := range states {
				if state.Name == "vpn-client" {
					if state.Status == appserver.AppStatusRunning {
						startProcess = false
						internal.PrintOutput(cmd.Flags(), nil, fmt.Sprintln("\nRunning!"))
						ip, err := visor.GetIP()
						out := output{
							CurrentIP: ip,
						}
						if err == nil {
							internal.PrintOutput(cmd.Flags(), out, fmt.Sprintf("Your current IP: %s\n", ip))
						}
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

var vpnStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "stop the vpn",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.StopVPNClient("vpn-client"))
		internal.PrintOutput(cmd.Flags(), "OK", fmt.Sprintln("OK"))
	},
}

var vpnStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "vpn status",
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
			if state.Name == "vpn-client" {

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
