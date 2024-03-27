// Package skysocksc cmd/skywire-cli/commands/skysocksc/skysocks.go
package skysocksc

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bitfield/script"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/tidwall/pretty"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/skyenv"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
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
		version = "" //nolint
	}
	startCmd.Flags().StringVarP(&pk, "pk", "k", "", "server public key")
	startCmd.Flags().StringVarP(&addr, "addr", "a", "", "address of proxy for use")
	startCmd.Flags().StringVarP(&clientName, "name", "n", "", "name of skysocks client")
	startCmd.Flags().IntVarP(&startingTimeout, "timeout", "t", 0, "timeout for starting proxy")
	startCmd.Flags().StringVar(&httpAddr, "http", "", "address for http proxy")
	stopCmd.Flags().BoolVar(&allClients, "all", false, "stop all skysocks client")
	stopCmd.Flags().StringVar(&clientName, "name", "", "specific skysocks client that want stop")
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "start the " + serviceType + " client",
	Run: func(cmd *cobra.Command, args []string) {

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}

		// stop possible running proxy before start it again
		if clientName != "" {
			rpcClient.StopApp(clientName) //nolint
		} else {
			rpcClient.StopApp("skysocks-client") //nolint
		}

		tCtx := context.Background() //nolint
		if startingTimeout != 0 {
			tCtx, _ = context.WithTimeout(context.Background(), time.Duration(startingTimeout)*time.Second) //nolint
		}
		ctx, cancel := cmdutil.SignalContext(tCtx, &logrus.Logger{})
		go func() {
			<-ctx.Done()
			cancel()
			rpcClient.KillApp(clientName) //nolint
			fmt.Print("\nStopped!")
			os.Exit(1)
		}()

		if pk != "" {
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
			arguments := map[string]any{}
			arguments["app"] = "skysocks-client"

			arguments["--srv"] = pubkey.String()

			if addr == "" {
				addr = visorconfig.SkysocksClientAddr
			}
			arguments["--addr"] = addr

			if httpAddr != "" {
				arguments["--http"] = httpAddr
			}

			if clientName == "" {
				clientName = "skysocks-client"
			}

			_, err = rpcClient.App(clientName)
			if err == nil {
				err = rpcClient.DoCustomSetting(clientName, arguments)
				if err != nil {
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Error occurs during set args to custom skysocks client"))
				}
			} else {
				err = rpcClient.AddApp(clientName, "skywire")
				if err != nil {
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Error during add new app"))
				}
				err = rpcClient.DoCustomSetting(clientName, arguments)
				if err != nil {
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Error occurs during set args to custom skysocks client"))
				}
			}
			internal.Catch(cmd.Flags(), rpcClient.StartApp(clientName))
			internal.PrintOutput(cmd.Flags(), nil, "Starting.")
		} else {
			if clientName == "" {
				clientName = "skysocks-client"
			}
			internal.Catch(cmd.Flags(), rpcClient.StartApp(clientName))
			internal.PrintOutput(cmd.Flags(), nil, "Starting.")
		}

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
				if state.Name == stateName {
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
					if state.Status == appserver.AppStatusStopped {
						startProcess = false
						out := output{
							AppError: state.DetailedStatus,
						}
						internal.PrintOutput(cmd.Flags(), out, fmt.Sprintln("\nStopped!"+state.DetailedStatus))
					}
				}
			}
		}
	},
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "stop the " + serviceType + " client",
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		if allClients && clientName != "" {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("cannot use both --all and --name flag in together"))
		}
		if !allClients && clientName == "" {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("you should use one of flags, --all or --name"))
		}
		if allClients {
			internal.Catch(cmd.Flags(), rpcClient.StopSkysocksClients())
			internal.PrintOutput(cmd.Flags(), "all skysocks client stopped", fmt.Sprintln("all skysocks clients stopped"))
			return
		}
		internal.Catch(cmd.Flags(), rpcClient.StopApp(clientName))
		internal.PrintOutput(cmd.Flags(), fmt.Sprintf("skysocks client %s stopped", clientName), fmt.Sprintf("skysocks client %s stopped\n", clientName))
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: serviceType + " client status",
	Run: func(cmd *cobra.Command, args []string) {
		//TODO: check status of multiple clients
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		states, err := rpcClient.Apps()
		internal.Catch(cmd.Flags(), err)

		var b bytes.Buffer
		w := tabwriter.NewWriter(&b, 0, 0, 5, ' ', tabwriter.TabIndent)
		internal.Catch(cmd.Flags(), err)
		type appState struct {
			Name      string       `json:"name"`
			Status    string       `json:"status"`
			AutoStart bool         `json:"autostart"`
			Args      []string     `json:"args"`
			AppPort   routing.Port `json:"app_port"`
		}
		var jsonAppStatus []appState
		fmt.Fprintf(w, "---- All Proxy List -----------------------------------------------------\n\n")
		for _, state := range states {
			for _, v := range state.AppConfig.Args {
				if v == binaryName {
					status := "stopped"
					if state.Status == appserver.AppStatusRunning {
						status = "running"
					}
					if state.Status == appserver.AppStatusErrored {
						status = "errored"
					}
					jsonAppStatus = append(jsonAppStatus, appState{
						Name:      state.Name,
						Status:    status,
						AutoStart: state.AutoStart,
						Args:      state.Args,
						AppPort:   state.Port,
					})
					var tmpAddr string
					var tmpSrv string
					for idx, arg := range state.Args {
						if arg == "--srv" {
							tmpSrv = state.Args[idx+1]
						}
						if arg == "--addr" {
							tmpAddr = "127.0.0.1" + state.Args[idx+1]
						}
					}
					_, err = fmt.Fprintf(w, "Name: %s\nStatus: %s\nServer: %s\nAddress: %s\nAppPort: %d\nAutoStart: %t\n\n", state.Name, status, tmpSrv, tmpAddr, state.Port, state.AutoStart)
					internal.Catch(cmd.Flags(), err)
				}
			}
		}
		fmt.Fprintf(w, "-------------------------------------------------------------------------\n")
		internal.Catch(cmd.Flags(), w.Flush())
		internal.PrintOutput(cmd.Flags(), jsonAppStatus, b.String())
	},
}

var isLabel bool

func init() {
	if version == "unknown" {
		version = "" //nolint
	}
	version = strings.Split(version, "-")[0]
	listCmd.Flags().StringVarP(&utURL, "uturl", "w", skyenv.UptimeTrackerAddr, "uptime tracker url")
	listCmd.Flags().StringVarP(&sdURL, "sdurl", "a", skyenv.ServiceDiscAddr, "service discovery url")
	listCmd.Flags().BoolVarP(&rawData, "raw", "r", false, "print raw data")
	listCmd.Flags().BoolVarP(&noFilterOnline, "noton", "o", false, "do not filter by online status in UT")
	listCmd.Flags().StringVar(&cacheFileSD, "cfs", os.TempDir()+"/proxysd.json", "SD cache file location")
	listCmd.Flags().StringVar(&cacheFileUT, "cfu", os.TempDir()+"/ut.json", "UT cache file location.")
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
		sds := internal.GetData(cacheFileSD, sdURL+"/api/services?type="+serviceType, cacheFilesAge)
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
		uts := internal.GetData(cacheFileUT, utURL+"/uptimes?v=v2", cacheFilesAge)
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
