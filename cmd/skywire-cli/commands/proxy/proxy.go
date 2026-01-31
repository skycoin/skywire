// Package skysocksc cmd/skywire-cli/commands/skysocksc/skysocks.go
package skysocksc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/bitfield/script"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/tidwall/pretty"
	"golang.org/x/net/proxy"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routing"
	services "github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// proxyTestClient is a minimal interface for proxy testing
type proxyTestClient interface {
	StopApp(appName string) error
	StartApp(appName string) error
	AddApp(appName, binaryName string) error
	App(appName string) (*appserver.AppState, error)
	Apps() ([]*appserver.AppState, error)
	DoCustomSetting(appName string, customSetting map[string]any) error
	AddTransport(remote cipher.PubKey, tpType string, timeout time.Duration) (*visor.TransportSummary, error)
}

func init() {
	RootCmd.PersistentFlags().StringVar(&clirpc.Addr, "rpc", "localhost:3435", "RPC server address")
	RootCmd.AddCommand(
		startCmd,
		stopCmd,
		statusCmd,
		listCmd,
		testCmd,
	)
	startCmd.Flags().StringVarP(&pk, "pk", "k", "", "server public key")
	startCmd.Flags().StringVarP(&addr, "addr", "a", visorconfig.SkysocksClientAddr, "address of proxy for use")
	startCmd.Flags().StringVarP(&clientName, "name", "n", "", "name of skysocks client")
	startCmd.Flags().IntVarP(&startingTimeout, "timeout", "t", 0, "timeout for starting proxy")
	startCmd.Flags().StringVar(&httpAddr, "http", "", "address for http proxy")
	startCmd.Flags().Uint16Var(&appPort, "port", 0, "routing port for communication between proxy (skysocks) and visor")
	startCmd.Flags().BoolVar(&useInternal, "internal", false, "force internal launcher")
	startCmd.Flags().BoolVar(&useExternal, "external", false, "force external launcher")
	startCmd.MarkFlagsMutuallyExclusive("internal", "external")
	stopCmd.Flags().BoolVar(&allClients, "all", false, "stop all skysocks client")
	stopCmd.Flags().StringVar(&clientName, "name", "", "specific skysocks client that want stop")
	// test command flags
	testCmd.Flags().StringVarP(&testURL, "url", "u", "https://ip.skycoin.com", "URL to fetch through proxy for testing")
	testCmd.Flags().IntVarP(&testTimeout, "timeout", "t", 30, "timeout in seconds for each proxy test")
	testCmd.Flags().IntVarP(&testBatchSize, "batch", "b", 5, "number of proxies to test in parallel")
	testCmd.Flags().BoolVarP(&testOnlyWithTp, "transport", "p", false, "only test proxies that have an existing transport")
	testCmd.Flags().BoolVarP(&testVerbose, "verbose", "v", false, "verbose output")
	testCmd.Flags().StringVarP(&sdURL, "sdurl", "a", deployment.Prod.ServiceDiscovery, "service discovery url")
	testCmd.Flags().StringVarP(&utURL, "uturl", "w", deployment.Prod.UptimeTracker, "uptime tracker url")
	testCmd.Flags().StringVar(&cacheFileSD, "cfs", os.TempDir()+"/proxysd.json", "SD cache file location")
	testCmd.Flags().StringVar(&cacheFileUT, "cfu", os.TempDir()+"/ut.json", "UT cache file location")
	testCmd.Flags().IntVarP(&cacheFilesAge, "cfa", "m", 5, "update cache files if older than n minutes")
	testCmd.Flags().StringVarP(&country, "country", "k", "", "filter proxies by country code")
	testCmd.Flags().BoolVarP(&testConnectOnly, "connect", "c", false, "connect only mode: add transports without HTTP testing")
	testCmd.Flags().StringVarP(&testVersion, "version", "V", "", "filter proxies by version (empty to skip)")
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
			rpcClient.StopApp(clientName) //nolint:errcheck,gosec
		} else {
			rpcClient.StopApp("skysocks-client") //nolint:errcheck,gosec
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
			rpcClient.KillApp(clientName) //nolint:errcheck,gosec
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
			internal.Catch(cmd.Flags(), rpcClient.StartAppWithMode(clientName, getLauncherMode()))
			internal.PrintOutput(cmd.Flags(), nil, "Starting.")
		} else {
			if clientName == "" {
				clientName = "skysocks-client"
			}
			internal.Catch(cmd.Flags(), rpcClient.StartAppWithMode(clientName, getLauncherMode()))
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
				script.Echo(fmt.Sprintf("%v\n", count)).Stdout() //nolint:errcheck,gosec
				return
			}
			script.Echo(sds).JQ(sdJQ).Replace(`"`, "").Stdout() //nolint:errcheck,gosec
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
			script.Echo(fmt.Sprintf("%v\n", count)).Stdout() //nolint:errcheck,gosec
			return
		}

		script.Echo(joinedJSON).JQ(jqFilter).Replace(`"`, "").Stdout() //nolint:errcheck,gosec
	},
}

