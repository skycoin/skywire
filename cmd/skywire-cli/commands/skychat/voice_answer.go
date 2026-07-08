// Package cliskychat cmd/skywire-cli/commands/skychat/voice_answer.go c4-vis-cli
//
// Explicit-answer controls for inbound voice calls. When a visor runs with real
// audio (SKYWIRE_VOICE_AUDIO=mic|monitor) incoming calls RING instead of
// auto-answering, so the microphone is never streamed without an explicit
// answer. `voice incoming` lists ringing calls; `voice answer`/`voice decline`
// act on one by id.
package cliskychat

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
)

func init() {
	voiceCmd.AddCommand(voiceIncomingCmd, voiceAnswerCmd, voiceDeclineCmd)
}

var voiceIncomingCmd = &cobra.Command{
	Use:   "incoming",
	Short: "List ringing inbound calls awaiting an answer",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			cliutil.PrintFatalError(cmd.Flags(), err)
		}
		ids, err := rpcClient.VoiceIncoming()
		if err != nil {
			cliutil.PrintFatalError(cmd.Flags(), err)
		}
		out := "no ringing calls\n"
		if len(ids) > 0 {
			out = "ringing calls:\n  " + strings.Join(ids, "\n  ") + "\n"
		}
		cliutil.PrintOutput(cmd.Flags(), ids, out)
	},
}

var voiceAnswerCmd = &cobra.Command{
	Use:   "answer <call-id>",
	Short: "Accept a ringing inbound call",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			cliutil.PrintFatalError(cmd.Flags(), err)
		}
		if err := rpcClient.VoiceAnswer(args[0]); err != nil {
			cliutil.PrintFatalError(cmd.Flags(), err)
		}
		cliutil.PrintOutput(cmd.Flags(), args[0], fmt.Sprintf("answered: %s\n", args[0]))
	},
}

var voiceDeclineCmd = &cobra.Command{
	Use:   "decline <call-id>",
	Short: "Reject a ringing inbound call",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			cliutil.PrintFatalError(cmd.Flags(), err)
		}
		if err := rpcClient.VoiceDecline(args[0]); err != nil {
			cliutil.PrintFatalError(cmd.Flags(), err)
		}
		cliutil.PrintOutput(cmd.Flags(), args[0], fmt.Sprintf("declined: %s\n", args[0]))
	},
}
