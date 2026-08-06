// Package pv cmd/skywire-cli/commands/pv/pv.go c4-vis-cli
package pv

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"
	"github.com/tidwall/pretty"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

var (
	serviceType    = servicedisc.ServiceTypeVisor
	sdURL          string
	tpdURL         string
	cacheDirSD     string
	cacheDirTPD    string
	cacheFilesAge  int
	rawData        bool
	noFilterOnline bool
	country        string
	version        string
	isStats        bool
	showTransports bool
	showByType     bool
	minTransports  int
	testEnv        bool
)

// isTestEnv checks if test environment is enabled via SKYWIRETEST env var
func isTestEnv() bool {
	return os.Getenv("SKYWIRETEST") == "1"
}

// cacheDirPath returns a cache directory path based on the service URL host
func cacheDirPath(serviceURL string) string {
	u, err := url.Parse(serviceURL)
	if err != nil || u.Host == "" {
		return ""
	}
	return filepath.Join(os.TempDir(), u.Host)
}

// cacheFile returns the full cache file path for a given URL.
// If cacheDir is empty, returns "" (disables caching).
// Creates the cache directory if it doesn't exist.
// Generates simple, descriptive filenames (e.g., "visor.json", "uptimes.json").
func cacheFile(cacheDir, fullURL string) string {
	if cacheDir == "" {
		return ""
	}

	u, err := url.Parse(fullURL)
	if err != nil {
		return ""
	}

	// Create cache directory if it doesn't exist
	if err := os.MkdirAll(cacheDir, 0750); err != nil {
		return ""
	}

	// Extract a simple, meaningful name from the URL
	var name string

	// Check for service type in query (e.g., ?type=visor -> visor.json)
	if typeVal := u.Query().Get("type"); typeVal != "" {
		name = typeVal
	} else {
		// Use the last path segment (e.g., /all-transports -> all-transports.json)
		path := strings.TrimSuffix(u.Path, "/")
		if idx := strings.LastIndex(path, "/"); idx >= 0 {
			name = path[idx+1:]
		} else {
			name = strings.TrimPrefix(path, "/")
		}
	}

	if name == "" {
		name = "cache"
	}

	return filepath.Join(cacheDir, name+".json")
}

// getDeployment returns the appropriate deployment config based on test env
func getDeployment() deployment.Services {
	if isTestEnv() {
		return deployment.Test
	}
	return deployment.Prod
}

func init() {
	dep := getDeployment()
	defaultTestEnv := isTestEnv()

	RootCmd.Flags().BoolVar(&testEnv, "testenv", defaultTestEnv, "use test deployment")
	RootCmd.Flags().StringVarP(&sdURL, "sdurl", "a", dep.ServiceDiscovery, "service discovery url")
	RootCmd.Flags().StringVarP(&tpdURL, "tpdurl", "d", dep.TransportDiscovery, "transport discovery url (also serves the integrated uptime tracker)")
	RootCmd.Flags().BoolVarP(&rawData, "raw", "r", false, "print raw json data")
	RootCmd.Flags().BoolVarP(&noFilterOnline, "noton", "o", false, "do not filter by online status")
	RootCmd.Flags().StringVar(&cacheDirSD, "cds", cacheDirPath(dep.ServiceDiscovery), "SD cache dir (\"\" to disable)")
	RootCmd.Flags().StringVar(&cacheDirTPD, "cdt", cacheDirPath(dep.TransportDiscovery), "TPD cache dir, shared by transports + uptime (\"\" to disable)")
	RootCmd.Flags().IntVarP(&cacheFilesAge, "cfa", "m", 5, "refetch cached data older than n minutes (0 = always fetch fresh, e.g. for `watch`)")
	RootCmd.Flags().StringVarP(&country, "country", "c", "", "filter by country code")
	RootCmd.Flags().StringVarP(&version, "version", "v", "", "filter by version")
	RootCmd.Flags().BoolVarP(&isStats, "stats", "s", false, "return only a count of the results")
	RootCmd.Flags().BoolVarP(&showTransports, "transports", "t", false, "show transport count per visor")
	RootCmd.Flags().BoolVarP(&showByType, "bytype", "y", false, "break the transport count down by type (implies -t): total + per-type columns")
	RootCmd.Flags().IntVarP(&minTransports, "min", "n", 0, "minimum transport count (requires -t)")
	RootCmd.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr, "RPC server address (env: SKYWIRE_RPC)")
	clirpc.RegisterFetchFlags(RootCmd)
}

