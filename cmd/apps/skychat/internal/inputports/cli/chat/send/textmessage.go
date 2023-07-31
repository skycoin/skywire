package clichatsend

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/inputports"
)

var textMessageCmd = &cobra.Command{
	Use:   "text <vpk> <spk> <rpk> <msg>",
	Short: "Send text message",
	Args:  cobra.MinimumNArgs(4),
	Run: func(cmd *cobra.Command, args []string) {
		vpk := ParsePK(cmd.Flags(), "vpk", args[0])
		spk := ParsePK(cmd.Flags(), "spk", args[1])
		rpk := ParsePK(cmd.Flags(), "rpk", args[2])
		msg := args[3]

		fmt.Println("SendTextMessage via cli (cli)")
		fmt.Println(msg)
		fmt.Printf("to v: %s s: %s, r %s\n", vpk.Hex(), spk.Hex(), rpk.Hex())

		err := inputports.InputportsServices.RPCClient.SendTextMessage(vpk, spk, rpk, msg)
		if err != nil {
			fmt.Println(err)
		}
	},
}

// ParsePK parses a public key
func ParsePK(_ *pflag.FlagSet, _, v string) cipher.PubKey {
	var pk cipher.PubKey
	err := pk.Set(v)
	if err != nil {
		//PrintFatalError(cmdFlags, fmt.Errorf("failed to parse <%s>: %v", name, err))
		fmt.Printf("Error")
	}
	return pk
}
