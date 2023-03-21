// Package mgmt cmd/skywire-cli/commands/mgmt/api.go
package mgmt

import (
	"bytes"
	"fmt"
	"os"
	"sort"
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
	"github.com/skycoin/skywire/pkg/transport/setup"
)

var (
	managerPK string
)

func init() {
	apiCmd.PersistentFlags().StringVarP(&managerPK, "pk", "k", "", "pk of the remote Manager Server")
	apiCmd.AddCommand(
		addTpCmd,
	)
	RootCmd.AddCommand(apiCmd)
}

// apiCmd contains commands to use API provided by Manager Server
var apiCmd = &cobra.Command{
	Use:   "api",
	Short: "Use Manager Server's API",
}

var (
	remotePK      string
	transportType string
	timeout       time.Duration
)

func init() {
	addTpCmd.Flags().StringVarP(&remotePK, "rpk", "r", "", "remote public key.")
	addTpCmd.Flags().StringVarP(&transportType, "type", "t", "", "type of transport to add.")
	addTpCmd.Flags().DurationVarP(&timeout, "timeout", "o", 0, "if specified, sets an operation timeout")
}

// addTpCmd add's a tp to Manager Server
var addTpCmd = &cobra.Command{
	Use:   "add-tp -r",
	Short: "Add a transport",
	Long:  "\n    Add a transport to the Manager Server\n    \n    If the transport type is unspecified,\n    the visor will attempt to establish a transport\n    in the following order: skywire-tcp, stcpr, sudph, dmsg",
	Args:  cobra.MinimumNArgs(0),
	Run: func(cmd *cobra.Command, args []string) {

		isJSON, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck

		var rpk cipher.PubKey
		var mpk cipher.PubKey

		internal.Catch(cmd.Flags(), rpk.Set(remotePK))
		internal.Catch(cmd.Flags(), mpk.Set(managerPK))

		var tp *setup.TransportSummary

		if transportType != "" {
			rpcClient, err := clirpc.Client(cmd.Flags())
			if err != nil {
				os.Exit(1)
			}
			tp, err = rpcClient.AddMgmtTransport(mpk, rpk, transportType, timeout)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to establish %v transport: %v", transportType, err))
			}
			if !isJSON {
				logger.Infof("Established %v transport from %v to %v", transportType, mpk, rpk)
			}
		} else {
			transportTypes := []network.Type{
				network.STCPR,
				network.SUDPH,
				network.DMSG,
				network.STCP,
			}
			for _, transportType := range transportTypes {
				rpcClient, err := clirpc.Client(cmd.Flags())
				if err != nil {
					os.Exit(1)
				}
				tp, err = rpcClient.AddMgmtTransport(mpk, rpk, string(transportType), timeout)
				if err == nil {
					if !isJSON {
						logger.Infof("Established %v transport from %v to %v", transportType, mpk, rpk)
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

// PrintTransports prints transports used by the visor
func PrintTransports(cmdFlags *pflag.FlagSet, tps ...*setup.TransportSummary) {
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

func sortTransports(tps ...*setup.TransportSummary) {
	sort.Slice(tps, func(i, j int) bool {
		return tps[i].ID.String() < tps[j].ID.String()
	})
}

var (
	tpID string
)

func init() {
	rmTpCmd.Flags().StringVarP(&tpID, "id", "i", "", "remove transport of given ID")
}

var rmTpCmd = &cobra.Command{
	Use:                   "rm -i",
	Short:                 "Remove transport(s) by id",
	Long:                  "\n    Remove transport(s) by id from the Manager Server",
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {

		var mpk cipher.PubKey
		internal.Catch(cmd.Flags(), mpk.Set(managerPK))

		tID := internal.ParseUUID(cmd.Flags(), "transport-id", tpID)
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.RemoveMgmtTransport(mpk, tID))
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
	},
}

var (
	tpPK string
)

func init() {
	discTpCmd.Flags().StringVarP(&tpID, "id", "i", "", "obtain transport of given ID")
	discTpCmd.Flags().StringVarP(&tpPK, "pk", "p", "", "obtain transports by public key")
}

var discTpCmd = &cobra.Command{
	Use:                   "disc",
	Short:                 "Discover remote transport(s)",
	Long:                  "\n    Discover remote transport(s) on the Manager Server.",
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, _ []string) {
		var mpk cipher.PubKey
		var tppk cipher.PubKey
		var tpid transportID
		internal.Catch(cmd.Flags(), mpk.Set(managerPK))
		internal.Catch(cmd.Flags(), tpid.Set(tpID))
		internal.Catch(cmd.Flags(), tppk.Set(tpPK))

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		tps, err := rpcClient.GetMgmtTransports(mpk)
		internal.Catch(cmd.Flags(), err)
		if !tppk.Null() {
			var tpSums []*setup.TransportSummary
			for _, tp := range tps {
				if tp.Remote == tppk {
					tpSums = append(tpSums, tp)
				}
			}
			PrintTransports(cmd.Flags(), tpSums...)
		} else if tpid.String() != "" {
			var tpSum *setup.TransportSummary
			for _, tp := range tps {
				if tp.ID.String() == tpid.String() {
					tpSum = tp
				}
			}
			PrintTransports(cmd.Flags(), tpSum)
		} else {
			PrintTransports(cmd.Flags(), tps...)
		}
	},
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
