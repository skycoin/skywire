// Package cliut cmd/skywire-cli/ut/root.go
package cliut

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/transport"
)

// RootCmd is utCmd
var RootCmd = utCmd

var (
	pk            string
	online        bool
	isStats       bool
	isMoreStats   bool
	utURL         = deployment.Prod.UptimeTracker
	tpdURL        = deployment.Prod.TransportDiscovery
	cacheFileUT   string
	cacheFileTPD  string
	cacheFilesAge int
	versionFilter string
	listVersions  bool
	maxTP         int
)

var minUT int

func init() {
	utCmd.Flags().StringVarP(&pk, "pk", "k", "", "check uptime for the specified key")
	utCmd.Flags().BoolVarP(&online, "on", "o", false, "list currently online visors")
	utCmd.Flags().BoolVarP(&isStats, "stats", "s", false, "count the number of results")
	utCmd.Flags().BoolVarP(&isMoreStats, "stats2", "t", false, "count of versions")
	utCmd.Flags().IntVarP(&minUT, "min", "n", 75, "list visors meeting minimum uptime percentage\n\r")
	utCmd.Flags().StringVar(&cacheFileUT, "cfu", os.TempDir()+"/ut.json", "UT cache file location\n\r")
	utCmd.Flags().StringVar(&cacheFileTPD, "cft", os.TempDir()+"/tpd.json", "TPD cache file location\n\r")
	utCmd.Flags().IntVarP(&cacheFilesAge, "cfa", "m", 5, "update cache files if older than n minutes\n\r")
	utCmd.Flags().StringVarP(&utURL, "url", "u", utURL, "specify alternative uptime tracker url\n\r")
	utCmd.Flags().StringVarP(&versionFilter, "version", "v", "", "filter visors by version")
	utCmd.Flags().BoolVarP(&listVersions, "list-versions", "l", false, "list PKs with their versions")
	utCmd.Flags().IntVar(&maxTP, "max-tp", -1, "filter visors with at most N transports (fetches TPD data)")
}

var utCmd = &cobra.Command{
	Use:   "ut",
	Short: "query uptime tracker",
	Long:  fmt.Sprintf("query uptime tracker\n\n%v/uptimes?v=v2\n\nCheck local visor daily uptime percent with:\n\n$ skywire-cli ut -n0 -k $(skywire-cli visor pk)\n\nSet cache file location to \"\" to avoid using cache files", utURL),
	Run: func(cmd *cobra.Command, _ []string) {
		uts := internal.GetData(cacheFileUT, utURL+"/uptimes?v=v2", cacheFilesAge)

		// Build transport count map if --max-tp is specified
		var tpCount map[string]int
		if maxTP >= 0 {
			tpd := internal.GetData(cacheFileTPD, tpdURL+"/all-transports", cacheFilesAge)
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

		// Handle version filter
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
