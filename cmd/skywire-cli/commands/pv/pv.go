// Package pv cmd/skywire-cli/commands/pv/pv.go
package pv

import (
	"fmt"
	"os"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"
	"github.com/tidwall/pretty"

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/servicedisc"
)

var (
	serviceType    = servicedisc.ServiceTypeVisor
	sdURL          string
	utURL          string
	cacheFileSD    string
	cacheFileUT    string
	cacheFilesAge  int
	rawData        bool
	noFilterOnline bool
	country        string
	version        string
	isStats        bool
)

func init() {
	RootCmd.Flags().StringVarP(&sdURL, "sdurl", "a", deployment.Prod.ServiceDiscovery, "service discovery url")
	RootCmd.Flags().StringVarP(&utURL, "uturl", "w", deployment.Prod.UptimeTracker, "uptime tracker url")
	RootCmd.Flags().BoolVarP(&rawData, "raw", "r", false, "print raw json data")
	RootCmd.Flags().BoolVarP(&noFilterOnline, "noton", "o", false, "do not filter by online status in UT")
	RootCmd.Flags().StringVar(&cacheFileSD, "cfs", os.TempDir()+"/visorsd.json", "SD cache file location")
	RootCmd.Flags().StringVar(&cacheFileUT, "cfu", os.TempDir()+"/ut.json", "UT cache file location")
	RootCmd.Flags().IntVarP(&cacheFilesAge, "cfa", "m", 5, "update cache files if older than n minutes")
	RootCmd.Flags().StringVarP(&country, "country", "c", "", "filter by country code")
	RootCmd.Flags().StringVarP(&version, "version", "v", "", "filter by version")
	RootCmd.Flags().BoolVarP(&isStats, "stats", "s", false, "return only a count of the results")
}

// RootCmd is the command for listing public visors
var RootCmd = &cobra.Command{
	Use:   "pv",
	Short: "Public Visors",
	Long: fmt.Sprintf(`List public visors from service discovery
%v/api/services?type=%v

Returns only public keys, one per line.
Set cache file location to "" to avoid using cache files`, deployment.Prod.ServiceDiscovery, serviceType),
	Run: func(cmd *cobra.Command, _ []string) {
		// Fetch SD
		sds := internal.GetData(cacheFileSD, sdURL+"/api/services?type="+serviceType, cacheFilesAge)
		if rawData {
			script.Echo(string(pretty.Color(pretty.Pretty([]byte(sds)), nil))).Stdout() //nolint:errcheck,gosec
			return
		}

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

		// Filter by online status via jq join
		uts := internal.GetData(cacheFileUT, utURL+"/uptimes?v=v2", cacheFilesAge)
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
