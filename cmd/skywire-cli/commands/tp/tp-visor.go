// Package clitp cmd/skywire-cli/commands/tp/tp-visor.go
package clitp

import (
	"bytes"
	"encoding/json"
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
	visorServiceType = servicedisc.ServiceTypeVisor
)

var (
	vSDURL          string
	vUTURL          string
	vCacheFileSD    string
	vCacheFileUT    string
	vCacheFilesAge  int
	vPK             string
	vCountry        string
	vVersion        string
	vRawData        bool
	vNoFilterOnline bool
	vIsStats        bool
	vJSONOutput     bool
)

func init() {
	visorListCmd.Flags().StringVarP(&vSDURL, "sdurl", "a", deployment.Prod.ServiceDiscovery, "service discovery url")
	visorListCmd.Flags().StringVarP(&vUTURL, "uturl", "w", deployment.Prod.UptimeTracker, "uptime tracker url")
	visorListCmd.Flags().BoolVarP(&vRawData, "raw", "r", false, "print raw json data")
	visorListCmd.Flags().BoolVarP(&vNoFilterOnline, "noton", "o", false, "do not filter by online status in UT")
	visorListCmd.Flags().StringVar(&vCacheFileSD, "cfs", os.TempDir()+"/visorsd.json", "SD cache file location")
	visorListCmd.Flags().StringVar(&vCacheFileUT, "cfu", os.TempDir()+"/ut.json", "UT cache file location.")
	visorListCmd.Flags().IntVarP(&vCacheFilesAge, "cfa", "m", 5, "update cache files if older than n minutes")
	visorListCmd.Flags().StringVarP(&vPK, "pk", "k", "", "check visor service discovery for public key")
	visorListCmd.Flags().StringVarP(&vCountry, "country", "c", "", "filter by country code")
	visorListCmd.Flags().StringVarP(&vVersion, "version", "e", "", "filter by version")
	visorListCmd.Flags().BoolVarP(&vIsStats, "stats", "s", false, "return only a count of the results")
	visorListCmd.Flags().BoolVar(&vJSONOutput, internal.JSONString, false, "print output in json")
}

var visorListCmd = &cobra.Command{
	Use:   "v",
	Short: "List public visors",
	Long: fmt.Sprintf(`List public visors from service discovery
%v/api/services?type=%v
%v/api/services?type=%v&country=US

Set cache file location to "" to avoid using cache files`,
		deployment.Prod.ServiceDiscovery, visorServiceType,
		deployment.Prod.ServiceDiscovery, visorServiceType),
	Run: func(cmd *cobra.Command, _ []string) {
		// --- Fetch SD ---
		sds := internal.GetData(vCacheFileSD, vSDURL+"/api/services?type="+visorServiceType, vCacheFilesAge)
		if vRawData {
			script.Echo(string(pretty.Color(pretty.Pretty([]byte(sds)), nil))).Stdout() //nolint:errcheck,gosec
			return
		}

		// --- If JSON output requested ---
		if vJSONOutput {
			var list []servicedisc.Service
			json.Unmarshal([]byte(sds), &list) //nolint:errcheck,gosec
			var b bytes.Buffer
			internal.PrintOutput(cmd.Flags(), list, b.String())
			return
		}

		// --- Lookup by PK ---
		if vPK != "" {
			// For visors, address is just the public key without port
			jsonOut, err := script.Echo(sds).
				JQ(`map(select(.address | startswith("` + vPK + `")))`).Bytes()
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("error: %w", err))
			}
			script.Echo(string(pretty.Color(pretty.Pretty(jsonOut), nil))).Stdout() //nolint:errcheck,gosec
			return
		}

		// --- No filtering case ---
		if vNoFilterOnline {
			sdJQ := `.[]`
			if vCountry != "" {
				sdJQ += ` | select(.geo.country == "` + vCountry + `")`
			}
			if vVersion != "" {
				sdJQ += ` | select(.version == "` + vVersion + `")`
			}
			// Address for visor is just the public key
			sdJQ += ` | "\(.address) \(.geo.country) \(.version)"`

			if vIsStats {
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
		uts := internal.GetData(vCacheFileUT, vUTURL+"/uptimes?v=v2", vCacheFilesAge)
		joinedJSON := fmt.Sprintf(`{"sd": %s, "ut": %s}`, sds, uts)

		// Build jq filter with optional country and version conditions
		countryCond := ""
		if vCountry != "" {
			countryCond = ` | select(.geo.country == "` + vCountry + `")`
		}
		versionCond := ""
		if vVersion != "" {
			versionCond = ` | select(.version == "` + vVersion + `")`
		}

		jqFilter := `
		[ .ut[] | select(.on) | .pk ] as $online
		| .sd[]
		| select(.address as $pk | $pk | IN($online[]))` + countryCond + versionCond + `
		| "\(.address) \(.geo.country) \(.version)"
		`

		if vIsStats {
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
