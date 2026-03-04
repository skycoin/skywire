// Package pv cmd/skywire-cli/commands/pv/pv.go
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

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/transport"
)

var (
	serviceType    = servicedisc.ServiceTypeVisor
	sdURL          string
	utURL          string
	tpdURL         string
	cacheDirSD     string
	cacheDirUT     string
	cacheDirTPD    string
	cacheFilesAge  int
	rawData        bool
	noFilterOnline bool
	country        string
	version        string
	isStats        bool
	showTransports bool
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
	RootCmd.Flags().StringVarP(&utURL, "uturl", "w", dep.UptimeTracker, "uptime tracker url")
	RootCmd.Flags().StringVarP(&tpdURL, "tpdurl", "d", dep.TransportDiscovery, "transport discovery url")
	RootCmd.Flags().BoolVarP(&rawData, "raw", "r", false, "print raw json data")
	RootCmd.Flags().BoolVarP(&noFilterOnline, "noton", "o", false, "do not filter by online status in UT")
	RootCmd.Flags().StringVar(&cacheDirSD, "cds", cacheDirPath(dep.ServiceDiscovery), "SD cache dir (\"\" to disable)")
	RootCmd.Flags().StringVar(&cacheDirUT, "cdu", cacheDirPath(dep.UptimeTracker), "UT cache dir (\"\" to disable)")
	RootCmd.Flags().StringVar(&cacheDirTPD, "cdt", cacheDirPath(dep.TransportDiscovery), "TPD cache dir (\"\" to disable)")
	RootCmd.Flags().IntVarP(&cacheFilesAge, "cfa", "m", 5, "update cache files if older than n minutes")
	RootCmd.Flags().StringVarP(&country, "country", "c", "", "filter by country code")
	RootCmd.Flags().StringVarP(&version, "version", "v", "", "filter by version")
	RootCmd.Flags().BoolVarP(&isStats, "stats", "s", false, "return only a count of the results")
	RootCmd.Flags().BoolVarP(&showTransports, "transports", "t", false, "show transport count per visor")
	RootCmd.Flags().IntVarP(&minTransports, "min", "n", 0, "minimum transport count (requires -t)")
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
		// Handle --testenv flag: override URLs and cache dirs that weren't explicitly set
		if testEnv && !isTestEnv() {
			// --testenv was specified at runtime (not via SKYWIRETEST env)
			// Override values that weren't explicitly changed by user
			if !cmd.Flags().Changed("sdurl") {
				sdURL = deployment.Test.ServiceDiscovery
			}
			if !cmd.Flags().Changed("uturl") {
				utURL = deployment.Test.UptimeTracker
			}
			if !cmd.Flags().Changed("tpdurl") {
				tpdURL = deployment.Test.TransportDiscovery
			}
			if !cmd.Flags().Changed("cds") {
				cacheDirSD = cacheDirPath(deployment.Test.ServiceDiscovery)
			}
			if !cmd.Flags().Changed("cdu") {
				cacheDirUT = cacheDirPath(deployment.Test.UptimeTracker)
			}
			if !cmd.Flags().Changed("cdt") {
				cacheDirTPD = cacheDirPath(deployment.Test.TransportDiscovery)
			}
		}

		// Build full URLs with endpoints
		sdFullURL := sdURL + "/api/services?type=" + serviceType
		utFullURL := utURL + "/uptimes?v=v2"
		tpdFullURL := tpdURL + "/all-transports"

		// Fetch SD
		sds := internal.GetData(cacheFile(cacheDirSD, sdFullURL), sdFullURL, cacheFilesAge)
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
					script.Echo(fmt.Sprintf("%v\n", count)).Stdout() //nolint:errcheck,gosec
					return
				}
				script.Echo(sds).JQ(sdJQ).Replace(`"`, "").Stdout() //nolint:errcheck,gosec
				return
			}

			// Get PKs for transport counting
			pks, _ = script.Echo(sds).JQ(sdJQ).Replace(`"`, "").Slice() //nolint:errcheck
		} else {
			// Filter by online status via jq join
			uts := internal.GetData(cacheFile(cacheDirUT, utFullURL), utFullURL, cacheFilesAge)
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
					script.Echo(fmt.Sprintf("%v\n", count)).Stdout() //nolint:errcheck,gosec
					return
				}
				script.Echo(joinedJSON).JQ(jqFilter).Replace(`"`, "").Stdout() //nolint:errcheck,gosec
				return
			}

			// Get PKs for transport counting
			pks, _ = script.Echo(joinedJSON).JQ(jqFilter).Replace(`"`, "").Slice() //nolint:errcheck
		}

		// Show transports mode - fetch TPD and count transports per visor
		tpd := internal.GetData(cacheFile(cacheDirTPD, tpdFullURL), tpdFullURL, cacheFilesAge)

		var entries []*transport.Entry
		if err := json.Unmarshal([]byte(tpd), &entries); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to parse transport discovery data: %w", err))
		}

		// Count transports per PK
		tpCount := make(map[string]int)
		for _, entry := range entries {
			// Skip self-transports
			if entry.Edges[0] == entry.Edges[1] {
				continue
			}
			tpCount[entry.Edges[0].String()]++
			tpCount[entry.Edges[1].String()]++
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
			fmt.Printf("%d\n", len(results))
			return
		}

		for _, r := range results {
			fmt.Printf("%s %d\n", r.pk, r.count)
		}
	},
}