func getLauncherMode() string {
	if useInternal {
		return "internal"
	} else if useExternal {
		return "external"
	}
	return ""
}

// ProxyTestResult holds the result of testing a single proxy
type ProxyTestResult struct {
	PublicKey    string  `json:"public_key"`
	Country      string  `json:"country,omitempty"`
	Version      string  `json:"version,omitempty"`
	HasTransport bool    `json:"has_transport"`
	Connected    bool    `json:"connected,omitempty"`
	Success      bool    `json:"success"`
	Latency      float64 `json:"latency_ms,omitempty"`
	Response     string  `json:"response,omitempty"`
	Error        string  `json:"error,omitempty"`
}

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Test proxy servers from service discovery",
	Long: `Fetch proxy servers from service discovery and test connectivity.
For each proxy, check if visor has a transport to it, then attempt to
make an HTTP request through the proxy to verify it's working.

With --connect flag, connects to all online proxies (adds transports)
without HTTP testing. Use --version to filter by visor version.

Results show which proxies are reachable and their response latency.`,
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}

		// Fetch proxy servers from service discovery
		sds := internal.GetData(cacheFileSD, sdURL+"/api/services?type="+serviceType, cacheFilesAge)
		var proxyServices []services.Service
		if err := json.Unmarshal([]byte(sds), &proxyServices); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to parse service discovery response: %w", err))
		}

		// Filter by country if specified
		if country != "" {
			var filtered []services.Service
			for _, svc := range proxyServices {
				if svc.Geo != nil && strings.EqualFold(svc.Geo.Country, country) {
					filtered = append(filtered, svc)
				}
			}
			proxyServices = filtered
		}

		// Filter by version if specified
		if testVersion != "" {
			var filtered []services.Service
			for _, svc := range proxyServices {
				if strings.HasPrefix(svc.Version, testVersion) {
					filtered = append(filtered, svc)
				}
			}
			proxyServices = filtered
		}

		if len(proxyServices) == 0 {
			internal.PrintOutput(cmd.Flags(), nil, "No proxy servers found\n")
			return
		}

		if testVerbose {
			fmt.Printf("Found %d proxy servers\n", len(proxyServices))
		}

		// Get current transports from visor
		transports, err := rpcClient.Transports(nil, nil, false)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to get transports: %w", err))
		}

		// Build a set of PKs we have transports to
		transportPKs := make(map[string]bool)
		for _, tp := range transports {
			transportPKs[tp.Remote.String()] = true
		}

		// Collect proxies to test
		type proxyToTest struct {
			pk           cipher.PubKey
			country      string
			version      string
			hasTransport bool
		}
		var proxiesToTest []proxyToTest

		for _, svc := range proxyServices {
			// Parse PK from address (format: pk:port)
			addrParts := strings.Split(svc.Addr.String(), ":")
			if len(addrParts) < 1 {
				continue
			}
			pkStr := addrParts[0]
			var pk cipher.PubKey
			if err := pk.Set(pkStr); err != nil {
				continue
			}

			hasTransport := transportPKs[pkStr]

			// In connect-only mode, skip proxies we already have transports to
			if testConnectOnly && hasTransport {
				continue
			}

			// In normal test mode with --transport flag, skip proxies without transport
			if !testConnectOnly && testOnlyWithTp && !hasTransport {
				continue
			}

			countryCode := ""
			if svc.Geo != nil {
				countryCode = svc.Geo.Country
			}

			proxiesToTest = append(proxiesToTest, proxyToTest{
				pk:           pk,
				country:      countryCode,
				version:      svc.Version,
				hasTransport: hasTransport,
			})
		}

		if len(proxiesToTest) == 0 {
			internal.PrintOutput(cmd.Flags(), nil, "No proxies to test (with current filters)\n")
			return
		}

		if testVerbose {
			if testConnectOnly {
				fmt.Printf("Connecting to %d proxies\n", len(proxiesToTest))
			} else {
				withTp := 0
				for _, p := range proxiesToTest {
					if p.hasTransport {
						withTp++
					}
				}
				fmt.Printf("Testing %d proxies (%d with existing transport)\n", len(proxiesToTest), withTp)
			}
		}

		// Process in batches
		results := make([]ProxyTestResult, len(proxiesToTest))
		var wg sync.WaitGroup
		semaphore := make(chan struct{}, testBatchSize)
		var completed int32
		var mu sync.Mutex

		// Progress indicator
		total := len(proxiesToTest)
		fmt.Printf("Processing %d proxies...\n", total)

		for i, p := range proxiesToTest {
			wg.Add(1)
			go func(idx int, pxy proxyToTest) {
				defer wg.Done()
				semaphore <- struct{}{}
				defer func() { <-semaphore }()

				result := ProxyTestResult{
					PublicKey:    pxy.pk.String(),
					Country:      pxy.country,
					Version:      pxy.version,
					HasTransport: pxy.hasTransport,
				}

				if testConnectOnly {
					// Connect-only mode: just add transport
					start := time.Now()
					// Try STCPR first (for public visors), then SUDPH
					_, err := rpcClient.AddTransport(pxy.pk, "stcpr", time.Duration(testTimeout)*time.Second)
					if err != nil {
						// Try SUDPH as fallback
						_, err = rpcClient.AddTransport(pxy.pk, "sudph", time.Duration(testTimeout)*time.Second)
					}
					if err != nil {
						result.Success = false
						result.Connected = false
						result.Error = err.Error()
					} else {
						result.Success = true
						result.Connected = true
						result.Latency = float64(time.Since(start).Milliseconds())
					}
				} else {
					// Normal test mode: test proxy via HTTP
					// Use unique port per test to avoid conflicts
					port := 10800 + idx
					latency, response, err := testProxyServerWithPort(rpcClient, pxy.pk, testURL, testTimeout, port)
					if err != nil {
						result.Success = false
						result.Error = err.Error()
					} else {
						result.Success = true
						result.Latency = latency
						result.Response = response
					}
				}

				results[idx] = result

				// Update progress
				mu.Lock()
				completed++
				current := completed
				mu.Unlock()

				if testVerbose {
					status := "✓"
					if !result.Success {
						status = "✗"
					}
					verStr := ""
					if pxy.version != "" {
						verStr = " [" + pxy.version + "]"
					}
					if result.Success {
						fmt.Printf("[%d/%d] %s %s%s - %.0fms\n", current, total, status, pxy.pk.String()[:8], verStr, result.Latency)
					} else {
						fmt.Printf("[%d/%d] %s %s%s - %s\n", current, total, status, pxy.pk.String()[:8], verStr, result.Error)
					}
				} else if current%10 == 0 || current == int32(total) {
					fmt.Printf("Progress: %d/%d\n", current, total)
				}
			}(i, p)
		}

		wg.Wait()

		// Output results
		if jsonOutput {
			internal.PrintOutput(cmd.Flags(), results, "")
			return
		}

		// Print summary
		var b bytes.Buffer
		w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		if testConnectOnly {
			fmt.Fprintf(w, "PUBLIC KEY\tCOUNTRY\tVERSION\tSTATUS\tTIME\n")
		} else {
			fmt.Fprintf(w, "PUBLIC KEY\tCOUNTRY\tTRANSPORT\tSTATUS\tLATENCY\n")
		}
		successCount := 0
		for _, r := range results {
			status := "fail"
			latency := "-"
			if r.Success {
				status = "ok"
				latency = fmt.Sprintf("%.0fms", r.Latency)
				successCount++
			}
			if testConnectOnly {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.PublicKey[:16]+"...", r.Country, r.Version, status, latency)
			} else {
				tpStr := "no"
				if r.HasTransport {
					tpStr = "yes"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.PublicKey[:16]+"...", r.Country, tpStr, status, latency)
			}
		}
		w.Flush()

		fmt.Print(b.String())
		if testConnectOnly {
			fmt.Printf("\nSummary: %d/%d proxies connected\n", successCount, len(results))
		} else {
			fmt.Printf("\nSummary: %d/%d proxies working\n", successCount, len(results))
		}
	},
}

