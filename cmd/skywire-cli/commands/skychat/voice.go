// Package cliskychat cmd/skywire-cli/commands/skychat/voice.go c4-vis-cli
package cliskychat

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cipher"
)

func init() {
	voiceCmd.AddCommand(voiceCallCmd, voiceHangupCmd, voiceListCmd)
	RootCmd.AddCommand(voiceCmd)
}

var voiceCmd = &cobra.Command{
	Use:   "voice",
	Short: "1:1 voice calls (dmsg/skynet signaling, RTP media on the mesh)",
}

var voiceCallCmd = &cobra.Command{
	Use:   "call <peer-pk>",
	Short: "Place a voice call to a peer",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var pk cipher.PubKey
		if err := pk.Set(args[0]); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("bad peer pk: %w", err))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		id, err := rpcClient.VoiceCall(pk)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), id, fmt.Sprintf("call started: %s\n", id))
	},
}

var voiceHangupCmd = &cobra.Command{
	Use:   "hangup <call-id>",
	Short: "End an active call",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if err := rpcClient.VoiceHangup(args[0]); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), args[0], fmt.Sprintf("hung up: %s\n", args[0]))
	},
}

var voiceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List active calls",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		ids, err := rpcClient.VoiceActive()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		out := "no active calls\n"
		if len(ids) > 0 {
			out = "active calls:\n  " + strings.Join(ids, "\n  ") + "\n"
		}
		internal.PrintOutput(cmd.Flags(), ids, out)
	},
}
