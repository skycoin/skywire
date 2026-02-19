// Package clivpn cmd/skywire-cli/commands/vpn/vvpn.go
package clivpn

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bitfield/script"
	"github.com/blang/semver/v4"
	"github.com/pterm/pterm"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/tidwall/pretty"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/app/appserver"
	services "github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/visor"
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
	startCmd.Flags().StringVar(&geoipURL, "geoip", skyenv.GeoIP, "server public key")
	startCmd.Flags().IntVarP(&startingTimeout, "timeout", "t", 0, "starting timeout value in second")
	startCmd.Flags().BoolVar(&useInternal, "internal", false, "force internal launcher")
	startCmd.Flags().BoolVar(&useExternal, "external", false, "force external launcher")
	startCmd.MarkFlagsMutuallyExclusive("internal", "external")
	startCmd.Flags().BoolVar(&existingTpOnly, "existing-tp", false, "only use existing transports, don't create new ones")
	startCmd.Flags().BoolVar(&forceLocalRoutes, "local-route", false, "calculate routes locally instead of using route finder")
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

		// If --existing-tp flag is set, configure router to only use existing transports
		if existingTpOnly {
			if err := rpcClient.SetExistingTPOnly(true); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to set existing transport only mode: %w", err))
			}
		}

		// If --local-route flag is set, skip route finder and use local route calculation
		if forceLocalRoutes {
			if err := rpcClient.SetForceLocalRoutes(true); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to set force local routes mode: %w", err))
			}
		}

		launcherMode := ""
		if useInternal {
			launcherMode = "internal"
		} else if useExternal {
			launcherMode = "external"
		}

		internal.Catch(cmd.Flags(), rpcClient.StartVPNClientWithMode(pubkey, launcherMode))
		internal.PrintOutput(cmd.Flags(), nil, "Starting.")
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
			rpcClient.KillApp("vpn-client") //nolint:errcheck,gosec
			fmt.Print("\nStopped!")
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
						ip, err := visor.GetIP(geoipURL)
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

var (
	serverPort       = visorconfig.VPNServerPort
	serverPortString = fmt.Sprintf("%v", serverPort)
)

