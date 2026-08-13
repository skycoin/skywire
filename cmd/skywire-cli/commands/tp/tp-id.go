// Package clitp cmd/skywire-cli/commands/tp/tp-id.go c4-vis-cli
package clitp

import (
	"fmt"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cliout"
	"github.com/skycoin/skywire/pkg/cliout/clitp"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

var idTpType string

func init() {
	idTpCmd.Flags().StringVarP(&idTpType, "type", "t", "dmsg", "transport type (dmsg, stcp, stcpr, sudph)")
	tpCmd.AddCommand(idTpCmd)
}

var idTpCmd = &cobra.Command{
	Use:   "id <pk1> <pk2>",
	Short: "Compute the deterministic transport ID for a given PK pair and type",
	Long: `Compute the deterministic transport ID (UUID) for a given transport type
and pair of public keys. This is purely a local computation — no RPC, no
discovery query — and mirrors transport.MakeTransportID().

The returned ID is independent of PK order: id(T, A, B) == id(T, B, A).

Valid transport types: stcpr, squicr, sudph, stcp, webrtc, swsr, swtr, dmsg (default: dmsg)`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		if !tptypes.Valid(tptypes.Type(idTpType)) {
			internal.Catch(cmd.Flags(), fmt.Errorf("invalid transport type %q (valid: %v)", idTpType, tptypes.Known()))
		}

		var pk1, pk2 cipher.PubKey
		if err := pk1.Set(args[0]); err != nil {
			internal.Catch(cmd.Flags(), fmt.Errorf("invalid pk1: %w", err))
		}
		if err := pk2.Set(args[1]); err != nil {
			internal.Catch(cmd.Flags(), fmt.Errorf("invalid pk2: %w", err))
		}

		id := transport.MakeTransportID(pk1, pk2, tptypes.Type(idTpType))

		// Was {"output": "<uuid>"} — the last of the envelope that forced
		// every consumer through jq '.output'. The field has a name now.
		internal.Catch(cmd.Flags(), cliout.Print(cmd, clitp.ID{ID: id.String()}))
	},
}
