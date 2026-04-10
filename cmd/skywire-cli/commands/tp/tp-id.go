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

func init() {
	tpCmd.AddCommand(idTpCmd)
}

var idTpCmd = &cobra.Command{
	Use:   "id <type> <pk1> <pk2>",
	Short: "Compute the deterministic transport ID for a given type + PK pair",
	Long: `Compute the deterministic transport ID (UUID) for a given transport type
and pair of public keys. This is purely a local computation — no RPC, no
discovery query — and mirrors transport.MakeTransportID().

The returned ID is independent of PK order: id(T, A, B) == id(T, B, A).

Valid transport types: dmsg, stcp, stcpr, sudph`,
	Args: cobra.ExactArgs(3),
	Run: func(cmd *cobra.Command, args []string) {
		tpType := tptypes.Type(args[0])
		var pk1, pk2 cipher.PubKey
		if err := pk1.Set(args[1]); err != nil {
			internal.Catch(cmd.Flags(), fmt.Errorf("invalid pk1: %w", err))
		}
		if err := pk2.Set(args[2]); err != nil {
			internal.Catch(cmd.Flags(), fmt.Errorf("invalid pk2: %w", err))
		}

		id := transport.MakeTransportID(pk1, pk2, tpType)

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
