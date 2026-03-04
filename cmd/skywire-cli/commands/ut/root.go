// Package cliut cmd/skywire-cli/ut/root.go
package cliut

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/transport"
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

// RootCmd is utCmd
var RootCmd = utCmd

var (
	pk            string
	online        bool
	isStats       bool
	isMoreStats   bool
	utURL         string
	tpdURL        string
	cacheDirUT    string
	cacheDirTPD   string
	cacheFilesAge int
	versionFilter string
	minVersion    string
	listVersions  bool
	maxTP         int
	testEnv       bool
)

var minUT int

func init() {
	dep := getDeployment()
	defaultTestEnv := isTestEnv()

	utCmd.Flags().BoolVar(&testEnv, "testenv", defaultTestEnv, "use test deployment")
	utCmd.Flags().StringVarP(&pk, "pk", "k", "", "check uptime for the specified key")
	utCmd.Flags().BoolVarP(&online, "on", "o", false, "list currently online visors")
	utCmd.Flags().BoolVarP(&isStats, "stats", "s", false, "count the number of results")
	utCmd.Flags().BoolVarP(&isMoreStats, "stats2", "t", false, "count of versions")
	utCmd.Flags().IntVarP(&minUT, "min", "n", 75, "list visors meeting minimum uptime percentage\n\r")
	utCmd.Flags().StringVar(&cacheDirUT, "cdu", cacheDirPath(dep.UptimeTracker), "UT cache dir (\"\" to disable)\n\r")
	utCmd.Flags().StringVar(&cacheDirTPD, "cdt", cacheDirPath(dep.TransportDiscovery), "TPD cache dir (\"\" to disable)\n\r")
	utCmd.Flags().IntVarP(&cacheFilesAge, "cfa", "m", 5, "update cache files if older than n minutes\n\r")
	utCmd.Flags().StringVarP(&utURL, "url", "u", dep.UptimeTracker, "specify alternative uptime tracker url\n\r")
	utCmd.Flags().StringVar(&tpdURL, "tpdurl", dep.TransportDiscovery, "transport discovery url")
	utCmd.Flags().StringVarP(&versionFilter, "version", "v", "", "filter visors by exact version")
	utCmd.Flags().StringVar(&minVersion, "min-version", "", "filter visors with version >= specified (e.g. v1.3.34)")
	utCmd.Flags().BoolVarP(&listVersions, "list-versions", "l", false, "list PKs with their versions")
	utCmd.Flags().IntVar(&maxTP, "max-tp", -1, "filter visors with at most N transports (fetches TPD data)")
}

