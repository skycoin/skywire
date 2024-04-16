// Package climdisc root.go
package climdisc

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"text/tabwriter"
	"time"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/bitfield/script"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	utilenv "github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
)

var (
	cacheFileDMSGD   string
	cacheFilesAge int
	mdURL string
	isStats       bool
)
// var allEntries bool
var masterLogger = logging.NewMasterLogger()
var packageLogger = masterLogger.PackageLogger("mdisc:disc")

func init() {
	RootCmd.AddCommand(
		entryCmd,
		availableServersCmd,
	)
	RootCmd.Flags().StringVar(&cacheFileDMSGD, "cfu", os.TempDir()+"/dmsgd.json", "DMSGD cache file location.")
	RootCmd.Flags().IntVarP(&cacheFilesAge, "cfa", "m", 5, "update cache file if older than n minutes")
	RootCmd.Flags().BoolVarP(&isStats, "stats", "s", false, "count the number of results")
	entryCmd.Flags().StringVar(&mdURL, "url", utilenv.DmsgDiscAddr, "specify alternative DMSG discovery url")
	RootCmd.Flags().StringVar(&mdURL, "url", utilenv.DmsgDiscAddr, "specify alternative DMSG discovery url")
	availableServersCmd.Flags().StringVar(&mdURL, "url", utilenv.DmsgDiscAddr, "specify alternative DMSG discovery url")
}

// RootCmd is the command that contains sub-commands which interacts with DMSG services.
var RootCmd = &cobra.Command{
	Use:   "mdisc",
	Short: "Query DMSG Discovery",
	Long: `Query DMSG Discovery
	list entries in dmsg discovery`,
	Run: func(cmd *cobra.Command, args []string) {
		dmsgclientkeys := internal.GetData(cacheFileDMSGD, mdURL+"/dmsg-discovery/entries", cacheFilesAge)
		if isStats {
			stats, _ := script.Echo(dmsgclientkeys).JQ(".[]").CountLines() //nolint
			internal.PrintOutput(cmd.Flags(), fmt.Sprintf("%d dmsg clients\n", stats), fmt.Sprintf("%d dmsg clients\n", stats))
			return
		}
		script.Echo(dmsgclientkeys).JQ(".[]").Replace("\"", "").Stdout() //nolint
	},
}

var entryCmd = &cobra.Command{
	Use:   "entry <visor-public-key>",
	Short: "Fetch an entry",
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			cmd.Help() //nolint
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
	_, err := fmt.Fprintln(w, "version\tregistered\tpublic-key\taddress\tavailable-sessions")
	internal.Catch(cmdFlags, err)

	type serverEntry struct {
		Version           string        `json:"version"`
		Registered        int64         `json:"registered"`
		PublicKey         cipher.PubKey `json:"public_key"`
		Address           string        `json:"address"`
		AvailableSessions int           `json:"available_sessions"`
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