func init() {
	listCmd.Flags().StringVarP(&sdURL, "sdurl", "a", deployment.Prod.ServiceDiscovery, "service discovery url")
	listCmd.Flags().StringVarP(&utURL, "uturl", "w", deployment.Prod.UptimeTracker, "uptime tracker url")
	listCmd.Flags().BoolVarP(&rawData, "raw", "r", false, "pretty print json data")
	listCmd.Flags().BoolVarP(&noFilterOnline, "noton", "o", false, "do not filter by online status in UT")
	listCmd.Flags().StringVar(&cacheFileSD, "cfs", os.TempDir()+"/vpnsd.json", "SD cache file location")
	listCmd.Flags().StringVar(&cacheFileUT, "cfu", os.TempDir()+"/ut.json", "UT cache file location.")
	listCmd.Flags().IntVarP(&cacheFilesAge, "cfa", "m", 5, "update cache files if older than n minutes")
	listCmd.Flags().StringVarP(&pk, "pk", "k", "", "check "+serviceType+" service discovery for public key")
	listCmd.Flags().StringVarP(&country, "country", "c", "", "filter by country code")
	listCmd.Flags().StringVarP(&version, "version", "v", "", "filter by version")
	listCmd.Flags().StringVar(&minVersion, "minv", "", "filter by minimum version (>=)")
	listCmd.Flags().StringVar(&maxVersion, "maxv", "", "filter by maximum version (<=)")
	listCmd.Flags().BoolVar(&showOffline, "offline", false, "show only offline servers (red)")
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
			script.Echo(string(pretty.Color(pretty.Pretty([]byte(sds)), nil))).Stdout() //nolint:errcheck,gosec
			return
		}

		// --- If JSON output requested ---
		if jsonOutput {
			var list []services.Service
			json.Unmarshal([]byte(sds), &list) //nolint:errcheck,gosec
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
			script.Echo(string(pretty.Color(pretty.Pretty(jsonOut), nil))).Stdout() //nolint:errcheck,gosec
			return
		}

		// Parse minVersion if specified
		var minVer semver.Version
		if minVersion != "" {
			var err error
			minVer, err = semver.ParseTolerant(minVersion)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid minimum version %q: %w", minVersion, err))
			}
		}

		// Parse maxVersion if specified
		var maxVer semver.Version
		if maxVersion != "" {
			var err error
			maxVer, err = semver.ParseTolerant(maxVersion)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid maximum version %q: %w", maxVersion, err))
			}
		}

		// Helper to check if version meets minimum requirement
		meetsMinVersion := func(ver string) bool {
			if minVersion == "" {
				return true
			}
			v, err := semver.ParseTolerant(ver)
			if err != nil {
				return false
			}
			return v.GTE(minVer)
		}

		// Helper to check if version meets maximum requirement
		meetsMaxVersion := func(ver string) bool {
			if maxVersion == "" {
				return true
			}
			v, err := semver.ParseTolerant(ver)
			if err != nil {
				return false
			}
			return v.LTE(maxVer)
		}

		// --- Show only offline servers ---
		if showOffline {
			uts := internal.GetData(cacheFileUT, utURL+"/uptimes?v=v2", cacheFilesAge)

			// Parse SD and UT data
			var sdEntries []services.Service
			json.Unmarshal([]byte(sds), &sdEntries) //nolint:errcheck,gosec

			type utEntry struct {
				PK string `json:"pk"`
				On bool   `json:"on"`
			}
			var utEntries []utEntry
			json.Unmarshal([]byte(uts), &utEntries) //nolint:errcheck,gosec

			// Build online set
			onlinePKs := make(map[string]bool)
			for _, ut := range utEntries {
				if ut.On {
					onlinePKs[ut.PK] = true
				}
			}

			// Filter and print offline entries
			count := 0
			for _, svc := range sdEntries {
				addrParts := strings.Split(svc.Addr.String(), ":")
				if len(addrParts) < 1 {
					continue
				}
				pkStr := addrParts[0]

				// Skip if online
				if onlinePKs[pkStr] {
					continue
				}

				// Apply country filter
				if country != "" && (svc.Geo == nil || svc.Geo.Country != country) {
					continue
				}

				// Apply version filter
				if version != "" && svc.Version != version {
					continue
				}

				// Apply minVersion filter
				if !meetsMinVersion(svc.Version) {
					continue
				}

				// Apply maxVersion filter
				if !meetsMaxVersion(svc.Version) {
					continue
				}

				count++
				if !isStats {
					countryCode := ""
					if svc.Geo != nil {
						countryCode = svc.Geo.Country
					}
					fmt.Println(pterm.Red(fmt.Sprintf("%s %s %s", pkStr, countryCode, svc.Version)))
				}
			}

			if isStats {
				fmt.Printf("%d\n", count)
			}
			return
		}

		// --- No filtering case ---
		if noFilterOnline {
			// Parse SD data for minVersion/maxVersion filtering
			if minVersion != "" || maxVersion != "" {
				var sdEntries []services.Service
				json.Unmarshal([]byte(sds), &sdEntries) //nolint:errcheck,gosec

				count := 0
				for _, svc := range sdEntries {
					addrParts := strings.Split(svc.Addr.String(), ":")
					if len(addrParts) < 1 {
						continue
					}
					pkStr := addrParts[0]

					// Apply country filter
					if country != "" && (svc.Geo == nil || svc.Geo.Country != country) {
						continue
					}

					// Apply version filter
					if version != "" && svc.Version != version {
						continue
					}

					// Apply minVersion filter
					if !meetsMinVersion(svc.Version) {
						continue
					}

					// Apply maxVersion filter
					if !meetsMaxVersion(svc.Version) {
						continue
					}

					count++
					if !isStats {
						countryCode := ""
						if svc.Geo != nil {
							countryCode = svc.Geo.Country
						}
						fmt.Printf("%s %s %s\n", pkStr, countryCode, svc.Version)
					}
				}

				if isStats {
					fmt.Printf("%d\n", count)
				}
				return
			}

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
				script.Echo(fmt.Sprintf("%v\n", count)).Stdout() //nolint:errcheck,gosec
				return
			}
			script.Echo(sds).JQ(sdJQ).Replace(`"`, "").Stdout() //nolint:errcheck,gosec
			return
		}

		// --- Filtering by online status ---
		uts := internal.GetData(cacheFileUT, utURL+"/uptimes?v=v2", cacheFilesAge)

		// Use Go-based filtering when minVersion or maxVersion is specified (jq can't do semver comparison)
		if minVersion != "" || maxVersion != "" {
			var sdEntries []services.Service
			json.Unmarshal([]byte(sds), &sdEntries) //nolint:errcheck,gosec

			type utEntry struct {
				PK string `json:"pk"`
				On bool   `json:"on"`
			}
			var utEntries []utEntry
			json.Unmarshal([]byte(uts), &utEntries) //nolint:errcheck,gosec

			// Build online set
			onlinePKs := make(map[string]bool)
			for _, ut := range utEntries {
				if ut.On {
					onlinePKs[ut.PK] = true
				}
			}

			count := 0
			for _, svc := range sdEntries {
				addrParts := strings.Split(svc.Addr.String(), ":")
				if len(addrParts) < 1 {
					continue
				}
				pkStr := addrParts[0]

				// Skip if offline
				if !onlinePKs[pkStr] {
					continue
				}

				// Apply country filter
				if country != "" && (svc.Geo == nil || svc.Geo.Country != country) {
					continue
				}

				// Apply version filter
				if version != "" && svc.Version != version {
					continue
				}

				// Apply minVersion filter
				if !meetsMinVersion(svc.Version) {
					continue
				}

				// Apply maxVersion filter
				if !meetsMaxVersion(svc.Version) {
					continue
				}

				count++
				if !isStats {
					countryCode := ""
					if svc.Geo != nil {
						countryCode = svc.Geo.Country
					}
					fmt.Printf("%s %s %s\n", pkStr, countryCode, svc.Version)
				}
			}

			if isStats {
				fmt.Printf("%d\n", count)
			}
			return
		}

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
			script.Echo(fmt.Sprintf("%v\n", count)).Stdout() //nolint:errcheck,gosec
			return
		}

		script.Echo(joinedJSON).JQ(jqFilter).Replace(`"`, "").Stdout() //nolint:errcheck,gosec
	},
}
