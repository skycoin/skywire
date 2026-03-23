// Package climdisc root.go
package climdisc

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bitfield/script"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
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
	masterLogger  = logging.NewMasterLogger()
	packageLogger = masterLogger.PackageLogger("mdisc:disc")
)

func init() {
	dep := getDeployment()
	defaultTestEnv := isTestEnv()

	RootCmd.AddCommand(
		entryCmd,
		availableServersCmd,
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

		dmsgclientkeys := internal.GetData(cacheFile(cacheDirDMSGD, dmsgFullURL), dmsgFullURL, cacheFilesAge)
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
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			internal.Catch(cmd.Flags(), cmd.Help())
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()
		pk := internal.ParsePK(cmd.Flags(), "visor-public-key", args[0])

		masterLogger.SetLevel(logrus.InfoLevel)
		entry, err := disc.NewHTTP(mdURL, &http.Client{}, packageLogger).Entry(ctx, pk)
		internal.Catch(cmd.Flags(), err)
		internal.PrintOutput(cmd.Flags(), entry, fmt.Sprintln(entry))
	},
}

var availableServersCmd = &cobra.Command{
	Use:   "servers",
	Short: "Fetch available servers",
	Run: func(cmd *cobra.Command, _ []string) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()

		masterLogger.SetLevel(logrus.InfoLevel)

		entries, err := disc.NewHTTP(mdURL, &http.Client{}, packageLogger).AvailableServers(ctx)
		internal.Catch(cmd.Flags(), err)
		printAvailableServers(cmd.Flags(), entries)
	},
}

func printAvailableServers(cmdFlags *pflag.FlagSet, entries []*disc.Entry) {
	var b bytes.Buffer
	w := tabwriter.NewWriter(&b, 0, 0, 5, ' ', tabwriter.TabIndent)
	_, err := fmt.Fprintln(w, "version\tregistered\tpublic-key\taddress\tavail-sess")
	internal.Catch(cmdFlags, err)

	type serverEntry struct {
		Version           string        `json:"version"`
		Registered        int64         `json:"registered"`
		PublicKey         cipher.PubKey `json:"public_key"`
		Address           string        `json:"address"`
		AvailableSessions int           `json:"avail_sess"`
	}

	var serverEntries []serverEntry

	for _, entry := range entries {
		_, err := fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%d\n",
			entry.Version, entry.Timestamp, entry.Static, entry.Server.Address, entry.Server.AvailableSessions)
		sEntry := serverEntry{
			Version:           entry.Version,
			Registered:        entry.Timestamp,
			PublicKey:         entry.Static,
			Address:           entry.Server.Address,
			AvailableSessions: entry.Server.AvailableSessions,
		}
		serverEntries = append(serverEntries, sEntry)
		internal.Catch(cmdFlags, err)
	}
	internal.Catch(cmdFlags, w.Flush())
	internal.PrintOutput(cmdFlags, serverEntries, b.String())
}
