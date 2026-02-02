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
	"sync/atomic"
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

// isRPCConnectionError returns true if the error indicates a dead RPC connection
func isRPCConnectionError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "connection is shut down") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "EOF")
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
	startCmd.Flags().BoolVar(&existingTpOnly, "existing-tp", false, "only use existing transports, don't create new ones")
	startCmd.Flags().BoolVar(&forceLocalRoutes, "local-route", false, "calculate routes locally instead of using route finder")
	stopCmd.Flags().BoolVar(&allClients, "all", false, "stop all skysocks client")
	stopCmd.Flags().StringVar(&clientName, "name", "", "specific skysocks client that want stop")
	// test command flags
	testCmd.Flags().StringVarP(&testURL, "url", "u", "https://ip.skycoin.com", "URL to fetch through proxy for testing")
	testCmd.Flags().IntVarP(&testTimeout, "timeout", "t", 10, "timeout in seconds for HTTP request (route setup has separate 30s timeout)")
	testCmd.Flags().IntVarP(&testBatchSize, "batch", "b", 1, "number of proxies to test (1=sequential/stable, >1=parallel/experimental)")
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
	testCmd.Flags().BoolVar(&forceLocalRoutes, "local-route", false, "calculate routes locally instead of using route finder")
	testCmd.Flags().BoolVar(&existingTpOnly, "existing-tp", false, "only use existing transports, don't create new ones")
	testCmd.Flags().StringVarP(&pk, "pk", "s", "", "test a specific proxy server by public key")
	testCmd.Flags().StringVar(&viaVisor, "via", "", "test 2-hop routes via specified visor (queries TPD for its transports)")
	testCmd.Flags().IntVar(&testDelay, "delay", 0, "delay in ms between dispatching tests (0=auto: 500ms for batch>1, 0 for batch=1)")
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "start the " + serviceType + " client",
	Run: func(cmd *cobra.Command, args []string) {

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close()

		// stop possible running proxy before start it again
		if clientName != "" {
			rpcClient.StopApp(clientName) //nolint:errcheck,gosec
		} else {
			rpcClient.StopApp("skysocks-client") //nolint:errcheck,gosec
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
		defer rpcClient.Close()
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
		defer rpcClient.Close()
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
	Bandwidth    float64 `json:"bandwidth_kbps,omitempty"`
	ProxyIP      string  `json:"proxy_ip,omitempty"`
	ProxyCountry string  `json:"proxy_country,omitempty"`
	ProxyCity    string  `json:"proxy_city,omitempty"`
	Response     string  `json:"response,omitempty"`
	Error        string  `json:"error,omitempty"`
	ViaVisor     string  `json:"via_visor,omitempty"`
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
		defer rpcClient.Close() // Ensure connection is closed when command finishes

		// If --via is specified, auto-enable local routes and existing-tp-only
		if viaVisor != "" {
			forceLocalRoutes = true
			existingTpOnly = true
		}

		// Set routing mode flags
		if existingTpOnly {
			if err := rpcClient.SetExistingTPOnly(true); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to set existing transport only mode: %w", err))
			}
		}
		if forceLocalRoutes {
			if err := rpcClient.SetForceLocalRoutes(true); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to set force local routes mode: %w", err))
			}
		}

		// If --via is specified, get destination PKs from that visor's transports
		var viaDestinations []cipher.PubKey
		var viaPK cipher.PubKey
		if viaVisor != "" {
			if err := viaPK.Set(viaVisor); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid via visor public key: %w", err))
			}

			// Query TPD for transports of the intermediate visor
			entries, err := rpcClient.DiscoverTransportsByPK(viaPK)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to query transports for via visor: %w", err))
			}

			if testVerbose {
				fmt.Printf("Found %d transports for via visor %s\n", len(entries), viaVisor[:16]+"...")
			}

			// Extract destination PKs (the "other" edge from each transport)
			seenPKs := make(map[string]bool)
			for _, entry := range entries {
				for _, edge := range entry.Edges {
					if edge != viaPK && !seenPKs[edge.String()] {
						seenPKs[edge.String()] = true
						viaDestinations = append(viaDestinations, edge)
					}
				}
			}

			if testVerbose {
				fmt.Printf("Found %d unique destination visors via %s\n", len(viaDestinations), viaVisor[:16]+"...")
			}
		}

		// If batch=1 and a specific pk is provided, use existing skysocks-client
		if testBatchSize == 1 && pk != "" {
			var serverPK cipher.PubKey
			if err := serverPK.Set(pk); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid public key: %w", err))
			}

			// Stop existing client
			_ = rpcClient.StopApp("skysocks-client")
			time.Sleep(200 * time.Millisecond)

			// Configure and start the existing skysocks-client
			arguments := map[string]any{
				"app":    "skysocks-client",
				"--srv":  serverPK.String(),
				"--addr": addr,
			}

			err = rpcClient.DoCustomSetting("skysocks-client", arguments)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to configure proxy: %w", err))
			}

			err = rpcClient.StartApp("skysocks-client")
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to start proxy: %w", err))
			}

			fmt.Printf("Started skysocks-client with server %s\n", serverPK.String())

			// Wait for it to be ready and test
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(testTimeout)*time.Second)
			defer cancel()

			ready := false
			for i := 0; i < 20; i++ {
				select {
				case <-ctx.Done():
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("timeout waiting for proxy to start"))
				default:
				}
				states, err := rpcClient.Apps()
				if err == nil {
					for _, state := range states {
						if state.Name == "skysocks-client" && state.Status == appserver.AppStatusRunning {
							ready = true
							break
						}
						if state.Name == "skysocks-client" && state.Status == appserver.AppStatusErrored {
							internal.PrintFatalError(cmd.Flags(), fmt.Errorf("proxy errored: %s", state.DetailedStatus))
						}
					}
				}
				if ready {
					break
				}
				time.Sleep(500 * time.Millisecond)
			}

			if !ready {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("proxy client failed to start"))
			}

			fmt.Println("Proxy is running!")

			// Test the proxy
			proxyAddr := "127.0.0.1" + addr
			if !strings.HasPrefix(addr, ":") {
				proxyAddr = addr
			}

			dialer, err := proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to create SOCKS5 dialer: %w", err))
			}

			transport := &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					return dialer.Dial(network, addr)
				},
			}
			client := &http.Client{Transport: transport, Timeout: time.Duration(testTimeout) * time.Second}

			start := time.Now()
			resp, err := client.Get(testURL)
			if err != nil {
				fmt.Printf("HTTP test failed: %v\n", err)
			} else {
				defer resp.Body.Close()
				latency := time.Since(start).Milliseconds()
				fmt.Printf("HTTP test succeeded in %dms (status: %d)\n", latency, resp.StatusCode)
			}

			return
		}

		// Collect proxies to test
		type proxyToTest struct {
			pk           cipher.PubKey
			country      string
			version      string
			hasTransport bool
			viaVisor     string
		}
		var proxiesToTest []proxyToTest

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

		// If --via is specified, use destinations from that visor's transports
		if len(viaDestinations) > 0 {
			for _, destPK := range viaDestinations {
				// Check if we have a transport to the via visor
				hasTransport := transportPKs[viaPK.String()]
				proxiesToTest = append(proxiesToTest, proxyToTest{
					pk:           destPK,
					country:      "", // Will discover via test
					version:      "",
					hasTransport: hasTransport,
					viaVisor:     viaPK.String(),
				})
			}

			if testVerbose {
				fmt.Printf("Testing %d destinations via %s\n", len(proxiesToTest), viaVisor[:16]+"...")
			}
		} else {
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
		}

		if len(proxiesToTest) == 0 {
			internal.PrintOutput(cmd.Flags(), nil, "No proxies to test (with current filters)\n")
			return
		}

		if testVerbose && len(viaDestinations) == 0 {
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

		// Progress indicator
		total := len(proxiesToTest)
		if viaVisor != "" {
			fmt.Printf("Testing %d destinations via %s (batch=%d, timeout=%ds)\n", total, viaVisor[:16]+"...", testBatchSize, testTimeout)
		} else {
			fmt.Printf("Processing %d proxies (batch=%d, timeout=%ds)...\n", total, testBatchSize, testTimeout)
		}

		results := make([]ProxyTestResult, len(proxiesToTest))

		// When batch=1, use sequential mode with existing skysocks-client to avoid polluting config
		if testBatchSize == 1 {
			proxyAddr := "127.0.0.1" + addr
			if !strings.HasPrefix(addr, ":") {
				proxyAddr = addr
			}

			for i, pxy := range proxiesToTest {
				result := ProxyTestResult{
					PublicKey:    pxy.pk.String(),
					Country:      pxy.country,
					Version:      pxy.version,
					HasTransport: pxy.hasTransport,
					ViaVisor:     pxy.viaVisor,
				}

				// Stop existing client
				stopErr := rpcClient.StopApp("skysocks-client")
				// Check for connection error and try to reconnect
				if isRPCConnectionError(stopErr) {
					fmt.Println("[RPC] Connection lost, reconnecting...")
					// Close old connection to prevent leak
					if rpcClient != nil {
						_ = rpcClient.Close()
					}
					newClient, err := clirpc.Client(cmd.Flags())
					if err != nil {
						fmt.Printf("[RPC] Reconnection failed: %v\n", err)
						result.Error = fmt.Sprintf("RPC reconnection failed: %v", err)
						results[i] = result
						fmt.Printf("[%d/%d] FAIL %s - %s\n", i+1, total, pxy.pk.String()[:8], result.Error)
						continue
					}
					rpcClient = newClient
					fmt.Println("[RPC] Reconnected successfully")
					_ = rpcClient.StopApp("skysocks-client")
				}
				time.Sleep(100 * time.Millisecond)

				// Configure and start the existing skysocks-client
				arguments := map[string]any{
					"app":    "skysocks-client",
					"--srv":  pxy.pk.String(),
					"--addr": addr,
				}

				err := rpcClient.DoCustomSetting("skysocks-client", arguments)
				if isRPCConnectionError(err) {
					// Try reconnecting once
					fmt.Println("[RPC] Connection lost during configure, reconnecting...")
					// Close old connection to prevent leak
					if rpcClient != nil {
						_ = rpcClient.Close()
					}
					newClient, reconnErr := clirpc.Client(cmd.Flags())
					if reconnErr != nil {
						result.Error = fmt.Sprintf("configure failed (reconnect failed): %v", err)
						results[i] = result
						fmt.Printf("[%d/%d] FAIL %s - %s\n", i+1, total, pxy.pk.String()[:8], result.Error)
						continue
					}
					rpcClient = newClient
					// Retry the configure
					err = rpcClient.DoCustomSetting("skysocks-client", arguments)
				}
				if err != nil {
					result.Error = fmt.Sprintf("configure failed: %v", err)
					results[i] = result
					fmt.Printf("[%d/%d] FAIL %s - %s\n", i+1, total, pxy.pk.String()[:8], result.Error)
					continue
				}

				err = rpcClient.StartApp("skysocks-client")
				if isRPCConnectionError(err) {
					// Try reconnecting once
					fmt.Println("[RPC] Connection lost during start, reconnecting...")
					// Close old connection to prevent leak
					if rpcClient != nil {
						_ = rpcClient.Close()
					}
					newClient, reconnErr := clirpc.Client(cmd.Flags())
					if reconnErr != nil {
						result.Error = fmt.Sprintf("start failed (reconnect failed): %v", err)
						results[i] = result
						fmt.Printf("[%d/%d] FAIL %s - %s\n", i+1, total, pxy.pk.String()[:8], result.Error)
						continue
					}
					rpcClient = newClient
					// Retry the start
					err = rpcClient.StartApp("skysocks-client")
				}
				if err != nil {
					result.Error = fmt.Sprintf("start failed: %v", err)
					results[i] = result
					fmt.Printf("[%d/%d] FAIL %s - %s\n", i+1, total, pxy.pk.String()[:8], result.Error)
					continue
				}

				// Wait for it to be ready - use longer timeout for route setup (30s)
				startupTimeout := 30
				ready := false
				var lastStatus string
				var lastDetailedStatus string
				for j := 0; j < startupTimeout*2; j++ {
					states, err := rpcClient.Apps()
					if isRPCConnectionError(err) {
						// Try to reconnect
						fmt.Println("[RPC] Connection lost during status check, reconnecting...")
						// Close old connection to prevent leak
						if rpcClient != nil {
							_ = rpcClient.Close()
						}
						newClient, reconnErr := clirpc.Client(cmd.Flags())
						if reconnErr == nil {
							rpcClient = newClient
							states, err = rpcClient.Apps()
						}
					}
					if err == nil {
						for _, state := range states {
							if state.Name == "skysocks-client" {
								lastStatus = string(state.Status)
								lastDetailedStatus = state.DetailedStatus
								if state.Status == appserver.AppStatusRunning {
									ready = true
									break
								}
								if state.Status == appserver.AppStatusErrored {
									result.Error = state.DetailedStatus
									if result.Error == "" {
										result.Error = "app errored (no details)"
									}
									break
								}
								if state.Status == appserver.AppStatusStopped && j > 4 {
									// App stopped after we started it - likely a connection failure
									result.Error = state.DetailedStatus
									if result.Error == "" {
										result.Error = "app stopped unexpectedly"
									}
									break
								}
							}
						}
					}
					if ready || result.Error != "" {
						break
					}
					time.Sleep(500 * time.Millisecond)
				}

				if result.Error != "" {
					results[i] = result
					fmt.Printf("[%d/%d] FAIL %s - %s\n", i+1, total, pxy.pk.String()[:8], result.Error)
					continue
				}

				if !ready {
					// Get more details about why it didn't start
					errMsg := "startup timeout"
					if lastDetailedStatus != "" {
						errMsg = lastDetailedStatus
					} else if lastStatus != "" {
						errMsg = fmt.Sprintf("stuck in status: %s", lastStatus)
					}
					result.Error = errMsg
					results[i] = result
					fmt.Printf("[%d/%d] FAIL %s - %s\n", i+1, total, pxy.pk.String()[:8], result.Error)
					continue
				}

				// Small delay to let connection establish
				time.Sleep(300 * time.Millisecond)

				// Test with HTTP request
				start := time.Now()
				dialer, err := proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
				if err != nil {
					result.Error = fmt.Sprintf("SOCKS5 dialer: %v", err)
					results[i] = result
					fmt.Printf("[%d/%d] FAIL %s - %s\n", i+1, total, pxy.pk.String()[:8], result.Error)
					continue
				}

				transport := &http.Transport{
					DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
						return dialer.Dial(network, addr)
					},
				}
				client := &http.Client{Transport: transport, Timeout: time.Duration(testTimeout) * time.Second}

				resp, err := client.Get(testURL)
				if err != nil {
					result.Error = fmt.Sprintf("HTTP: %v", err)
					results[i] = result
					fmt.Printf("[%d/%d] FAIL %s - %s\n", i+1, total, pxy.pk.String()[:8], result.Error)
					continue
				}

				result.Latency = float64(time.Since(start).Milliseconds())

				// Read response
				bodyStart := time.Now()
				body, _ := readAllWithLimit(resp.Body, 64*1024)
				resp.Body.Close()
				bodyDuration := time.Since(bodyStart)

				if bodyDuration.Milliseconds() > 0 {
					result.Bandwidth = (float64(len(body)) * 8 / 1000) / bodyDuration.Seconds()
				}

				response := strings.TrimSpace(string(body))
				result.ProxyIP, result.ProxyCountry, result.ProxyCity = extractGeoInfo(response)
				result.Success = resp.StatusCode == http.StatusOK

				results[i] = result

				// Print result
				if result.Success {
					locStr := ""
					if result.ProxyCountry != "" {
						locStr = result.ProxyCountry
						if result.ProxyCity != "" {
							locStr = result.ProxyCity + "," + result.ProxyCountry
						}
						locStr = " " + locStr
					}
					ipStr := ""
					if result.ProxyIP != "" {
						ipStr = " " + result.ProxyIP
					}
					bwStr := ""
					if result.Bandwidth > 0 {
						bwStr = fmt.Sprintf(" %.0fkbps", result.Bandwidth)
					}
					fmt.Printf("[%d/%d] OK %s - %.0fms%s%s%s\n", i+1, total, pxy.pk.String()[:8], result.Latency, locStr, ipStr, bwStr)
				} else {
					fmt.Printf("[%d/%d] FAIL %s - HTTP %d\n", i+1, total, pxy.pk.String()[:8], resp.StatusCode)
				}
			}

			// Stop the client when done
			_ = rpcClient.StopApp("skysocks-client")

		} else {
			// Parallel processing with pooled test clients (reuse N clients for batch size N)
			type testClient struct {
				name string
				port int
			}

			// Create pool of test clients
			clientPool := make(chan testClient, testBatchSize)
			var createdClients []string

			fmt.Printf("Setting up %d test clients...\n", testBatchSize)
			for i := 0; i < testBatchSize; i++ {
				clientName := fmt.Sprintf("proxy-test-%d", i)
				port := 10800 + i

				// Check if app exists, if not add it
				_, err := rpcClient.App(clientName)
				if err != nil {
					err = rpcClient.AddApp(clientName, "skywire")
					if err != nil {
						fmt.Printf("Warning: failed to add test client %s: %v\n", clientName, err)
						continue
					}
				}
				createdClients = append(createdClients, clientName)
				clientPool <- testClient{name: clientName, port: port}
			}

			if len(createdClients) == 0 {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to create any test clients"))
			}

			// Cleanup function
			defer func() {
				fmt.Printf("Cleaning up test clients...\n")
				for _, name := range createdClients {
					_ = rpcClient.StopApp(name)
				}
			}()

			var wg sync.WaitGroup
			var completed int32
			var mu sync.Mutex
			var rpcMu sync.Mutex // Serializes RPC calls to prevent overwhelming visor
			var aborted int32    // Set to 1 if we should abort due to RPC failure

			// Wrapper to allow reconnection during test
			type rpcClientWrapper struct {
				client visor.API
				mu     sync.RWMutex
			}
			rpcWrapper := &rpcClientWrapper{client: rpcClient}

			reconnectRPC := func() bool {
				rpcWrapper.mu.Lock()
				defer rpcWrapper.mu.Unlock()
				fmt.Println("[RPC] Connection lost, reconnecting...")
				// Close old connection to prevent leak
				if rpcWrapper.client != nil {
					_ = rpcWrapper.client.Close()
				}
				newClient, err := clirpc.Client(cmd.Flags())
				if err != nil {
					fmt.Printf("[RPC] Reconnection failed: %v\n", err)
					return false
				}
				rpcWrapper.client = newClient
				fmt.Println("[RPC] Reconnected successfully")
				return true
			}

			getRPCClient := func() visor.API {
				rpcWrapper.mu.RLock()
				defer rpcWrapper.mu.RUnlock()
				return rpcWrapper.client
			}

			// Throttle how fast we dispatch new tests to avoid overwhelming visor
			var ticker *time.Ticker
			if testDelay > 0 {
				ticker = time.NewTicker(time.Duration(testDelay) * time.Millisecond)
				defer ticker.Stop()
			}

			for i, p := range proxiesToTest {
				// Check if we should abort
				if atomic.LoadInt32(&aborted) == 1 {
					break
				}

				// Wait for ticker before dispatching next test (skip first one)
				if ticker != nil && i > 0 {
					<-ticker.C
				}

				wg.Add(1)
				go func(idx int, pxy proxyToTest) {
					defer wg.Done()

					// Check if aborted
					if atomic.LoadInt32(&aborted) == 1 {
						return
					}

					// Get a client from pool
					client := <-clientPool
					defer func() { clientPool <- client }()

					result := ProxyTestResult{
						PublicKey:    pxy.pk.String(),
						Country:      pxy.country,
						Version:      pxy.version,
						HasTransport: pxy.hasTransport,
						ViaVisor:     pxy.viaVisor,
					}

					if testConnectOnly {
						// Connect-only mode: just add transport
						start := time.Now()
						rpcMu.Lock()
						currentClient := getRPCClient()
						_, err := currentClient.AddTransport(pxy.pk, "stcpr", time.Duration(testTimeout)*time.Second)
						if err != nil {
							_, err = currentClient.AddTransport(pxy.pk, "sudph", time.Duration(testTimeout)*time.Second)
						}
						rpcMu.Unlock()
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
						// Test proxy via HTTP using pooled client
						testResult := testProxyWithPooledClient(getRPCClient(), client.name, client.port, pxy.pk, testURL, testTimeout, &rpcMu)

						// Handle RPC connection loss with retry
						if testResult.err == errRPCConnectionLost {
							// Reconnect (this has its own mutex)
							if reconnectRPC() {
								// Retry the test with new connection (testProxyWithPooledClient handles its own rpcMu locking)
								testResult = testProxyWithPooledClient(getRPCClient(), client.name, client.port, pxy.pk, testURL, testTimeout, &rpcMu)
							}
						}

						if testResult.err != nil {
							result.Success = false
							result.Error = testResult.err.Error()
							// Abort if still connection error after retry
							if testResult.err == errRPCConnectionLost {
								atomic.StoreInt32(&aborted, 1)
							}
						} else {
							result.Success = true
							result.Latency = testResult.latencyMs
							result.Bandwidth = testResult.bandwidthKbps
							result.ProxyIP = testResult.proxyIP
							result.ProxyCountry = testResult.proxyCountry
							result.ProxyCity = testResult.proxyCity
							result.Response = testResult.response
						}
					}

					results[idx] = result

					// Update progress
					mu.Lock()
					completed++
					current := completed
					mu.Unlock()

					// Show progress
					showProgress := testVerbose || testBatchSize <= 5 || current%10 == 0 || current == int32(total)
					if showProgress {
						verStr := ""
						if pxy.version != "" {
							verStr = " [" + pxy.version + "]"
						}
						if result.Success {
							locStr := ""
							if result.ProxyCountry != "" {
								locStr = result.ProxyCountry
								if result.ProxyCity != "" {
									locStr = result.ProxyCity + "," + result.ProxyCountry
								}
								locStr = " " + locStr
							}
							ipStr := ""
							if result.ProxyIP != "" {
								ipStr = fmt.Sprintf(" %s", result.ProxyIP)
							}
							bwStr := ""
							if result.Bandwidth > 0 {
								bwStr = fmt.Sprintf(" %.1fkbps", result.Bandwidth)
							}
							fmt.Printf("[%d/%d] OK %s%s - %.0fms%s%s%s\n", current, total, pxy.pk.String()[:8], verStr, result.Latency, locStr, ipStr, bwStr)
						} else {
							fmt.Printf("[%d/%d] FAIL %s%s - %s\n", current, total, pxy.pk.String()[:8], verStr, result.Error)
						}
					}
				}(i, p)
			}

			wg.Wait()

			if atomic.LoadInt32(&aborted) == 1 {
				fmt.Printf("\nTest aborted: RPC connection to visor was lost\n")
				fmt.Printf("Showing partial results (%d completed)...\n", atomic.LoadInt32(&completed))
			}
		}

		// Output results
		if jsonOutput {
			internal.PrintOutput(cmd.Flags(), results, "")
			return
		}

		// Print summary table
		var b bytes.Buffer
		w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		if testConnectOnly {
			fmt.Fprintf(w, "PUBLIC KEY\tCOUNTRY\tVERSION\tSTATUS\tTIME\n")
		} else if viaVisor != "" {
			fmt.Fprintf(w, "PUBLIC KEY\tVIA\tSTATUS\tLATENCY\tBW\tLOCATION\tPROXY IP\n")
		} else {
			fmt.Fprintf(w, "PUBLIC KEY\tCOUNTRY\tSTATUS\tLATENCY\tBW\tLOCATION\tPROXY IP\n")
		}
		successCount := 0
		for _, r := range results {
			latency := "-"
			bandwidth := "-"
			proxyIP := "-"
			proxyLoc := "-"
			statusStr := "fail"
			if r.Success {
				statusStr = "ok"
				latency = fmt.Sprintf("%.0fms", r.Latency)
				if r.Bandwidth > 0 {
					bandwidth = fmt.Sprintf("%.0fkbps", r.Bandwidth)
				}
				if r.ProxyIP != "" {
					proxyIP = r.ProxyIP
				}
				if r.ProxyCountry != "" {
					proxyLoc = r.ProxyCountry
					if r.ProxyCity != "" {
						proxyLoc = r.ProxyCity + "," + r.ProxyCountry
					}
				}
				successCount++
			}
			if testConnectOnly {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.PublicKey[:16]+"...", r.Country, r.Version, statusStr, latency)
			} else if viaVisor != "" {
				via := "-"
				if r.ViaVisor != "" {
					via = r.ViaVisor[:8] + "..."
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.PublicKey[:16]+"...", via, statusStr, latency, bandwidth, proxyLoc, proxyIP)
			} else {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.PublicKey[:16]+"...", r.Country, statusStr, latency, bandwidth, proxyLoc, proxyIP)
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

