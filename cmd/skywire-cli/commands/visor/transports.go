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
	filterPubKeys []string
	showLogs      bool
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
	Use:   "type",
	Short: "Transport types used by the local visor",
	Long: "\n	Transport types used by the local visor",
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, _ []string) {
		types, err := clirpc.Client(cmd.Flags()).TransportTypes()
		internal.Catch(cmd.Flags(), err)
		internal.PrintOutput(cmd.Flags(), types, fmt.Sprintln(strings.Join(types, "\n")))
	},
}

func init() {
	lsTpCmd.Flags().StringSliceVarP(&filterTypes, "types", "t", filterTypes, "show transport(s) type(s) comma-separated")
	lsTpCmd.Flags().StringSliceVarP(&filterPubKeys, "pks", "p", filterPubKeys, "show transport(s) for public key(s) comma-separated")
	lsTpCmd.Flags().BoolVarP(&showLogs, "logs", "l", true, "show transport logs")
}

var lsTpCmd = &cobra.Command{
	Use:   "ls",
	Short: "Available transports",
	Long: "\n	Available transports\n\n	displays transports of the local visor",
	Run: func(cmd *cobra.Command, _ []string) {
		var pks cipher.PubKeys

		internal.Catch(cmd.Flags(), pks.Set(strings.Join(filterPubKeys, ",")))
		transports, err := clirpc.Client(cmd.Flags()).Transports(filterTypes, pks, showLogs)
		internal.Catch(cmd.Flags(), err)
		PrintTransports(cmd.Flags(), transports...)
	},
}

func init() {
	idCmd.Flags().StringVarP(&tpID, "id", "i", "", "transport ID")
}

var idCmd = &cobra.Command{
	Use:   "id (-i) <transport-id>",
	Short: "Transport summary by id",
	Long: "\n	Transport summary by id",
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {

		tpid := internal.ParseUUID(cmd.Flags(), "transport-id", args[0])
		tp, err := clirpc.Client(cmd.Flags()).Transport(tpid)
		internal.Catch(cmd.Flags(), err)
		PrintTransports(cmd.Flags(), tp)
	},
}

var (
	transportType string
	timeout       time.Duration
	rpk           string
)

func init() {
	addTpCmd.Flags().StringVarP(&rpk, "rpk", "r", "", "remote public key.")
	addTpCmd.Flags().StringVarP(&transportType, "type", "t", "", "type of transport to add.")
	addTpCmd.Flags().DurationVarP(&timeout, "timeout", "o", 0, "if specified, sets an operation timeout")
}

var addTpCmd = &cobra.Command{
	Use:   "add (-p) <remote-public-key>",
	Short: "Add a transport",
	Long: "\n	Add a transport\n	\n	If the transport type is unspecified,\n	the visor will attempt to establish a transport\n	in the following order: skywire-tcp, stcpr, sudph, dmsg",
	Args:                  cobra.MinimumNArgs(1),
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		isJSON, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck

		var pk cipher.PubKey

		if rpk == "" {
			pk = internal.ParsePK(cmd.Flags(), "remote-public-key", args[0])
		} else {
			internal.Catch(cmd.Flags(), pk.Set(rpk))
		}

		var tp *visor.TransportSummary
		var err error

		if transportType != "" {
			tp, err = clirpc.Client(cmd.Flags()).AddTransport(pk, transportType, timeout)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to establish %v transport: %v", transportType, err))
			}
			if !isJSON {
				logger.Infof("Established %v transport to %v", transportType, pk)
			}
		} else {
			transportTypes := []network.Type{
				network.STCPR,
				network.SUDPH,
				network.DMSG,
				network.STCP,
			}
			for _, transportType := range transportTypes {
				tp, err = clirpc.Client(cmd.Flags()).AddTransport(pk, string(transportType), timeout)
				if err == nil {
					if !isJSON {
						logger.Infof("Established %v transport to %v", transportType, pk)
					}
					break
				}
				if !isJSON {
					logger.WithError(err).Warnf("Failed to establish %v transport", transportType)
				}
			}
		}
		PrintTransports(cmd.Flags(), tp)
	},
}

func init() {
	rmTpCmd.Flags().BoolVarP(&removeAll, "all", "a", false, "remove all transports")
	rmTpCmd.Flags().StringVarP(&tpID, "id", "i", "", "remove transport of given ID")
}

var rmTpCmd = &cobra.Command{
	Use:   "rm ( -a || -i ) <transport-id>",
	Short: "Remove transport(s) by id",
	Long: "\n	Remove transport(s) by id",
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		//TODO
		//if removeAll {
		//	var pks cipher.PubKeys
		//	internal.Catch(cmd.Flags(), pks.Set(strings.Join(filterPubKeys, ",")))
		//	tID, err := clirpc.Client(cmd.Flags()).Transports(filterTypes, pks, showLogs)
		//	internal.Catch(cmd.Flags(), err)
		//	internal.Catch(cmd.Flags(), clirpc.Client(cmd.Flags()).RemoveTransport(tID))
		//} else {
		if args[0] != "" {
			tpID = args[0]
		}
		tID := internal.ParseUUID(cmd.Flags(), "transport-id", tpID)
		internal.Catch(cmd.Flags(), clirpc.Client(cmd.Flags()).RemoveTransport(tID))
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
		//}
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

var (
	tpID string
	tpPK string
)

func init() {
	discTpCmd.Flags().StringVarP(&tpID, "id", "i", "", "obtain transport of given ID")
	discTpCmd.Flags().StringVarP(&tpPK, "pk", "p", "", "obtain transports by public key")
}

var discTpCmd = &cobra.Command{
	Use:   "disc (--id=<transport-id> || --pk=<edge-public-key>)",
	Short: "Discover remote transport(s)",
	Long: "\n	Discover remote transport(s) by ID or public key",
	DisableFlagsInUseLine: true,
	Args: func(_ *cobra.Command, _ []string) error {
		if tpID == "" && tpPK == "" {
			return errors.New("must specify either transport id or public key")
		}
		if tpID != "" && tpPK != "" {
			return errors.New("cannot specify both transport id and public key")
		}
		return nil
	},
	Run: func(cmd *cobra.Command, _ []string) {
		var tppk cipher.PubKey
		var tpid transportID
		internal.Catch(cmd.Flags(), tpid.Set(tpID))
		internal.Catch(cmd.Flags(), tppk.Set(tpPK))
		if rc := clirpc.Client(cmd.Flags()); tppk.Null() {
			entry, err := rc.DiscoverTransportByID(uuid.UUID(tpid))
			internal.Catch(cmd.Flags(), err)
			PrintTransportEntries(cmd.Flags(), entry)
		} else {
			entries, err := rc.DiscoverTransportsByPK(tppk)
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
