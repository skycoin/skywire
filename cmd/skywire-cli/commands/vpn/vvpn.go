// Package clivpn cmd/skywire-cli/commands/vpn/vvpn.go
package clivpn

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bitfield/script"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/tidwall/pretty"

	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/skyenv"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/app/appserver"
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
	if version == "unknown" {
		version = "" //nolint
	}
	startCmd.Flags().StringVarP(&pk, "pk", "k", "", "server public key")
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
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		internal.Catch(cmd.Flags(), rpcClient.StartVPNClient(pubkey))
		internal.PrintOutput(cmd.Flags(), nil, "Starting.")
		ctx, cancel := cmdutil.SignalContext(context.Background(), &logrus.Logger{})
		go func() {
			<-ctx.Done()
			cancel()
			rpcClient.StopVPNClient("vpn-client") //nolint
			os.Exit(1)
		}()
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

var isLabel bool

func init() {
	if version == "unknown" {
		version = ""
	}
	version = strings.Split(version, "-")[0]
	listCmd.Flags().StringVarP(&utURL, "uturl", "w", skyenv.UptimeTrackerAddr, "uptime tracker url")
	listCmd.Flags().StringVarP(&sdURL, "sdurl", "a", skyenv.ServiceDiscAddr, "service discovery url")
	listCmd.Flags().BoolVarP(&rawData, "raw", "r", false, "print raw data")
	listCmd.Flags().BoolVarP(&noFilterOnline, "noton", "o", false, "do not filter by online status in UT")
	listCmd.Flags().StringVar(&cacheFileSD, "cfs", "/tmp/vpnsd.json", "SD cache file location")
	listCmd.Flags().StringVar(&cacheFileUT, "cfu", "/tmp/ut.json", "UT cache file location.")
	listCmd.Flags().IntVarP(&cacheFilesAge, "cfa", "m", 5, "update cache files if older than n minutes")
	listCmd.Flags().StringVarP(&pk, "pk", "k", "", "check "+serviceType+" service discovery for public key")
	listCmd.Flags().BoolVarP(&isUnFiltered, "unfilter", "u", false, "provide unfiltered results")
	listCmd.Flags().StringVarP(&ver, "ver", "v", version, "filter results by version")
	listCmd.Flags().StringVarP(&country, "country", "c", "", "filter results by country")
	listCmd.Flags().BoolVarP(&isStats, "stats", "s", false, "return only a count of the results")
	listCmd.Flags().BoolVarP(&isLabel, "label", "l", false, "label keys by country \033[91m(SLOW)\033[0m")
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List servers",
	Long:  fmt.Sprintf("List %v servers from service discovery\n%v/api/services?type=%v\n%v/api/services?type=%v&country=US\n\nSet cache file location to \"\" to avoid using cache files", serviceType, skyenv.ServiceDiscAddr, serviceType, skyenv.ServiceDiscAddr, serviceType),
	Run: func(cmd *cobra.Command, args []string) {
		sds := getData(cacheFileSD, sdURL+"/api/services?type="+serviceType)
		if rawData {
			script.Echo(string(pretty.Color(pretty.Pretty([]byte(sds)), nil))).Stdout() //nolint
			return
		}
		if pk != "" {
			if isStats {
				count, _ := script.Echo(sds).JQ(`map(select(.address == "`+pk+`:3"))`).Replace("\"", "").Replace(":", " ").Column(1).CountLines() //nolint
				script.Echo(fmt.Sprintf("%v\n", count)).Stdout()                                                                                  //nolint
				return
			}
			jsonOut, _ := script.Echo(sds).JQ(`map(select(.address == "` + pk + `:3"))`).Bytes() //nolint
			script.Echo(string(pretty.Color(pretty.Pretty(jsonOut), nil))).Stdout()              //nolint
			return
		}
		var sdJQ string
		if !isUnFiltered {
			if ver != "" && country == "" {
				sdJQ = `select(.version == "` + ver + `")`
			}
			if country != "" && ver == "" {
				sdJQ = `select(.geo.country == "` + country + `")`
			}
			if country != "" && ver != "" {
				sdJQ = `select(.geo.country == "` + country + `" and .version == "` + ver + `")`
			}
		}
		if sdJQ != "" {
			sdJQ = `.[] | ` + sdJQ + ` | .address`
		} else {
			sdJQ = `.[] .address`
		}
		var sdkeys string
		sdkeys, _ = script.Echo(sds).JQ(sdJQ).Replace("\"", "").Replace(":", " ").Column(1).String() //nolint
		if noFilterOnline {
			if isStats {
				count, _ := script.Echo(sdkeys).CountLines()     //nolint
				script.Echo(fmt.Sprintf("%v\n", count)).Stdout() //nolint
				return
			}
			script.Echo(sdkeys).Stdout() //nolint
			return
		}
		uts := getData(cacheFileUT, utURL+"/uptimes?v=v2")
		utkeys, _ := script.Echo(uts).JQ(".[] | select(.on) | .pk").Replace("\"", "").String() //nolint
		if isStats {
			count, _ := script.Echo(sdkeys + utkeys).Freq().Match("2 ").Column(2).CountLines() //nolint
			script.Echo(fmt.Sprintf("%v\n", count)).Stdout()                                   //nolint
			return
		}
		if !isLabel {
			script.Echo(sdkeys + utkeys).Freq().Match("2 ").Column(2).Stdout() //nolint
		} else {
			filteredKeys, _ := script.Echo(sdkeys + utkeys).Freq().Match("2 ").Column(2).Slice()                           //nolint
			formattedoutput, _ := script.Echo(sds).JQ(".[] | \"\\(.address) \\(.geo.country)\"").Replace("\"", "").Slice() //nolint
			// Very slow!
			for _, fo := range formattedoutput {
				for _, fk := range filteredKeys {
					script.Echo(fo).Match(fk).Stdout() //nolint
				}
			}
		}

	},
}

func getData(cachefile, thisurl string) (thisdata string) {
	var shouldfetch bool
	buf1 := new(bytes.Buffer)
	cTime := time.Now()
	if cachefile == "" {
		thisdata, _ = script.NewPipe().WithHTTPClient(&http.Client{Timeout: 30 * time.Second}).Get(thisurl).String() //nolint
		return thisdata
	}
	if cachefile != "" {
		if u, err := os.Stat(cachefile); err != nil {
			shouldfetch = true
		} else {
			if cTime.Sub(u.ModTime()).Minutes() > float64(cacheFilesAge) {
				shouldfetch = true
			}
		}
		if shouldfetch {
			_, _ = script.NewPipe().WithHTTPClient(&http.Client{Timeout: 30 * time.Second}).Get(thisurl).Tee(buf1).WriteFile(cachefile) //nolint
			thisdata = buf1.String()
		} else {
			thisdata, _ = script.File(cachefile).String() //nolint
		}
	} else {
		thisdata, _ = script.NewPipe().WithHTTPClient(&http.Client{Timeout: 30 * time.Second}).Get(thisurl).String() //nolint
	}
	return thisdata
}