// testProxyServerWithPort tests a single proxy server by starting the client and making an HTTP request
func testProxyServerWithPort(rpcClient proxyTestClient, serverPK cipher.PubKey, testURL string, timeoutSec int, port int) (latencyMs float64, response string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()

	// Use a unique client name for this test
	testClientName := fmt.Sprintf("proxy-test-%s", serverPK.String()[:8])
	proxyAddr := fmt.Sprintf("127.0.0.1:%d", port)

	// Stop any existing test client first
	_ = rpcClient.StopApp(testClientName)
	time.Sleep(100 * time.Millisecond)

	// Configure and start the proxy client
	arguments := map[string]any{
		"app":    "skysocks-client",
		"--srv":  serverPK.String(),
		"--addr": fmt.Sprintf(":%d", port),
	}

	// Check if app exists, if not add it
	_, err = rpcClient.App(testClientName)
	if err != nil {
		err = rpcClient.AddApp(testClientName, "skywire")
		if err != nil {
			return 0, "", fmt.Errorf("failed to add test app: %w", err)
		}
	}

	err = rpcClient.DoCustomSetting(testClientName, arguments)
	if err != nil {
		return 0, "", fmt.Errorf("failed to configure proxy: %w", err)
	}

	err = rpcClient.StartApp(testClientName)
	if err != nil {
		return 0, "", fmt.Errorf("failed to start proxy: %w", err)
	}

	// Wait for proxy to be ready
	defer func() {
		_ = rpcClient.StopApp(testClientName)
	}()

	// Wait for the proxy client to start
	ready := false
	for i := 0; i < 10; i++ {
		select {
		case <-ctx.Done():
			return 0, "", ctx.Err()
		default:
		}
		states, err := rpcClient.Apps()
		if err == nil {
			for _, state := range states {
				if state.Name == testClientName && state.Status == appserver.AppStatusRunning {
					ready = true
					break
				}
			}
		}
		if ready {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !ready {
		return 0, "", fmt.Errorf("proxy client failed to start")
	}

	// Give it a moment to establish connection
	time.Sleep(500 * time.Millisecond)

	// Make HTTP request through the SOCKS5 proxy
	start := time.Now()

	dialer, err := proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
	if err != nil {
		return 0, "", fmt.Errorf("failed to create SOCKS5 dialer: %w", err)
	}

	// Create a context-aware dial function
	dialFunc := func(ctx context.Context, network, addr string) (net.Conn, error) {
		// Use a channel to handle context cancellation
		type dialResult struct {
			conn net.Conn
			err  error
		}
		ch := make(chan dialResult, 1)
		go func() {
			conn, err := dialer.Dial(network, addr)
			ch <- dialResult{conn, err}
		}()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case result := <-ch:
			return result.conn, result.err
		}
	}

	transport := &http.Transport{
		DialContext: dialFunc,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   time.Duration(timeoutSec) * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, "GET", testURL, nil)
	if err != nil {
		return 0, "", fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	latencyMs = float64(time.Since(start).Milliseconds())

	// Read response body (limit to first 256 bytes)
	buf := make([]byte, 256)
	n, _ := resp.Body.Read(buf)
	response = strings.TrimSpace(string(buf[:n]))

	if resp.StatusCode != http.StatusOK {
		return latencyMs, response, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return latencyMs, response, nil
}
