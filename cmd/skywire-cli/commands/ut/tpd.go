// Package cliut cmd/skywire-cli/ut/tpd.go
package cliut

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
)

// TPD subcommand variables (separate from main ut command)
var (
	tpdPK            string
	tpdOnline        bool
	tpdIsStats       bool
	tpdIsMoreStats   bool
	tpdCacheDirTPD   string
	tpdCacheFilesAge int
	tpdVersionFilter string
	tpdMinVersion    string
	tpdListVersions  bool
	tpdMinUT         int
	tpdTestEnv       bool
	tpdUptimeURL     string
)

func init() {
	dep := getDeployment()
	defaultTestEnv := isTestEnv()

	utCmd.AddCommand(tpdCmd)

	tpdCmd.Flags().BoolVar(&tpdTestEnv, "testenv", defaultTestEnv, "use test deployment")
	tpdCmd.Flags().StringVarP(&tpdPK, "pk", "k", "", "check uptime for the specified key")
	tpdCmd.Flags().BoolVarP(&tpdOnline, "on", "o", false, "list currently online visors")
	tpdCmd.Flags().BoolVarP(&tpdIsStats, "stats", "s", false, "count the number of results")
	tpdCmd.Flags().BoolVarP(&tpdIsMoreStats, "stats2", "t", false, "count of versions")
	tpdCmd.Flags().IntVarP(&tpdMinUT, "min", "n", 75, "list visors meeting minimum uptime percentage")
	tpdCmd.Flags().StringVar(&tpdCacheDirTPD, "cdt", cacheDirPath(dep.TransportDiscovery), "TPD cache dir (\"\" to disable)")
	tpdCmd.Flags().IntVarP(&tpdCacheFilesAge, "cfa", "m", 5, "update cache files if older than n minutes")
	tpdCmd.Flags().StringVar(&tpdUptimeURL, "url", dep.TransportDiscovery, "transport discovery url")
	tpdCmd.Flags().StringVarP(&tpdVersionFilter, "version", "v", "", "filter visors by exact version")
	tpdCmd.Flags().StringVar(&tpdMinVersion, "min-version", "", "filter visors with version >= specified (e.g. v1.3.34)")
	tpdCmd.Flags().BoolVarP(&tpdListVersions, "list-versions", "l", false, "list PKs with their versions")
}

