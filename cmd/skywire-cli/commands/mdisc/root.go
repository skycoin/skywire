// Package climdisc root.go
package climdisc

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"text/tabwriter"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	utilenv "github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
)

var mdAddr string
//var allEntries bool
var masterLogger = logging.NewMasterLogger()
var packageLogger = masterLogger.PackageLogger("mdisc:disc")

func init() {
	RootCmd.AddCommand(
		entryCmd,
		availableServersCmd,
	)
	entryCmd.PersistentFlags().StringVarP(&mdAddr, "addr", "a", "", "DMSG discovery server address\n"+utilenv.DmsgDiscAddr)
//	entryCmd.PersistentFlags().BoolVarP(&allEntries, "entries", "e", "", "get all entries")
	availableServersCmd.PersistentFlags().StringVar(&mdAddr, "addr", utilenv.DmsgDiscAddr, "address of DMSG discovery server\n")
	var helpflag bool
	RootCmd.Flags().BoolVarP(&helpflag, "help", "h", false, "help for "+RootCmd.Use)
	RootCmd.Flags().MarkHidden("help") //nolint
}

// RootCmd is the command that contains sub-commands which interacts with DMSG services.
var RootCmd = &cobra.Command{
	Use:   "mdisc",
	Short: "Query remote DMSG Discovery",
}

var entryCmd = &cobra.Command{
	Use:   "entry <visor-public-key>",
	Short: "Fetch an entry",
//	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		//print help on no args
		if len(args) == 0 {
			cmd.Help() //nolint
		} else {
			if mdAddr == "" {
				mdAddr = utilenv.DmsgDiscAddr
			}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()
		pk := internal.ParsePK(cmd.Flags(), "visor-public-key", args[0])

		masterLogger.SetLevel(logrus.InfoLevel)

//TODO: fetch all entries
//		if allEntries {
//			entries, err := disc.NewHTTP(mdAddr, &http.Client{}, packageLogger).AvailableServers(ctx)
//		}

		entry, err := disc.NewHTTP(mdAddr, &http.Client{}, packageLogger).Entry(ctx, pk)
		internal.Catch(cmd.Flags(), err)
		internal.PrintOutput(cmd.Flags(), entry, fmt.Sprintln(entry))
	}
	},
}

var availableServersCmd = &cobra.Command{
	Use:   "servers",
	Short: "Fetch available servers",
	Run: func(cmd *cobra.Command, _ []string) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()

		masterLogger.SetLevel(logrus.InfoLevel)

		entries, err := disc.NewHTTP(mdAddr, &http.Client{}, packageLogger).AvailableServers(ctx)
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
