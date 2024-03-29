// Package cliut cmd/skywire-cli/ut/root.go
package cliut

import (
	"fmt"
	"os"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"

	utilenv "github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
)

// RootCmd is utCmd
var RootCmd = utCmd

var (
	pk            string
	online        bool
	isStats       bool
	utURL         string
	cacheFileUT   string
	cacheFilesAge int
)

var minUT int

func init() {
	utCmd.Flags().StringVarP(&pk, "pk", "k", "", "check uptime for the specified key")
	utCmd.Flags().BoolVarP(&online, "on", "o", false, "list currently online visors")
	utCmd.Flags().BoolVarP(&isStats, "stats", "s", false, "count the number of results")
	utCmd.Flags().IntVarP(&minUT, "min", "n", 75, "list visors meeting minimum uptime")
	utCmd.Flags().StringVar(&cacheFileUT, "cfu", os.TempDir()+"/ut.json", "UT cache file location.")
	utCmd.Flags().IntVarP(&cacheFilesAge, "cfa", "m", 5, "update cache files if older than n minutes")
	utCmd.Flags().StringVarP(&utURL, "url", "u", utilenv.UptimeTrackerAddr, "specify alternative uptime tracker url")
}

var utCmd = &cobra.Command{
	Use:   "ut",
	Short: "query uptime tracker",
	Long:  fmt.Sprintf("query uptime tracker\n\n%v/uptimes?v=v2\n\nCheck local visor daily uptime percent with:\n skywire-cli ut -k $(skywire-cli visor pk)n\nSet cache file location to \"\" to avoid using cache files", utilenv.UptimeTrackerAddr),
	Run: func(cmd *cobra.Command, _ []string) {
		uts := internal.GetData(cacheFileUT, utURL+"/uptimes?v=v2", cacheFilesAge)
		if online {
			utKeysOnline, _ := script.Echo(uts).JQ(".[] | select(.on) | .pk").Match(pk).Replace("\"", "").Slice() //nolint
			if isStats {
				internal.PrintOutput(cmd.Flags(), fmt.Sprintf("%d visors online\n", len(utKeysOnline)), fmt.Sprintf("%d visors online\n", len(utKeysOnline)))
				return
			}
			for _, i := range utKeysOnline {
				internal.PrintOutput(cmd.Flags(), i+"\n", i+"\n")
			}
			return
		}
		script.Echo(uts).JQ(".[] | \"\\(.pk) \\(.daily | to_entries[] | select(.value | tonumber > "+fmt.Sprintf("%d", minUT)+") | \"\\(.key) \\(.value)\")\"").Match(pk).Replace("\"", "").Stdout() //nolint
	},
}
