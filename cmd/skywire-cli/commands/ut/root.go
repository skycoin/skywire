// Package cliut cmd/skywire-cli/ut/root.go
package cliut

import (
	"fmt"
	"os"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
)

// RootCmd is utCmd
var RootCmd = utCmd

var (
	pk            string
	online        bool
	isStats       bool
	isMoreStats   bool
	utURL         = deployment.Prod.UptimeTracker
	cacheFileUT   string
	cacheFilesAge int
	versionFilter string
	listVersions  bool
)

var minUT int

func init() {
	utCmd.Flags().StringVarP(&pk, "pk", "k", "", "check uptime for the specified key")
	utCmd.Flags().BoolVarP(&online, "on", "o", false, "list currently online visors")
	utCmd.Flags().BoolVarP(&isStats, "stats", "s", false, "count the number of results")
	utCmd.Flags().BoolVarP(&isMoreStats, "stats2", "t", false, "count of versions")
	utCmd.Flags().IntVarP(&minUT, "min", "n", 75, "list visors meeting minimum uptime percentage\n\r")
	utCmd.Flags().StringVar(&cacheFileUT, "cfu", os.TempDir()+"/ut.json", "UT cache file location\n\r")
	utCmd.Flags().IntVarP(&cacheFilesAge, "cfa", "m", 5, "update cache files if older than n minutes\n\r")
	utCmd.Flags().StringVarP(&utURL, "url", "u", utURL, "specify alternative uptime tracker url\n\r")
	utCmd.Flags().StringVarP(&versionFilter, "version", "v", "", "filter visors by version")
	utCmd.Flags().BoolVarP(&listVersions, "list-versions", "l", false, "list PKs with their versions")
}

var utCmd = &cobra.Command{
	Use:   "ut",
	Short: "query uptime tracker",
	Long:  fmt.Sprintf("query uptime tracker\n\n%v/uptimes?v=v2\n\nCheck local visor daily uptime percent with:\n\n$ skywire-cli ut -n0 -k $(skywire-cli visor pk)\n\nSet cache file location to \"\" to avoid using cache files", utURL),
	Run: func(cmd *cobra.Command, _ []string) {
		uts := internal.GetData(cacheFileUT, utURL+"/uptimes?v=v2", cacheFilesAge)

		// Build the base selector based on online flag
		baseSelector := ".[]"
		if online {
			baseSelector = ".[] | select(.on)"
		}

		// Handle version filter
		if versionFilter != "" {
			versionSelector := baseSelector + " | select(.version == \"" + versionFilter + "\")"
			if isStats {
				stats, _ := script.Echo(uts).JQ(versionSelector + " | .pk").CountLines() //nolint:errcheck
				label := "visors"
				if online {
					label = "online visors"
				}
				internal.PrintOutput(cmd.Flags(), fmt.Sprintf("%d %s with version %s\n", stats, label, versionFilter), fmt.Sprintf("%d %s with version %s\n", stats, label, versionFilter))
				return
			}
			if listVersions {
				script.Echo(uts).JQ(versionSelector+" | \"\\(.pk) \\(.version)\"").Match(pk).Replace("\"", "").Stdout() //nolint:errcheck,gosec
				return
			}
			keys, _ := script.Echo(uts).JQ(versionSelector+" | .pk").Match(pk).Replace("\"", "").Slice() //nolint:errcheck
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
			if isStats {
				stats, _ := script.Echo(uts).JQ(".[] | select(.on) | .pk").CountLines() //nolint:errcheck
				internal.PrintOutput(cmd.Flags(), fmt.Sprintf("%d visors online\n", stats), fmt.Sprintf("%d visors online\n", stats))
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
			stats, _ := script.Echo(uts).JQ(".[] | .pk").CountLines() //nolint:errcheck
			internal.PrintOutput(cmd.Flags(), fmt.Sprintf("%d visors\n", stats), fmt.Sprintf("%d visors\n", stats))
			return
		}
		if isMoreStats {
			script.Echo(uts).JQ(".[] | .version").Freq().Replace("\"", "").Stdout() //nolint:errcheck,gosec
			return
		}

		script.Echo(uts).JQ(".[] | \"\\(.pk) \\(.daily | to_entries[] | select(.value | tonumber > "+fmt.Sprintf("%d", minUT)+") | \"\\(.key) \\(.value)\")\"").Match(pk).Replace("\"", "").Stdout() //nolint:errcheck,gosec
	},
}
