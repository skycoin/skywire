package clivisor

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	filterTypes   []string
	filterPubKeys cipher.PubKeys
	showLogs      bool
	tpID          transportID
	tpPK          cipher.PubKey
)

func init() {
	lsTpCmd.Flags().SortFlags = false
	RootCmd.AddCommand(tpCmd)
	tpCmd.AddCommand(
		lsTypesCmd,
		lsTpCmd,
		idCmd,
		addTpCmd,
		rmTpCmd,
		discTpCmd,
	)
	discTpCmd.Flags().Var(&tpID, "id", "if specified, obtains a single transport of given ID")
	discTpCmd.Flags().Var(&tpPK, "pk", "if specified, obtains transports associated with given public key")
}

// RootCmd contains commands that interact with the skywire-visor
var tpCmd = &cobra.Command{
	Use:   "tp",
	Short: "View and set transports",
	Long: `
	Transports are bidirectional communication protocols
	used between two Skywire Visors (or Transport Edges)

	Each Transport is represented as a unique 16 byte (128 bit)
	UUID value called the Transport ID
	and has a Transport Type that identifies
	a specific implementation of the Transport.`,
}

var lsTypesCmd = &cobra.Command{
	Use: "type", Short: "Transport types used by the local visor",
	Run: func(cmd *cobra.Command, _ []string) {
		types, err := clirpc.Client().TransportTypes()
		internal.Catch(cmd.Flags(), err)
		internal.PrintOutput(cmd.Flags(), types, fmt.Sprintln(strings.Join(types, "\n")))
	},
}

func init() {
	lsTpCmd.Flags().StringSliceVarP(&filterTypes, "types", "t", filterTypes, "show transport(s) type(s) comma-separated\n")
	lsTpCmd.Flags().VarP(&filterPubKeys, "pks", "p", "show transport(s) for public key(s) comma-separated")
	lsTpCmd.Flags().BoolVarP(&showLogs, "logs", "l", true, "show transport logs")
}

var lsTpCmd = &cobra.Command{
	Use:   "ls",
	Short: "Available transports",
	Run: func(cmd *cobra.Command, _ []string) {
		transports, err := clirpc.Client().Transports(filterTypes, filterPubKeys, showLogs)
		internal.Catch(cmd.Flags(), err)
		PrintTransports(cmd.Flags(), transports...)
	},
}

var idCmd = &cobra.Command{
	Use:   "id <transport-id>",
	Short: "Transport summary by id",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		tpID := internal.ParseUUID(cmd.Flags(), "transport-id", args[0])
		tp, err := clirpc.Client().Transport(tpID)
		internal.Catch(cmd.Flags(), err)
		PrintTransports(cmd.Flags(), tp)
	},
}

var (
	transportType string
	timeout       time.Duration
)

func init() {
	const (
		typeFlagUsage = "type of transport to add; if unspecified, cli will attempt to establish a transport " +
			"in the following order: skywire-tcp, stcpr, sudph, dmsg"
		timeoutFlagUsage = "if specified, sets an operation timeout"
	)

	addTpCmd.Flags().StringVar(&transportType, "type", "", typeFlagUsage)
	addTpCmd.Flags().DurationVarP(&timeout, "timeout", "t", 0, timeoutFlagUsage)
}

var addTpCmd = &cobra.Command{
	Use:   "add <remote-public-key>",
	Short: "Add a transport",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		pk := internal.ParsePK(cmd.Flags(), "remote-public-key", args[0])

		var tp *visor.TransportSummary
		var err error

		if transportType != "" {
			tp, err = clirpc.Client().AddTransport(pk, transportType, timeout)
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
				tp, err = clirpc.Client().AddTransport(pk, string(transportType), timeout)
				if err == nil {
					logger.Infof("Established %v transport to %v", transportType, pk)
					break
				}
				logger.WithError(err).Warnf("Failed to establish %v transport", transportType)
			}
		}
		PrintTransports(cmd.Flags(), tp)
	},
}

