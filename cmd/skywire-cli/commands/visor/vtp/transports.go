package vtp

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/visor"
)

func init() {
	lsTpCmd.Flags().SortFlags = false
	RootCmd.AddCommand(
		lsTypesCmd,
		lsTpCmd,
		tpCmd,
		addTpCmd,
		rmTpCmd,
	)
}

var lsTypesCmd = &cobra.Command{
	Use:   "type",
	Short: "transport types used by the local visor",
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
	lsTpCmd.Flags().StringSliceVarP(&filterTypes, "types", "t", filterTypes, "show transport(s) type(s) comma-separated")
	lsTpCmd.Flags().VarP(&filterPubKeys, "pks", "p", "show transport(s) for public key(s) comma-separated")
	lsTpCmd.Flags().BoolVarP(&showLogs, "logs", "l", true, "show transport logs")
}

var lsTpCmd = &cobra.Command{
	Use:   "ls",
	Short: "available transports",
	Run: func(_ *cobra.Command, _ []string) {
		transports, err := rpcClient().Transports(filterTypes, filterPubKeys, showLogs)
		internal.Catch(err)
		printTransports(transports...)
	},
}

var tpCmd = &cobra.Command{
	Use:   "id <transport-id>",
	Short: "transport summary by id",
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
	Use:   "add <remote-public-key>",
	Short: "add a transport",
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
	Use:   "rm <transport-id>",
	Short: "remove transport(s) by id",
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
