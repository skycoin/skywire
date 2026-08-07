// Package climdisc cmd/skywire-cli/commands/mdisc/root.go c4-vis-cli
package climdisc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/bitfield/script"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
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
// Generates simple, descriptive filenames (e.g., "entries.json").
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
		// Use the last path segment (e.g., /dmsg-discovery/entries -> entries.json)
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

var (
	cacheDirDMSGD string
	cacheFilesAge int
	mdURL         string
	isStats       bool
	testEnv       bool
	// allEntries bool
	masterLogger = logging.NewMasterLogger()
)

func init() {
	dep := getDeployment()
	defaultTestEnv := isTestEnv()

	RootCmd.AddCommand(
		entryCmd,
		availableServersCmd,
		// The DMSG-Discovery /uptimes endpoint is consumed via
		// `skywire cli ut mdisc` for consistency with the other
		// uptime-data sources. See cmd/skywire-cli/commands/ut/mdisc.go.
	)
	RootCmd.Flags().BoolVar(&testEnv, "testenv", defaultTestEnv, "use test deployment")
	RootCmd.Flags().StringVar(&cacheDirDMSGD, "cdd", cacheDirPath(dep.DmsgDiscovery), "DMSG cache dir (\"\" to disable)")
	RootCmd.Flags().IntVarP(&cacheFilesAge, "cfa", "m", 5, "update cache file if older than n minutes")
	RootCmd.Flags().BoolVarP(&isStats, "stats", "s", false, "count the number of results")
	entryCmd.Flags().StringVar(&mdURL, "url", dep.DmsgDiscovery, "specify alternative DMSG discovery url")
	RootCmd.Flags().StringVar(&mdURL, "url", dep.DmsgDiscovery, "specify alternative DMSG discovery url")
	availableServersCmd.Flags().StringVar(&mdURL, "url", dep.DmsgDiscovery, "specify alternative DMSG discovery url")
}

// RootCmd is the command that contains sub-commands which interacts with DMSG services.
var RootCmd = &cobra.Command{
	Use:   "mdisc",
	Short: "Query DMSG Discovery",
	Long: `Query DMSG Discovery
	list entries in dmsg discovery

Use --testenv or SKYWIRETEST=1 to use test deployment services.`,
	Run: func(cmd *cobra.Command, _ []string) {
		// Handle --testenv flag: override URL and cache dir that weren't explicitly set
		if testEnv && !isTestEnv() {
			if !cmd.Flags().Changed("url") {
				mdURL = deployment.Test.DmsgDiscovery
			}
			if !cmd.Flags().Changed("cdd") {
				cacheDirDMSGD = cacheDirPath(deployment.Test.DmsgDiscovery)
			}
		}

		// Build full URL
		dmsgFullURL := mdURL + "/dmsg-discovery/entries"

		dmsgclientkeys := clirpc.FetchCachedServiceURL(cmd.Flags(), cacheFile(cacheDirDMSGD, dmsgFullURL), dmsgFullURL, cacheFilesAge)
		if isStats {
			stats, _ := script.Echo(dmsgclientkeys).JQ(".[]").CountLines() //nolint:errcheck
			internal.PrintOutput(cmd.Flags(), fmt.Sprintf("%d dmsg clients\n", stats), fmt.Sprintf("%d dmsg clients\n", stats))
			return
		}
		script.Echo(dmsgclientkeys).JQ(".[]").Replace("\"", "").Freq().Column(2).Stdout() //nolint:errcheck,gosec
	},
}

var entryCmd = &cobra.Command{
	Use:   "entry <visor-public-key>",
	Short: "Fetch an entry",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		pk := internal.ParsePK(cmd.Flags(), "visor-public-key", args[0])
		masterLogger.SetLevel(logrus.InfoLevel)

		// Use the RPC → DMSG → HTTP fallback chain so this works for
		// mdURL values with dmsg:// scheme too, not just http(s)://.
		// The raw response is a JSON-encoded disc.Entry so we decode
		// it directly rather than routing through disc.NewHTTP's
		// http.Client (which can't dial dmsg URLs).
		url := fmt.Sprintf("%s/dmsg-discovery/entry/%s", mdURL, pk.Hex())
		body, err := clirpc.FetchServiceURL(cmd.Flags(), url)
		internal.Catch(cmd.Flags(), err)

		var entry disc.Entry
		internal.Catch(cmd.Flags(), json.Unmarshal(body, &entry))
		internal.PrintOutput(cmd.Flags(), entry, fmt.Sprintln(&entry))
	},
}

var availableServersCmd = &cobra.Command{
	Use:   "servers",
	Short: "List dmsg servers with connected-client + available-session counts",
	Long: `List every dmsg server registered in discovery, sorted by load.

Reads the discovery's /all_servers endpoint (so saturated servers appear too,
not just those still advertising spare capacity) and, for each, shows the
connected-client count INFERRED from available sessions:

    connected ~= DefaultMaxSessions (2048) - available_sessions

The discovery entry only carries available sessions, so "connected~" assumes
every server runs the default 2048 max; a server with a custom max_sessions
would be off by the difference. A pv-t-style readout: most-loaded first.`,
	Run: func(cmd *cobra.Command, _ []string) {
		masterLogger.SetLevel(logrus.InfoLevel)
		// all_servers (not available_servers): a fully-loaded server drops out
		// of available_servers, but it's exactly the one worth seeing in a load
		// view. fetchAllServersAny is the same dmsg://-or-https scheme-aware
		// cached fetch `mdisc check` uses.
		entries, err := fetchAllServersAny(cmd, mdURL)
		internal.Catch(cmd.Flags(), err)
		printAvailableServers(cmd.Flags(), entries)
	},
}

func printAvailableServers(cmdFlags *pflag.FlagSet, entries []*disc.Entry) {
	type serverEntry struct {
		PublicKey         cipher.PubKey `json:"public_key"`
		Connected         int           `json:"connected"` // inferred: DefaultMaxSessions - avail_sess
		AvailableSessions int           `json:"avail_sess"`
		Address           string        `json:"address"`
		Version           string        `json:"version"`
		Registered        int64         `json:"registered"`
	}

	var serverEntries []serverEntry
	for _, entry := range entries {
		if entry.Server == nil {
			continue
		}
		avail := entry.Server.AvailableSessions
		connected := dmsg.DefaultMaxSessions - avail
		if connected < 0 {
			connected = 0
		}
		serverEntries = append(serverEntries, serverEntry{
			PublicKey:         entry.Static,
			Connected:         connected,
			AvailableSessions: avail,
			Address:           entry.Server.Address,
			Version:           entry.Version,
			Registered:        entry.Timestamp,
		})
	}
	// Most-loaded first, like `pv -t` sorts by transport count.
	sort.Slice(serverEntries, func(i, j int) bool {
		return serverEntries[i].Connected > serverEntries[j].Connected
	})

	var b bytes.Buffer
	w := tabwriter.NewWriter(&b, 0, 0, 5, ' ', tabwriter.TabIndent)
	_, err := fmt.Fprintln(w, "public-key\tconnected~\tavail-sess\taddress\tversion\tregistered")
	internal.Catch(cmdFlags, err)
	for _, s := range serverEntries {
		_, err := fmt.Fprintf(w, "%s\t%d\t%d\t%s\t%s\t%d\n",
			s.PublicKey, s.Connected, s.AvailableSessions, s.Address, s.Version, s.Registered)
		internal.Catch(cmdFlags, err)
	}
	internal.Catch(cmdFlags, w.Flush())
	internal.PrintOutput(cmdFlags, serverEntries, b.String())
}