var utCmd = &cobra.Command{
	Use:   "ut",
	Short: "query uptime tracker",
	Long:  fmt.Sprintf("query uptime tracker\n\n%v/uptimes?v=v2\n\nCheck local visor daily uptime percent with:\n\n$ skywire-cli ut -n0 -k $(skywire-cli visor pk)\n\nSet cache dir to \"\" to avoid using cache files\n\nUse --testenv or SKYWIRETEST=1 to use test deployment services.", getDeployment().UptimeTracker),
	Run: func(cmd *cobra.Command, _ []string) {
		// Handle --testenv flag: override URLs and cache dirs that weren't explicitly set
		if testEnv && !isTestEnv() {
			if !cmd.Flags().Changed("url") {
				utURL = deployment.Test.UptimeTracker
			}
			if !cmd.Flags().Changed("tpdurl") {
				tpdURL = deployment.Test.TransportDiscovery
			}
			if !cmd.Flags().Changed("cdu") {
				cacheDirUT = cacheDirPath(deployment.Test.UptimeTracker)
			}
			if !cmd.Flags().Changed("cdt") {
				cacheDirTPD = cacheDirPath(deployment.Test.TransportDiscovery)
			}
		}

		// Build full URLs
		utFullURL := utURL + "/uptimes?v=v2"
		tpdFullURL := tpdURL + "/all-transports"

		uts := internal.GetData(cacheFile(cacheDirUT, utFullURL), utFullURL, cacheFilesAge)

		// Build transport count map if --max-tp is specified
		var tpCount map[string]int
		if maxTP >= 0 {
			tpd := internal.GetData(cacheFile(cacheDirTPD, tpdFullURL), tpdFullURL, cacheFilesAge)
			var entries []*transport.Entry
			if err := json.Unmarshal([]byte(tpd), &entries); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to parse TPD data: %w", err))
			}
			tpCount = make(map[string]int)
			for _, entry := range entries {
				if entry.Edges[0] == entry.Edges[1] {
					continue
				}
				tpCount[entry.Edges[0].String()]++
				tpCount[entry.Edges[1].String()]++
			}
		}

		// Build the base selector based on online flag
		baseSelector := ".[]"
		if online {
			baseSelector = ".[] | select(.on)"
		}

		// Helper to filter keys by transport count
		filterByTP := func(keys []string) []string {
			if maxTP < 0 {
				return keys
			}
			var filtered []string
			for _, k := range keys {
				if tpCount[k] <= maxTP {
					filtered = append(filtered, k)
				}
			}
			return filtered
		}

		// Helper to parse version string like "v1.3.34" or "v1.3.34 dirty"
		parseVersion := func(v string) (major, minor, patch int, ok bool) {
			if v == "" {
				return 0, 0, 0, false
			}
			// Remove "v" prefix and anything after space
			v = strings.TrimPrefix(v, "v")
			if idx := strings.Index(v, " "); idx != -1 {
				v = v[:idx]
			}
			parts := strings.Split(v, ".")
			if len(parts) < 3 {
				return 0, 0, 0, false
			}
			var err error
			major, err = strconv.Atoi(parts[0])
			if err != nil {
				return 0, 0, 0, false
			}
			minor, err = strconv.Atoi(parts[1])
			if err != nil {
				return 0, 0, 0, false
			}
			patch, err = strconv.Atoi(parts[2])
			if err != nil {
				return 0, 0, 0, false
			}
			return major, minor, patch, true
		}

		// Helper to check if version a >= version b
		versionGTE := func(a, b string) bool {
			aMaj, aMin, aPatch, aOK := parseVersion(a)
			bMaj, bMin, bPatch, bOK := parseVersion(b)
			if !aOK || !bOK {
				return false
			}
			if aMaj != bMaj {
				return aMaj > bMaj
			}
			if aMin != bMin {
				return aMin > bMin
			}
			return aPatch >= bPatch
		}

		// Handle min-version filter
		if minVersion != "" {
			// Parse UT data to filter by version
			type utEntry struct {
				PK      string `json:"pk"`
				On      bool   `json:"on"`
				Version string `json:"version"`
			}
			var utEntries []utEntry
			if err := json.Unmarshal([]byte(uts), &utEntries); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to parse UT data: %w", err))
			}

			var keys []string
			for _, e := range utEntries {
				if online && !e.On {
					continue
				}
				if !versionGTE(e.Version, minVersion) {
					continue
				}
				if pk != "" && !strings.Contains(e.PK, pk) {
					continue
				}
				keys = append(keys, e.PK)
			}
			keys = filterByTP(keys)

			if isStats {
				label := "visors"
				if online {
					label = "online visors"
				}
				internal.PrintOutput(cmd.Flags(), fmt.Sprintf("%d %s with version >= %s\n", len(keys), label, minVersion), fmt.Sprintf("%d %s with version >= %s\n", len(keys), label, minVersion))
				return
			}
			for _, k := range keys {
				internal.PrintOutput(cmd.Flags(), k+"\n", k+"\n")
			}
			return
		}

		// Handle exact version filter
		if versionFilter != "" {
			versionSelector := baseSelector + " | select(.version == \"" + versionFilter + "\")"
			keys, _ := script.Echo(uts).JQ(versionSelector+" | .pk").Match(pk).Replace("\"", "").Slice() //nolint:errcheck
			keys = filterByTP(keys)
			if isStats {
				label := "visors"
				if online {
					label = "online visors"
				}
				internal.PrintOutput(cmd.Flags(), fmt.Sprintf("%d %s with version %s\n", len(keys), label, versionFilter), fmt.Sprintf("%d %s with version %s\n", len(keys), label, versionFilter))
				return
			}
			if listVersions {
				for _, k := range keys {
					fmt.Printf("%s %s\n", k, versionFilter)
				}
				return
			}
			for _, i := range keys {
				internal.PrintOutput(cmd.Flags(), i+"\n", i+"\n")
			}
			return
		}

		// Handle list-versions flag (without version filter)
		if listVersions {
			if isStats {
				script.Echo(uts).JQ(baseSelector+" | .version").Freq().Replace("\"", "").Stdout() //nolint:errcheck,gosec
				return
			}
			script.Echo(uts).JQ(baseSelector+" | \"\\(.pk) \\(.version)\"").Match(pk).Replace("\"", "").Stdout() //nolint:errcheck,gosec
			return
		}

		if online {
			utKeysOnline, _ := script.Echo(uts).JQ(".[] | select(.on) | .pk").Match(pk).Replace("\"", "").Slice() //nolint:errcheck
			utKeysOnline = filterByTP(utKeysOnline)
			if isStats {
				internal.PrintOutput(cmd.Flags(), fmt.Sprintf("%d visors online\n", len(utKeysOnline)), fmt.Sprintf("%d visors online\n", len(utKeysOnline)))
				return
			}
			if isMoreStats {
				script.Echo(uts).JQ(".[] | select(.on) | .version").Freq().Replace("\"", "").Stdout() //nolint:errcheck,gosec
				return
			}
			for _, i := range utKeysOnline {
				internal.PrintOutput(cmd.Flags(), i+"\n", i+"\n")
			}
			return
		}
		if isStats {
			keys, _ := script.Echo(uts).JQ(".[] | .pk").Replace("\"", "").Slice() //nolint:errcheck
			keys = filterByTP(keys)
			internal.PrintOutput(cmd.Flags(), fmt.Sprintf("%d visors\n", len(keys)), fmt.Sprintf("%d visors\n", len(keys)))
			return
		}
		if isMoreStats {
			script.Echo(uts).JQ(".[] | .version").Freq().Replace("\"", "").Stdout() //nolint:errcheck,gosec
			return
		}

		script.Echo(uts).JQ(".[] | \"\\(.pk) \\(.daily | to_entries[] | select(.value | tonumber > "+fmt.Sprintf("%d", minUT)+") | \"\\(.key) \\(.value)\")\"").Match(pk).Replace("\"", "").Stdout() //nolint:errcheck,gosec
	},
}