// proxyTestMetrics holds the results of a proxy test
type proxyTestMetrics struct {
	latencyMs     float64
	bandwidthKbps float64
	proxyIP       string
	proxyCountry  string
	proxyCity     string
	response      string
	err           error
}

// testProxyServerWithPort tests a single proxy server by starting the client and making an HTTP request
func testProxyServerWithPort(rpcClient proxyTestClient, serverPK cipher.PubKey, testURL string, timeoutSec int, port int) proxyTestMetrics {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()

	result := proxyTestMetrics{}

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
	_, err := rpcClient.App(testClientName)
	if err != nil {
		err = rpcClient.AddApp(testClientName, "skywire")
		if err != nil {
			result.err = fmt.Errorf("failed to add test app: %w", err)
			return result
		}
	}

	err = rpcClient.DoCustomSetting(testClientName, arguments)
	if err != nil {
		result.err = fmt.Errorf("failed to configure proxy: %w", err)
		return result
	}

	err = rpcClient.StartApp(testClientName)
	if err != nil {
		result.err = fmt.Errorf("failed to start proxy: %w", err)
		return result
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
			result.err = ctx.Err()
			return result
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
		result.err = fmt.Errorf("proxy client failed to start")
		return result
	}

	// Give it a moment to establish connection
	time.Sleep(500 * time.Millisecond)

	// Make HTTP request through the SOCKS5 proxy
	start := time.Now()

	dialer, err := proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
	if err != nil {
		result.err = fmt.Errorf("failed to create SOCKS5 dialer: %w", err)
		return result
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
		result.err = fmt.Errorf("failed to create request: %w", err)
		return result
	}

	resp, err := client.Do(req)
	if err != nil {
		result.err = fmt.Errorf("request failed: %w", err)
		return result
	}
	defer resp.Body.Close()

	result.latencyMs = float64(time.Since(start).Milliseconds())

	// Read full response body to measure bandwidth
	bodyStart := time.Now()
	body, err := readAllWithLimit(resp.Body, 64*1024) // Limit to 64KB
	bodyDuration := time.Since(bodyStart)

	if err != nil {
		result.err = fmt.Errorf("failed to read response: %w", err)
		return result
	}

	// Calculate bandwidth in kbps (kilobits per second)
	if bodyDuration.Milliseconds() > 0 {
		bytesReceived := float64(len(body))
		durationSec := bodyDuration.Seconds()
		result.bandwidthKbps = (bytesReceived * 8 / 1000) / durationSec
	}

	result.response = strings.TrimSpace(string(body))

	// Try to extract IP and geo info from response
	result.proxyIP, result.proxyCountry, result.proxyCity = extractGeoInfo(result.response)

	if resp.StatusCode != http.StatusOK {
		result.err = fmt.Errorf("HTTP %d", resp.StatusCode)
		return result
	}

	return result
}

