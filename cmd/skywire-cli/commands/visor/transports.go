package visor

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/skycoin/dmsg/cipher"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/visor"
)

func init() {
	RootCmd.AddCommand(
		lsTypesCmd,
		lsTpCmd,
		tpCmd,
		addTpCmd,
		rmTpCmd,
	)
}

var lsTypesCmd = &cobra.Command{
	Use:   "ls-types",
	Short: "Lists transport types used by the local visor",
	Run: func(_ *cobra.Command, _ []string) {
		types, err := rpcClient().TransportTypes()
		internal.Catch(err)
		for _, t := range types {
			fmt.Println(t)
		}
	},
}

var (
	filterTypes   []string
	filterPubKeys cipher.PubKeys
	showLogs      bool
)

func init() {
	lsTpCmd.Flags().StringSliceVar(&filterTypes, "filter-types", filterTypes, "comma-separated; if specified, only shows transports of given types")
	lsTpCmd.Flags().Var(&filterPubKeys, "filter-pks", "comma-separated; if specified, only shows transports associated with given visors")
	lsTpCmd.Flags().BoolVar(&showLogs, "show-logs", true, "whether to show transport logs in output")
}

var lsTpCmd = &cobra.Command{
	Use:   "ls-tp",
	Short: "Lists the available transports with optional filter flags",
	Run: func(_ *cobra.Command, _ []string) {
		transports, err := rpcClient().Transports(filterTypes, filterPubKeys, showLogs)
		internal.Catch(err)
		printTransports(transports...)
	},
}

var tpCmd = &cobra.Command{
	Use:   "tp <transport-id>",
	Short: "Returns summary of given transport by id",
	Args:  cobra.MinimumNArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		tpID := internal.ParseUUID("transport-id", args[0])
		tp, err := rpcClient().Transport(tpID)
		internal.Catch(err)
		printTransports(tp)
	},
}

var (
	transportType string
	public        bool
	timeout       time.Duration
)

func init() {
	const (
		typeFlagUsage = "type of transport to add; if unspecified, cli will attempt to establish a transport " +
			"in the following order: skywire-tcp, stcpr, sudph, dmsg"
		publicFlagUsage  = "whether to make the transport public (deprecated)"
		timeoutFlagUsage = "if specified, sets an operation timeout"
	)

	addTpCmd.Flags().StringVar(&transportType, "type", "", typeFlagUsage)
	addTpCmd.Flags().BoolVar(&public, "public", true, publicFlagUsage)
	addTpCmd.Flags().DurationVarP(&timeout, "timeout", "t", 0, timeoutFlagUsage)
}

var addTpCmd = &cobra.Command{
	Use:   "add-tp <remote-public-key>",
	Short: "Adds a new transport",
	Args:  cobra.MinimumNArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		pk := internal.ParsePK("remote-public-key", args[0])

		var tp *visor.TransportSummary
		var err error

		if transportType != "" {
			tp, err = rpcClient().AddTransport(pk, transportType, timeout)
			if err != nil {
				logger.WithError(err).Fatalf("Failed to establish %v transport", transportType)
			}

			logger.Infof("Established %v transport to %v", transportType, pk)
		} else {
			transportTypes := []network.Type{
				network.STCP,
				network.STCPR,
				network.SUDPH,
				network.DMSG,
			}

			for _, transportType := range transportTypes {
				tp, err = rpcClient().AddTransport(pk, string(transportType), timeout)
				if err == nil {
					logger.Infof("Established %v transport to %v", transportType, pk)
					break
				}

				logger.WithError(err).Warnf("Failed to establish %v transport", transportType)
			}
		}

		printTransports(tp)
	},
}

var rmTpCmd = &cobra.Command{
	Use:   "rm-tp <transport-id>",
	Short: "Removes transport with given id",
	Args:  cobra.MinimumNArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		tID := internal.ParseUUID("transport-id", args[0])
		internal.Catch(rpcClient().RemoveTransport(tID))
		fmt.Println("OK")
	},
}

func printTransports(tps ...*visor.TransportSummary) {
	sortTransports(tps...)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 5, ' ', tabwriter.TabIndent)
	_, err := fmt.Fprintln(w, "type\tid\tremote\tmode\tlabel")
	internal.Catch(err)
	for _, tp := range tps {
		tpMode := "regular"
		if tp.IsSetup {
			tpMode = "setup"
		}
		_, err = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", tp.Type, tp.ID, tp.Remote, tpMode, tp.Label)
		internal.Catch(err)
	}
	internal.Catch(w.Flush())
}

func sortTransports(tps ...*visor.TransportSummary) {
	sort.Slice(tps, func(i, j int) bool {
		return tps[i].ID.String() < tps[j].ID.String()
	})
}