// RootCmd is the command for listing public visors
var RootCmd = &cobra.Command{
	Use:   "pv",
	Short: "Public Visors",
	Long: fmt.Sprintf(`List public visors from service discovery
%v/api/services?type=%v

Returns only public keys, one per line.
Use -t to show transport counts per visor.

Cache files are stored in directories named after service hosts.
Set cache dir to "" to disable caching for that service.

Use --testenv or SKYWIRETEST=1 to use test deployment services.`, getDeployment().ServiceDiscovery, serviceType),
	Run: func(cmd *cobra.Command, _ []string) {
		// --bytype is a richer form of --transports; it needs the same TPD fetch.
		if showByType {
			showTransports = true
		}
		// Handle --testenv flag: override URLs and cache dirs that weren't explicitly set
		if testEnv && !isTestEnv() {
			// --testenv was specified at runtime (not via SKYWIRETEST env)
			// Override values that weren't explicitly changed by user
			if !cmd.Flags().Changed("sdurl") {
				sdURL = deployment.Test.ServiceDiscovery
			}
			if !cmd.Flags().Changed("tpdurl") {
				tpdURL = deployment.Test.TransportDiscovery
			}
			if !cmd.Flags().Changed("cds") {
				cacheDirSD = cacheDirPath(deployment.Test.ServiceDiscovery)
			}
			if !cmd.Flags().Changed("cdt") {
				cacheDirTPD = cacheDirPath(deployment.Test.TransportDiscovery)
			}
		}

		// Build full URLs with endpoints. Uptime and transports both come
		// from the transport-discovery base: the uptime tracker is
		// TPD-integrated (CXO-backed v3), so there is no separate uptime
		// endpoint or cache — never the deprecated standalone tracker.
		sdFullURL := sdURL + "/api/services?type=" + serviceType
		utFullURL := clirpc.IntegratedUptimeURL(tpdURL)
		tpdFullURL := tpdURL + "/all-transports"

		// Fetch SD
		sds := clirpc.FetchCachedServiceURL(cmd.Flags(), cacheFile(cacheDirSD, sdFullURL), sdFullURL, cacheFilesAge)
		if rawData {
			script.Echo(string(pretty.Color(pretty.Pretty([]byte(sds)), nil))).Stdout() //nolint:errcheck,gosec
			return
		}

		// Get list of PKs first (we'll need this for transport counting)
		var pks []string

		// No filtering by online status - just get PKs from SD
		if noFilterOnline {
			sdJQ := `.[]`
			if country != "" {
				sdJQ += ` | select(.geo.country == "` + country + `")`
			}
			if version != "" {
				sdJQ += ` | select(.version | startswith("` + version + `"))`
			}
			sdJQ += ` | .address | split(":")[0]`

			if !showTransports {
				if isStats {
					count, err := script.Echo(sds).JQ(sdJQ).Replace(`"`, "").CountLines()
					if err != nil {
						internal.PrintFatalError(cmd.Flags(), fmt.Errorf("error: %w", err))
					}
					internal.PrintOutput(cmd.Flags(), struct {
						Count int `json:"count"`
					}{Count: count}, fmt.Sprintf("%v\n", count))
					return
				}
				script.Echo(sds).JQ(sdJQ).Replace(`"`, "").Stdout() //nolint:errcheck,gosec
				return
			}

			// Get PKs for transport counting
			pks, _ = script.Echo(sds).JQ(sdJQ).Replace(`"`, "").Slice() //nolint:errcheck
		} else {
			// Filter by online status via jq join
			uts := clirpc.FetchCachedServiceURL(cmd.Flags(), cacheFile(cacheDirTPD, utFullURL), utFullURL, cacheFilesAge)
			if uts == "" {
				uts = "[]"
			}
			joinedJSON := fmt.Sprintf(`{"sd": %s, "ut": %s}`, sds, uts)

			// Build jq filter with optional country and version conditions
			countryCond := ""
			if country != "" {
				countryCond = ` | select(.geo.country == "` + country + `")`
			}
			versionCond := ""
			if version != "" {
				versionCond = ` | select(.version | startswith("` + version + `"))`
			}

			jqFilter := `
			[ .ut[] | select(.on) | .pk ] as $online
			| .sd[]
			| select((.address | split(":")[0]) as $pk | $pk | IN($online[]))` + countryCond + versionCond + `
			| .address | split(":")[0]
			`

			if !showTransports {
				if isStats {
					count, err := script.Echo(joinedJSON).JQ(jqFilter).Replace(`"`, "").CountLines()
					if err != nil {
						internal.PrintFatalError(cmd.Flags(), fmt.Errorf("error: %w", err))
					}
					internal.PrintOutput(cmd.Flags(), struct {
						Count int `json:"count"`
					}{Count: count}, fmt.Sprintf("%v\n", count))
					return
				}
				script.Echo(joinedJSON).JQ(jqFilter).Replace(`"`, "").Stdout() //nolint:errcheck,gosec
				return
			}

			// Get PKs for transport counting
			pks, _ = script.Echo(joinedJSON).JQ(jqFilter).Replace(`"`, "").Slice() //nolint:errcheck
		}

		// Show transports mode - fetch TPD and count transports per visor
		tpd := clirpc.FetchCachedServiceURL(cmd.Flags(), cacheFile(cacheDirTPD, tpdFullURL), tpdFullURL, cacheFilesAge)

		var entries []*transport.Entry
		if err := json.Unmarshal([]byte(tpd), &entries); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to parse transport discovery data: %w", err))
		}

		// Count transports per PK (and per type when --bytype is set).
		tpCount := make(map[string]int)
		tpByType := make(map[string]map[types.Type]int)
		for _, entry := range entries {
			// Skip self-transports
			if entry.Edges[0] == entry.Edges[1] {
				continue
			}
			for _, edge := range entry.Edges {
				pk := edge.String()
				tpCount[pk]++
				if showByType {
					if tpByType[pk] == nil {
						tpByType[pk] = make(map[types.Type]int)
					}
					tpByType[pk][entry.Type]++
				}
			}
		}

		// Build result with transport counts
		type visorInfo struct {
			pk    string
			count int
		}
		var results []visorInfo
		for _, pk := range pks {
			count := tpCount[pk]
			if count >= minTransports {
				results = append(results, visorInfo{pk: pk, count: count})
			}
		}

		// Sort by transport count descending
		sort.Slice(results, func(i, j int) bool {
			return results[i].count > results[j].count
		})

		if isStats {
			internal.PrintOutput(cmd.Flags(), struct {
				Count int `json:"count"`
			}{Count: len(results)}, fmt.Sprintf("%d\n", len(results)))
			return
		}

		if showByType {
			// Stable columns in preference order (stcpr squicr sudph stcp
			// webrtc swsr swtr dmsg), each printed even when zero, so the
			// output is a fixed-width table friendly to awk/graphing.
			order := types.PreferenceOrder()
			for _, r := range results {
				line := fmt.Sprintf("%s total=%d", r.pk, r.count)
				for _, t := range order {
					line += fmt.Sprintf(" %s=%d", t, tpByType[r.pk][t])
				}
				fmt.Println(line)
			}
			return
		}

		for _, r := range results {
			fmt.Printf("%s %d\n", r.pk, r.count)
		}
	},
}
