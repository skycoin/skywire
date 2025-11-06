// Package skysocksc cmd/skywire-cli/commands/skysocksc/skysocks.go
package skysocksc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/bitfield/script"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/tidwall/pretty"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routing"
	services "github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
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
	startCmd.Flags().StringVarP(&pk, "pk", "k", "", "server public key")
	startCmd.Flags().StringVarP(&addr, "addr", "a", visorconfig.SkysocksClientAddr, "address of proxy for use")
	startCmd.Flags().StringVarP(&clientName, "name", "n", "", "name of skysocks client")
	startCmd.Flags().IntVarP(&startingTimeout, "timeout", "t", 0, "timeout for starting proxy")
	startCmd.Flags().StringVar(&httpAddr, "http", "", "address for http proxy")
	startCmd.Flags().Uint16Var(&appPort, "port", 0, "routing port for communication between proxy (skysocks) and visor")
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
			rpcClient.StopApp(clientName) //nolint:errcheck
		} else {
			rpcClient.StopApp("skysocks-client") //nolint:errcheck
		}

		var tCtxCancelFunc context.CancelFunc
		tCtx := context.Background()
		if startingTimeout != 0 {
			tCtx, tCtxCancelFunc = context.WithTimeout(context.Background(), time.Duration(startingTimeout)*time.Second)
		}
		ctx, cancel := cmdutil.SignalContext(tCtx, &logrus.Logger{})
		go func() {
			<-ctx.Done()
			cancel()
			if tCtxCancelFunc != nil {
				tCtxCancelFunc()
			}
			rpcClient.KillApp(clientName) //nolint:errcheck
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

			arguments["--addr"] = addr

			if httpAddr != "" {
				arguments["--http"] = httpAddr
			}

			if clientName == "" {
				clientName = "skysocks-client"
			}

			if appPort != 0 {
				arguments["appPort"] = appPort
			}

			_, err = rpcClient.App(clientName)
			if err == nil {
				err = rpcClient.DoCustomSetting(clientName, arguments)
				if err != nil {
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Error occurs during set args to custom skysocks client. error: %s", err))
				}
			} else {
				err = rpcClient.AddApp(clientName, "skywire")
				if err != nil {
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Error during add new app. error: %s", err))
				}
				err = rpcClient.DoCustomSetting(clientName, arguments)
				if err != nil {
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Error occurs during set args to custom skysocks client. error: %s", err))
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
	Run: func(cmd *cobra.Command, _ []string) {
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
	Run: func(cmd *cobra.Command, _ []string) {
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
		_, err = fmt.Fprintf(w, "---- All Proxy List -----------------------------------------------------\n\n")
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("error on fmt.Fprintf: %w", err))
		}
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
		_, err = fmt.Fprintf(w, "-------------------------------------------------------------------------\n")
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("error on fmt.Fprintf: %w", err))
		}
		internal.Catch(cmd.Flags(), w.Flush())
		internal.PrintOutput(cmd.Flags(), jsonAppStatus, b.String())
	},
}

var (
	serverPort       = visorconfig.SkysocksPort
	serverPortString = fmt.Sprintf("%v", serverPort)
)

