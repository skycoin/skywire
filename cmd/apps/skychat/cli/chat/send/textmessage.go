package clichatsend

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/inputports"
)

var textMessageCmd = &cobra.Command{
	Use:   "text <vpk> <spk> <rpk> <msg>",
	Short: "Send text message",
	Args:  cobra.MinimumNArgs(4),
	Run: func(cmd *cobra.Command, args []string) {

		applog := logging.MustGetLogger("chat:clichatsend")

		vpk, err := ParsePK(cmd.Flags(), "vpk", args[0])
		if err != nil {
			applog.Errorln(err)
		}
		spk, err := ParsePK(cmd.Flags(), "spk", args[1])
		if err != nil {
			applog.Errorln(err)
		}
		rpk, err := ParsePK(cmd.Flags(), "rpk", args[2])
		if err != nil {
			applog.Errorln(err)
		}

		msg := args[3]

		applog.Debugln("SendTextMessage via cli (cli)")
		applog.Debugln(msg)
		applog.Debugf("to v: %s s: %s, r %s\n", vpk.Hex(), spk.Hex(), rpk.Hex())

		err = inputports.InputportsServices.RPCClient.SendTextMessage(*vpk, *spk, *rpk, msg)
		if err != nil {
			applog.Errorln(err)
		}
	},
}

// ParsePK parses a public key
func ParsePK(_ *pflag.FlagSet, _, v string) (*cipher.PubKey, error) {
	var pk cipher.PubKey
	err := pk.Set(v)
	if err != nil {
		return nil, fmt.Errorf("failed to parse <%s>: %v", v, err)
	}
	return &pk, nil
}