var rmTpCmd = &cobra.Command{
	Use:   "rm <transport-id>",
	Short: "Remove transport(s) by id",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		tID := internal.ParseUUID(cmd.Flags(), "transport-id", args[0])
		internal.Catch(cmd.Flags(), clirpc.Client().RemoveTransport(tID))
		fmt.Println("OK")
	},
}

// PrintTransports prints transports used by the visor
func PrintTransports(cmdFlags *pflag.FlagSet, tps ...*visor.TransportSummary) {
	sortTransports(tps...)

	var b bytes.Buffer
	w := tabwriter.NewWriter(&b, 0, 0, 5, ' ', tabwriter.TabIndent)
	_, err := fmt.Fprintln(w, "type\tid\tremote_pk\tmode\tlabel")
	internal.Catch(cmdFlags, err)

	type outputTP struct {
		Type   network.Type    `json:"type"`
		ID     uuid.UUID       `json:"id"`
		Remote cipher.PubKey   `json:"remote_pk"`
		TpMode string          `json:"mode"`
		Label  transport.Label `json:"label"`
	}

	var outputTPS []outputTP

	for _, tp := range tps {
		tpMode := "regular"
		if tp.IsSetup {
			tpMode = "setup"
		}
		tp.Log = nil
		oTP := outputTP{
			Type:   tp.Type,
			ID:     tp.ID,
			Remote: tp.Remote,
			TpMode: tpMode,
			Label:  tp.Label,
		}
		outputTPS = append(outputTPS, oTP)

		_, err = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", tp.Type, tp.ID, tp.Remote, tpMode, tp.Label)
		internal.Catch(cmdFlags, err)
	}
	internal.Catch(cmdFlags, w.Flush())
	internal.PrintOutput(cmdFlags, outputTPS, b.String())
}

func sortTransports(tps ...*visor.TransportSummary) {
	sort.Slice(tps, func(i, j int) bool {
		return tps[i].ID.String() < tps[j].ID.String()
	})
}

var discTpCmd = &cobra.Command{
	Use:   "disc (--id=<transport-id> | --pk=<edge-public-key>)",
	Short: "Discover transport(s) by ID or public key",
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
	Run: func(cmd *cobra.Command, _ []string) {

		if rc := clirpc.Client(); tpPK.Null() {
			entry, err := rc.DiscoverTransportByID(uuid.UUID(tpID))
			internal.Catch(cmd.Flags(), err)
			PrintTransportEntries(cmd.Flags(), entry)
		} else {
			entries, err := rc.DiscoverTransportsByPK(tpPK)
			internal.Catch(cmd.Flags(), err)
			PrintTransportEntries(cmd.Flags(), entries...)
		}
	},
}

// PrintTransportEntries prints the transport entries
func PrintTransportEntries(cmdFlags *pflag.FlagSet, entries ...*transport.Entry) {

	var b bytes.Buffer
	w := tabwriter.NewWriter(&b, 0, 0, 5, ' ', tabwriter.TabIndent)
	_, err := fmt.Fprintln(w, "id\ttype\tedge1\tedge2")
	internal.Catch(cmdFlags, err)

	type outputEntry struct {
		ID    uuid.UUID     `json:"id"`
		Type  network.Type  `json:"type"`
		Edge1 cipher.PubKey `json:"edge1"`
		Edge2 cipher.PubKey `json:"edge2"`
	}

	var outputEntries []outputEntry
	for _, e := range entries {
		_, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			e.ID, e.Type, e.Edges[0], e.Edges[1])
		internal.Catch(cmdFlags, err)
		oEntry := outputEntry{
			ID:    e.ID,
			Type:  e.Type,
			Edge1: e.Edges[0],
			Edge2: e.Edges[1],
		}
		outputEntries = append(outputEntries, oEntry)
	}
	internal.Catch(cmdFlags, w.Flush())
	internal.PrintOutput(cmdFlags, outputEntries, b.String())
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
