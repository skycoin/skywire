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
)

var minUT int

func init() {
	utCmd.Flags().StringVarP(&pk, "pk", "k", "", "check uptime for the specified key")
	utCmd.Flags().BoolVarP(&online, "on", "o", false, "list currently online visors")
	utCmd.Flags().BoolVarP(&isStats, "stats", "s", false, "count the number of results")
	utCmd.Flags().BoolVarP(&isMoreStats, "stats2", "t", false, "count of versions")
	utCmd.Flags().IntVarP(&minUT, "min", "n", 75, "list visors meeting minimum uptime percentage\033[0m\n\r")
	utCmd.Flags().StringVar(&cacheFileUT, "cfu", os.TempDir()+"/ut.json", "UT cache file location\033[0m\n\r")
	utCmd.Flags().IntVarP(&cacheFilesAge, "cfa", "m", 5, "update cache files if older than n minutes\033[0m\n\r")
	utCmd.Flags().StringVarP(&utURL, "url", "u", utURL, "specify alternative uptime tracker url\033[0m\n\r")
}

var utCmd = &cobra.Command{
	Use:   "ut",
	Short: "query uptime tracker",
	Long:  fmt.Sprintf("query uptime tracker\n\n%v/uptimes?v=v2\n\nCheck local visor daily uptime percent with:\n\n$ skywire-cli ut -n0 -k $(skywire-cli visor pk)\n\nSet cache file location to \"\" to avoid using cache files", utURL),
	Run: func(cmd *cobra.Command, _ []string) {
		uts := internal.GetData(cacheFileUT, utURL+"/uptimes?v=v2", cacheFilesAge)
		if online {
			utKeysOnline, _ := script.Echo(uts).JQ(".[] | select(.on) | .pk").Match(pk).Replace("\"", "").Slice() //nolint
			if isStats {
				stats, _ := script.Echo(uts).JQ(".[] | select(.on) | .pk").CountLines() //nolint
				internal.PrintOutput(cmd.Flags(), fmt.Sprintf("%d visors online\n", stats), fmt.Sprintf("%d visors online\n", stats))
				return
			}
			if isMoreStats {
				script.Echo(uts).JQ(".[] | select(.on) | .version").Freq().Replace("\"", "").Stdout()
				return
			}
			for _, i := range utKeysOnline {
				internal.PrintOutput(cmd.Flags(), i+"\n", i+"\n")
			}
			return
		}
		if isStats {
			stats, _ := script.Echo(uts).JQ(".[] | .pk").CountLines() //nolint
			internal.PrintOutput(cmd.Flags(), fmt.Sprintf("%d visors\n", stats), fmt.Sprintf("%d visors\n", stats))
			return
		}
		if isMoreStats {
			script.Echo(uts).JQ(".[] | .version").Freq().Replace("\"", "").Stdout()
			return
		}

		script.Echo(uts).JQ(".[] | \"\\(.pk) \\(.daily | to_entries[] | select(.value | tonumber > "+fmt.Sprintf("%d", minUT)+") | \"\\(.key) \\(.value)\")\"").Match(pk).Replace("\"", "").Stdout()
	},
}
