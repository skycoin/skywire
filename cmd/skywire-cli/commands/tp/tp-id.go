// Package clitp cmd/skywire-cli/commands/tp/tp-id.go
package clitp

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
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

Valid transport types: dmsg, stcp, stcpr, sudph (default: dmsg)`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		switch idTpType {
		case "dmsg", "stcp", "stcpr", "sudph":
		default:
			internal.Catch(cmd.Flags(), fmt.Errorf("invalid transport type %q (valid: dmsg, stcp, stcpr, sudph)", idTpType))
		}

		var pk1, pk2 cipher.PubKey
		if err := pk1.Set(args[0]); err != nil {
			internal.Catch(cmd.Flags(), fmt.Errorf("invalid pk1: %w", err))
		}
		if err := pk2.Set(args[1]); err != nil {
			internal.Catch(cmd.Flags(), fmt.Errorf("invalid pk2: %w", err))
		}

		id := transport.MakeTransportID(pk1, pk2, tptypes.Type(idTpType))

		// Honor --json if set (PersistentFlag on RootCmd).
		jsonFlag, _ := cmd.Flags().GetBool("json") //nolint:errcheck,gosec
		if jsonFlag {
			out, _ := json.Marshal(map[string]string{ //nolint:errcheck,gosec
				"output": id.String(),
			})
			fmt.Println(string(out))
			return
		}
		fmt.Println(id.String())
	},
}