var tpdCmd = &cobra.Command{
	Use:   "tpd",
	Short: "query TPD integrated uptime tracker",
	Long: fmt.Sprintf(`Query the TPD integrated uptime tracker

%v/uptimes

This queries the uptime data from the Transport Discovery service
instead of the separate Uptime Tracker service.

Use --testenv or SKYWIRETEST=1 to use test deployment services.`, getDeployment().TransportDiscovery),
	Run: func(cmd *cobra.Command, _ []string) {
		// Handle --testenv flag: override URL and cache dir that weren't explicitly set
		if tpdTestEnv && !isTestEnv() {
			if !cmd.Flags().Changed("url") {
				tpdUptimeURL = deployment.Test.TransportDiscovery
			}
			if !cmd.Flags().Changed("cdt") {
				tpdCacheDirTPD = cacheDirPath(deployment.Test.TransportDiscovery)
			}
		}

		// Build full URL - TPD integrated uptime endpoint
		// Note: TPD uses /uptimes (same as separate UT but on TPD host)
		tpdFullURL := tpdUptimeURL + "/uptimes"

		uts := internal.GetData(cacheFile(tpdCacheDirTPD, tpdFullURL), tpdFullURL, tpdCacheFilesAge)

		// Build the base selector based on online flag
		baseSelector := ".[]"
		if tpdOnline {
			baseSelector = ".[] | select(.on)"
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
		if tpdMinVersion != "" {
			// Parse UT data to filter by version
			type utEntry struct {
				PK      string `json:"pk"`
				On      bool   `json:"on"`
				Version string `json:"version"`
			}
			var utEntries []utEntry
			if err := json.Unmarshal([]byte(uts), &utEntries); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to parse TPD uptime data: %w", err))
			}

			var keys []string
			for _, e := range utEntries {
				if tpdOnline && !e.On {
					continue
				}
				if !versionGTE(e.Version, tpdMinVersion) {
					continue
				}
				if tpdPK != "" && !strings.Contains(e.PK, tpdPK) {
					continue
				}
				keys = append(keys, e.PK)
			}

			if tpdIsStats {
				label := "visors"
				if tpdOnline {
					label = "online visors"
				}
				internal.PrintOutput(cmd.Flags(), fmt.Sprintf("%d %s with version >= %s\n", len(keys), label, tpdMinVersion), fmt.Sprintf("%d %s with version >= %s\n", len(keys), label, tpdMinVersion))
				return
			}
			for _, k := range keys {
				internal.PrintOutput(cmd.Flags(), k+"\n", k+"\n")
			}
			return
		}

		// Handle exact version filter
		if tpdVersionFilter != "" {
			versionSelector := baseSelector + " | select(.version == \"" + tpdVersionFilter + "\")"
			keys, _ := script.Echo(uts).JQ(versionSelector+" | .pk").Match(tpdPK).Replace("\"", "").Slice() //nolint:errcheck
			if tpdIsStats {
				label := "visors"
				if tpdOnline {
					label = "online visors"
				}
				internal.PrintOutput(cmd.Flags(), fmt.Sprintf("%d %s with version %s\n", len(keys), label, tpdVersionFilter), fmt.Sprintf("%d %s with version %s\n", len(keys), label, tpdVersionFilter))
				return
			}
			if tpdListVersions {
				for _, k := range keys {
					fmt.Printf("%s %s\n", k, tpdVersionFilter)
				}
				return
			}
			for _, i := range keys {
				internal.PrintOutput(cmd.Flags(), i+"\n", i+"\n")
			}
			return
		}

		// Handle list-versions flag (without version filter)
		if tpdListVersions {
			if tpdIsStats {
				script.Echo(uts).JQ(baseSelector+" | .version").Freq().Replace("\"", "").Stdout() //nolint:errcheck,gosec
				return
			}
			script.Echo(uts).JQ(baseSelector+" | \"\\(.pk) \\(.version)\"").Match(tpdPK).Replace("\"", "").Stdout() //nolint:errcheck,gosec
			return
		}

		if tpdOnline {
			utKeysOnline, _ := script.Echo(uts).JQ(".[] | select(.on) | .pk").Match(tpdPK).Replace("\"", "").Slice() //nolint:errcheck
			if tpdIsStats {
				internal.PrintOutput(cmd.Flags(), fmt.Sprintf("%d visors online\n", len(utKeysOnline)), fmt.Sprintf("%d visors online\n", len(utKeysOnline)))
				return
			}
			if tpdIsMoreStats {
				script.Echo(uts).JQ(".[] | select(.on) | .version").Freq().Replace("\"", "").Stdout() //nolint:errcheck,gosec
				return
			}
			for _, i := range utKeysOnline {
				internal.PrintOutput(cmd.Flags(), i+"\n", i+"\n")
			}
			return
		}
		if tpdIsStats {
			keys, _ := script.Echo(uts).JQ(".[] | .pk").Replace("\"", "").Slice() //nolint:errcheck
			internal.PrintOutput(cmd.Flags(), fmt.Sprintf("%d visors\n", len(keys)), fmt.Sprintf("%d visors\n", len(keys)))
			return
		}
		if tpdIsMoreStats {
			script.Echo(uts).JQ(".[] | .version").Freq().Replace("\"", "").Stdout() //nolint:errcheck,gosec
			return
		}

		script.Echo(uts).JQ(".[] | \"\\(.pk) \\(.daily | to_entries[] | select(.value | tonumber > "+fmt.Sprintf("%d", tpdMinUT)+") | \"\\(.key) \\(.value)\")\"").Match(tpdPK).Replace("\"", "").Stdout() //nolint:errcheck,gosec
	},
}