// readAllWithLimit reads up to maxBytes from the reader
func readAllWithLimit(r interface{ Read([]byte) (int, error) }, maxBytes int) ([]byte, error) {
	buf := make([]byte, maxBytes)
	totalRead := 0
	for totalRead < maxBytes {
		n, err := r.Read(buf[totalRead:])
		totalRead += n
		if err != nil {
			break // EOF or error
		}
	}
	return buf[:totalRead], nil
}

// extractGeoInfo tries to extract IP address and geolocation from the response
func extractGeoInfo(response string) (ip, country, city string) {
	response = strings.TrimSpace(response)

	// Check if it's a plain IP address (v4 or v6)
	if parsedIP := net.ParseIP(response); parsedIP != nil {
		return response, "", ""
	}

	// Try to extract from JSON - support various formats
	// ip.skycoin.com format: {"ip_address":"...", "country_code":"...", "city_name":"..."}
	// httpbin format: {"origin":"..."}
	// ipinfo.io format: {"ip":"...", "country":"...", "city":"..."}
	type geoResponse struct {
		// ip.skycoin.com format
		IPAddress   string `json:"ip_address"`
		CountryCode string `json:"country_code"`
		CountryName string `json:"country_name"`
		CityName    string `json:"city_name"`
		// ipinfo.io / generic format
		IP      string `json:"ip"`
		Country string `json:"country"`
		City    string `json:"city"`
		// httpbin format
		Origin string `json:"origin"`
	}

	var geoResp geoResponse
	if err := json.Unmarshal([]byte(response), &geoResp); err == nil {
		// Extract IP
		if geoResp.IPAddress != "" {
			ip = geoResp.IPAddress
		} else if geoResp.IP != "" {
			ip = geoResp.IP
		} else if geoResp.Origin != "" {
			// Origin might have multiple IPs separated by comma
			parts := strings.Split(geoResp.Origin, ",")
			ip = strings.TrimSpace(parts[0])
		}

		// Extract country (prefer code for brevity)
		if geoResp.CountryCode != "" {
			country = geoResp.CountryCode
		} else if geoResp.Country != "" {
			country = geoResp.Country
		}

		// Extract city
		if geoResp.CityName != "" {
			city = geoResp.CityName
		} else if geoResp.City != "" {
			city = geoResp.City
		}
	}

	return ip, country, city
}

