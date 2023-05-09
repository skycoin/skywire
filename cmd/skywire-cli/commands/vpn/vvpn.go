// Package clivpn cmd/skywire-cli/commands/vpn/vvpn.go
package clivpn

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/skyenv"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/visor"
)

func init() {
	RootCmd.PersistentFlags().StringVar(&clirpc.Addr, "rpc", "localhost:3435", "RPC server address")
	RootCmd.AddCommand(
		startCmd,
		stopCmd,
		statusCmd,
		listCmd,
	)
	version := buildinfo.Version()
	if version == "unknown" {
		version = ""
	}
	startCmd.Flags().StringVarP(&pk, "pk", "k", "", "server public key")
	listCmd.Flags().StringVarP(&sdURL, "url", "a", "", "service discovery url default:\n"+skyenv.ServiceDiscAddr)
	listCmd.Flags().BoolVarP(&directQuery, "direct", "b", false, "query service discovery directly")
	listCmd.Flags().StringVarP(&pk, "pk", "k", "", "check "+serviceType+" service discovery for public key")
	listCmd.Flags().IntVarP(&count, "num", "n", 0, "number of results to return")
	listCmd.Flags().BoolVarP(&isUnFiltered, "unfilter", "u", false, "provide unfiltered results")
	listCmd.Flags().StringVarP(&ver, "ver", "v", version, "filter results by version")
	listCmd.Flags().StringVarP(&country, "country", "c", "", "filter results by country")
	listCmd.Flags().BoolVarP(&isStats, "stats", "s", false, "return only a count of the results")
}

func findServerByPK(servers []servicedisc.Service, addr string) *servicedisc.Service {
	for _, server := range servers {
		if server.Addr.String() == addr {
			return &server
		}
	}
	return nil
}

var startCmd = &cobra.Command{
	Use:   "start <public-key>",
	Short: "start the " + serviceType + " for <public-key>",
	//	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		//check that a valid public key is provided
		err := pubkey.Set(pk)
		if err != nil {
			if len(args) > 0 {
				err := pubkey.Set(args[1])
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
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
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
				if state.Name == stateName {
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

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "stop the " + serviceType + "client",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		internal.Catch(cmd.Flags(), rpcClient.StopVPNClient(stateName))
		internal.PrintOutput(cmd.Flags(), "OK", fmt.Sprintln("OK"))
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: serviceType + " client status",
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
			if state.Name == stateName {

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

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List " + serviceType + " servers",
	Long:  "List " + serviceType + " servers from service discovery\n " + skyenv.ServiceDiscAddr + "/api/services?type=" + serviceType + "\n " + skyenv.ServiceDiscAddr + "/api/services?type=" + serviceType + "&country=US",
	Run: func(cmd *cobra.Command, args []string) {
		//validate any specified public key
		if pk != "" {
			err := pubkey.Set(pk)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Invalid or missing public key"))
			}
		}
		if sdURL == "" {
			sdURL = skyenv.ServiceDiscAddr
		}
		if isUnFiltered {
			ver = ""
			country = ""
		}
		if directQuery {
			servers = directQuerySD(cmd.Flags())
		} else {
			rpcClient, err := clirpc.Client(cmd.Flags())
			if err != nil {
				internal.PrintError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
				internal.PrintOutput(cmd.Flags(), fmt.Sprintf("directly querying service discovery\n%s/api/services?type=%s\n", sdURL, serviceType), fmt.Sprintf("directly querying service discovery\n%s/api/services?type=%s\n", sdURL, serviceType))
				servers = directQuerySD(cmd.Flags())
			} else {
				servers, err = rpcClient.VPNServers(ver, country)
				if err != nil {
					internal.PrintError(cmd.Flags(), err)
					internal.PrintOutput(cmd.Flags(), fmt.Sprintf("directly querying service discovery\n%s/api/services?type=%s\n", sdURL, serviceType), fmt.Sprintf("directly querying service discovery\n%s/api/services?type=%s\n", sdURL, serviceType))
					servers = directQuerySD(cmd.Flags())
				}
			}
		}
		if len(servers) == 0 {
			internal.PrintOutput(cmd.Flags(), "No Servers found", "No Servers found")
			os.Exit(0)
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
					if strings.Replace(server.Addr.String(), servicePort, "", 1) == pk {
						results = append(results, server.Addr.String())
					}
				}
			} else {
				for _, server := range servers {
					results = append(results, server.Addr.String())
				}
			}

			//randomize the order of the displayed results
			rand.Shuffle(len(results), func(i, j int) {
				results[i], results[j] = results[j], results[i]
			})
			for i := 0; i < limit && i < len(results); i++ {
				msg += strings.Replace(results[i], servicePort, "", 1)
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

func directQuerySD(cmdFlags *pflag.FlagSet) (s []servicedisc.Service) {
	//url/uri format
	//https://sd.skycoin.com/api/services?type=vpn&country=US&version=v1.3.7
	sdURL += "/api/services?type=" + serviceType
	if country != "" {
		sdURL += "&country=" + country
	}
	if ver != "" {
		sdURL += "&version=" + ver
	}
	//preform http get request for the service discovery URL
	resp, err := (&http.Client{Timeout: time.Duration(30 * time.Second)}).Get(sdURL)
	if err != nil {
		internal.PrintFatalError(cmdFlags, fmt.Errorf("error fetching servers from service discovery: %w", err))
	}
	defer resp.Body.Close() //nolint:errcheck
	// Decode JSON response into struct
	err = json.NewDecoder(resp.Body).Decode(&s)
	if err != nil {
		internal.PrintFatalError(cmdFlags, fmt.Errorf("error decoding json to struct: %w", err))
	}
	return s
}
