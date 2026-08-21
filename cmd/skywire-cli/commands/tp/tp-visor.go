// Package clitp cmd/skywire-cli/commands/tp/tp-visor.go c4-vis-cli
package clitp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"
	"github.com/tidwall/pretty"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/uptimestats"
)

// visorOnlineJQFilter builds the jq expression that keeps only the SD
// visors whose public key appears in the uptime "online" set, optionally
// narrowing by country/version. The SD visor address is "pk:port" while
// the uptime .pk is the bare public key, so the address MUST have its
// port stripped before matching — otherwise the join never hits and
// every visor is filtered out (the historical "tp v returns 0" bug).
func visorOnlineJQFilter(country, version string) string {
	countryCond := ""
	if country != "" {
		countryCond = ` | select(.geo.country == "` + country + `")`
	}
	versionCond := ""
	if version != "" {
		versionCond = ` | select(.version == "` + version + `")`
	}
	return `
	[ .ut[] | select(.on) | .pk ] as $online
	| .sd[]
	| select((.address | split(":")[0]) as $pk | $pk | IN($online[]))` + countryCond + versionCond + `
	| "\(.address) \(.geo.country) \(.version)"
	`
}

// onlineDataUsable reports whether an /uptimes payload can drive the
// online filter: it must be non-empty JSON AND contain at least one
// visor flagged on. An all-offline (or empty-array) payload means the
// online-status source is not reporting live state — filtering against
// it would zero out every visor, which reads as "no public visors
// exist". Callers treat a false return as "fail open, list unfiltered".
func onlineDataUsable(uts string) bool {
	if uts == "" {
		return false
	}
	var entries []uptimestats.VisorSummary
	if err := json.Unmarshal([]byte(uts), &entries); err != nil {
		return false
	}
	for _, e := range entries {
		if e.Online {
			return true
		}
	}
	return false
}

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
	visorListCmd.Flags().StringVarP(&vUTURL, "uturl", "w", deployment.Prod.TransportDiscovery, "uptime tracker url (TPD integrated)")
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
	clirpc.RegisterFetchFlags(visorListCmd)
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
		sds := clirpc.FetchCachedServiceURL(cmd.Flags(), vCacheFileSD, vSDURL+"/api/services?type="+visorServiceType, vCacheFilesAge)
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

		// --- Online-status data (days=1: we only read .on) ---
		// Fetch the uptime data up-front so we can detect the "online
		// source unavailable" case and fail OPEN rather than silently
		// filtering every visor out. The full visor-uptime list can time
		// out over the visor RPC (large payload, CXO miss); when that
		// happens uts is "" and joining against it would zero the result,
		// which reads as "no public visors exist" — a lie. Fall through to
		// the unfiltered (--noton) path with a warning instead.
		online := ""
		if !vNoFilterOnline {
			online = clirpc.FetchIntegratedUptimesDays(cmd.Flags(), vUTURL, vCacheFileUT, vCacheFilesAge, 1)
			if !onlineDataUsable(online) {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning: transport-discovery online-status data unavailable (no visor is reporting live-online); listing all registered visors unfiltered (retry, or use -o to silence this)") //nolint:errcheck,gosec
				vNoFilterOnline = true
			}
		}

		// --- No filtering case (or fail-open when online data is missing) ---
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
		// online is non-empty here: the fail-open branch above already
		// diverted the empty case to the unfiltered path.
		joinedJSON := fmt.Sprintf(`{"sd": %s, "ut": %s}`, sds, online)

		jqFilter := visorOnlineJQFilter(vCountry, vVersion)

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
