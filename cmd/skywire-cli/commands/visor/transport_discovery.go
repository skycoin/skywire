package visor

import (
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/transport"
)

func init() {
	RootCmd.AddCommand(discTpCmd)
}

var (
	tpID transportID
	tpPK cipher.PubKey
)

func init() {
	discTpCmd.Flags().Var(&tpID, "id", "if specified, obtains a single transport of given ID")
	discTpCmd.Flags().Var(&tpPK, "pk", "if specified, obtains transports associated with given public key")
}

var discTpCmd = &cobra.Command{
	Use:   "disc-tp (--id=<transport-id> | --pk=<edge-public-key>)",
	Short: "Queries the Transport Discovery to find transport(s) of given transport ID or edge public key",
	Args: func(_ *cobra.Command, _ []string) error {
		var (
			nilID = uuid.UUID(tpID) == (uuid.UUID{})
			nilPK = tpPK.Null()
		)
		if nilID && nilPK {
			return errors.New("must specify --id flag or --pk flag")
		}
		if !nilID && !nilPK {
			return errors.New("cannot specify --id and --pk flag")
		}
		return nil
	},
	Run: func(_ *cobra.Command, _ []string) {

		if rc := rpcClient(); tpPK.Null() {
			entry, err := rc.DiscoverTransportByID(uuid.UUID(tpID))
			internal.Catch(err)
			printTransportEntries(entry)
		} else {
			entries, err := rc.DiscoverTransportsByPK(tpPK)
			internal.Catch(err)
			printTransportEntries(entries...)
		}
	},
}

func printTransportEntries(entries ...*transport.EntryWithStatus) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 5, ' ', tabwriter.TabIndent)
	_, err := fmt.Fprintln(w, "id\ttype\tpublic\tregistered\tup\tedge1\tedge2\topinion1\topinion2")
	internal.Catch(err)
	for _, e := range entries {
		_, err := fmt.Fprintf(w, "%s\t%s\t%t\t%d\t%t\t%s\t%s\t%t\t%t\n",
			e.Entry.ID, e.Entry.Type, e.Entry.Public, e.Registered, e.IsUp, e.Entry.Edges[0], e.Entry.Edges[1], e.Statuses[0], e.Statuses[1])
		internal.Catch(err)
	}
	internal.Catch(w.Flush())
}

type transportID uuid.UUID

// String implements pflag.Value
func (t transportID) String() string { return uuid.UUID(t).String() }

// Type implements pflag.Value
func (transportID) Type() string { return "transportID" }

// Set implements pflag.Value
func (t *transportID) Set(s string) error {
	tID, err := uuid.Parse(s)
	if err != nil {
		return err
	}
	*t = transportID(tID)
	return nil
}