// errRPCConnectionLost is a sentinel error indicating the RPC connection died
var errRPCConnectionLost = fmt.Errorf("RPC connection lost")

// testProxyWithPooledClient tests a proxy using a pre-created pooled client (avoids creating new apps)
// rpcMu serializes all RPC calls to prevent overwhelming the visor
// Returns errRPCConnectionLost if the RPC connection died (caller should reconnect)
func testProxyWithPooledClient(rpcClient proxyTestClient, clientName string, port int, serverPK cipher.PubKey, testURL string, timeoutSec int, rpcMu *sync.Mutex) proxyTestMetrics {
	result := proxyTestMetrics{}
	proxyAddr := fmt.Sprintf("127.0.0.1:%d", port)

	// Stop client if running (with RPC mutex)
	rpcMu.Lock()
	stopErr := rpcClient.StopApp(clientName)
	rpcMu.Unlock()
	if isRPCConnectionError(stopErr) {
		result.err = errRPCConnectionLost
		return result
	}
	time.Sleep(100 * time.Millisecond)

	// Configure for the new target
	arguments := map[string]any{
		"app":    "skysocks-client",
		"--srv":  serverPK.String(),
		"--addr": fmt.Sprintf(":%d", port),
	}

	rpcMu.Lock()
	err := rpcClient.DoCustomSetting(clientName, arguments)
	rpcMu.Unlock()
	if isRPCConnectionError(err) {
		result.err = errRPCConnectionLost
		return result
	}
	if err != nil {
		result.err = fmt.Errorf("configure failed: %w", err)
		return result
	}

	rpcMu.Lock()
	err = rpcClient.StartApp(clientName)
	rpcMu.Unlock()
	if isRPCConnectionError(err) {
		result.err = errRPCConnectionLost
		return result
	}
	if err != nil {
		result.err = fmt.Errorf("start failed: %w", err)
		return result
	}

	// Wait for it to be ready (30s for route setup)
	startupTimeout := 30
	ready := false
	var lastDetailedStatus string
	for i := 0; i < startupTimeout*2; i++ {
		rpcMu.Lock()
		states, err := rpcClient.Apps()
		rpcMu.Unlock()
		if isRPCConnectionError(err) {
			result.err = errRPCConnectionLost
			return result
		}
		if err == nil {
			for _, state := range states {
				if state.Name == clientName {
					lastDetailedStatus = state.DetailedStatus
					if state.Status == appserver.AppStatusRunning {
						ready = true
						break
					}
					if state.Status == appserver.AppStatusErrored {
						result.err = fmt.Errorf("%s", state.DetailedStatus)
						if result.err.Error() == "" {
							result.err = fmt.Errorf("app errored")
						}
						return result
					}
					if state.Status == appserver.AppStatusStopped && i > 4 {
						result.err = fmt.Errorf("%s", state.DetailedStatus)
						if result.err.Error() == "" {
							result.err = fmt.Errorf("app stopped")
						}
						return result
					}
				}
			}
		}
		if ready {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !ready {
		if lastDetailedStatus != "" {
			result.err = fmt.Errorf("%s", lastDetailedStatus)
		} else {
			result.err = fmt.Errorf("startup timeout")
		}
		return result
	}

	// Small delay for connection
	time.Sleep(200 * time.Millisecond)

	// HTTP request (with separate timeout) - no mutex needed, this is network I/O
	start := time.Now()
	dialer, err := proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
	if err != nil {
		result.err = fmt.Errorf("SOCKS5: %w", err)
		return result
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		},
	}
	client := &http.Client{Transport: transport, Timeout: time.Duration(timeoutSec) * time.Second}

	resp, err := client.Get(testURL)
	if err != nil {
		result.err = fmt.Errorf("request failed: %w", err)
		return result
	}
	defer resp.Body.Close()

	result.latencyMs = float64(time.Since(start).Milliseconds())

	// Read response
	bodyStart := time.Now()
	body, _ := readAllWithLimit(resp.Body, 64*1024)
	bodyDuration := time.Since(bodyStart)

	if bodyDuration.Milliseconds() > 0 {
		result.bandwidthKbps = (float64(len(body)) * 8 / 1000) / bodyDuration.Seconds()
	}

	result.response = strings.TrimSpace(string(body))
	result.proxyIP, result.proxyCountry, result.proxyCity = extractGeoInfo(result.response)

	if resp.StatusCode != http.StatusOK {
		result.err = fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return result
}