func init() {
	listCmd.Flags().StringVarP(&sdURL, "sdurl", "a", deployment.Prod.ServiceDiscovery, "service discovery url")
	listCmd.Flags().StringVarP(&utURL, "uturl", "w", deployment.Prod.UptimeTracker, "uptime tracker url")
	listCmd.Flags().BoolVarP(&rawData, "raw", "r", false, "print raw json data")
	listCmd.Flags().BoolVarP(&noFilterOnline, "noton", "o", false, "do not filter by online status in UT")
	listCmd.Flags().StringVar(&cacheFileSD, "cfs", os.TempDir()+"/proxysd.json", "SD cache file location")
	listCmd.Flags().StringVar(&cacheFileUT, "cfu", os.TempDir()+"/ut.json", "UT cache file location.")
	listCmd.Flags().IntVarP(&cacheFilesAge, "cfa", "m", 5, "update cache files if older than n minutes")
	listCmd.Flags().StringVarP(&pk, "pk", "k", "", "check "+serviceType+" service discovery for public key")
	listCmd.Flags().StringVarP(&country, "country", "c", "", "filter by country code")
	listCmd.Flags().StringVarP(&version, "version", "v", "", "filter by version")
	listCmd.Flags().BoolVarP(&isStats, "stats", "s", false, "return only a count of the results")
	listCmd.Flags().BoolVar(&jsonOutput, internal.JSONString, false, "print output in json")
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List servers",
	Long:  fmt.Sprintf("List %v servers from service discovery\n%v/api/services?type=%v\n%v/api/services?type=%v&country=US\n\nSet cache file location to \"\" to avoid using cache files\ndefault virtual port of servers: %v", serviceType, deployment.Prod.ServiceDiscovery, serviceType, deployment.Prod.ServiceDiscovery, serviceType, serverPort),
	Run: func(cmd *cobra.Command, _ []string) {
		// --- Fetch SD ---
		sds := internal.GetData(cacheFileSD, sdURL+"/api/services?type="+serviceType, cacheFilesAge)
		if rawData {
			script.Echo(string(pretty.Color(pretty.Pretty([]byte(sds)), nil))).Stdout()
			return
		}

		// --- If JSON output requested ---
		if jsonOutput {
			var list []services.Service
			json.Unmarshal([]byte(sds), &list) //nolint:errcheck
			var b bytes.Buffer
			internal.PrintOutput(cmd.Flags(), list, b.String())
			return
		}

		// --- Lookup by PK ---
		if pk != "" {
			jsonOut, err := script.Echo(sds).
				JQ(`map(select(.address == "` + pk + `:` + serverPortString + `"))`).Bytes()
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("error: %w", err))
			}
			script.Echo(string(pretty.Color(pretty.Pretty(jsonOut), nil))).Stdout()
			return
		}

		// --- No filtering case ---
		if noFilterOnline {
			sdJQ := `.[]`
			if country != "" {
				sdJQ += ` | select(.geo.country == "` + country + `")`
			}
			if version != "" {
				sdJQ += ` | select(.version == "` + version + `")`
			}
			sdJQ += ` | "\(.address | split(":")[0]) \(.geo.country) \(.version)"`

			if isStats {
				count, err := script.Echo(sds).JQ(sdJQ).Replace(`"`, "").CountLines()
				if err != nil {
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("error: %w", err))
				}
				script.Echo(fmt.Sprintf("%v\n", count)).Stdout()
				return
			}
			script.Echo(sds).JQ(sdJQ).Replace(`"`, "").Stdout()
			return
		}

		// --- Filtering by online status via jq join ---
		uts := internal.GetData(cacheFileUT, utURL+"/uptimes?v=v2", cacheFilesAge)
		joinedJSON := fmt.Sprintf(`{"sd": %s, "ut": %s}`, sds, uts)

		// Build jq filter with optional country and version conditions
		countryCond := ""
		if country != "" {
			countryCond = ` | select(.geo.country == "` + country + `")`
		}
		versionCond := ""
		if version != "" {
			versionCond = ` | select(.version == "` + version + `")`
		}

		jqFilter := `
		[ .ut[] | select(.on) | .pk ] as $online
		| .sd[]
		| select((.address | split(":")[0]) as $pk | $pk | IN($online[]))` + countryCond + versionCond + `
		| "\((.address | split(":")[0])) \(.geo.country) \(.version)"
		`

		if isStats {
			count, err := script.Echo(joinedJSON).JQ(jqFilter).Replace(`"`, "").CountLines()
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("error: %w", err))
			}
			script.Echo(fmt.Sprintf("%v\n", count)).Stdout()
			return
		}

		script.Echo(joinedJSON).JQ(jqFilter).Replace(`"`, "").Stdout()
	},
}
