// Package cliskychat cmd/skywire-cli/commands/skychat/voice_ringback.go c4-vis-cli
//
// Outbound-call progress and the ringback tone. `voice dialing` shows how the
// calls this visor is placing are going — connecting, ringing, or why one did
// not connect. `voice ringback` manages the tone people hear while THIS visor
// rings: set it from an audio file, show it, or clear it back to the plain
// ring.
package cliskychat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

var (
	ringbackType string
	ringbackOut  string
)

func init() {
	voiceRingbackSetCmd.Flags().StringVar(&ringbackType, "type", "", "audio type of the file (default: from its extension)")
	voiceRingbackShowCmd.Flags().StringVarP(&ringbackOut, "out", "o", "", "also save the tone to this file")
	voiceRingbackCmd.AddCommand(voiceRingbackSetCmd, voiceRingbackClearCmd, voiceRingbackShowCmd)
	voiceCmd.AddCommand(voiceDialingCmd, voiceRingbackCmd)
}

var voiceDialingCmd = &cobra.Command{
	Use:   "dialing",
	Short: "List outbound calls not yet answered, and how each is going",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			cliutil.PrintFatalError(cmd.Flags(), err)
		}
		calls, err := rpcClient.VoiceDialing()
		if err != nil {
			cliutil.PrintFatalError(cmd.Flags(), err)
		}
		if len(calls) == 0 {
			cliutil.PrintOutput(cmd.Flags(), calls, "no outbound calls\n")
			return
		}
		var b strings.Builder
		b.WriteString("outbound calls:\n")
		for _, c := range calls {
			state := c.State
			if state == "" {
				state = "calling" // a visor that predates dial progress
			}
			fmt.Fprintf(&b, "  %s to %s: %s", c.CallID, c.Peer, state)
			if c.Ringback {
				b.WriteString(" (their ringback tone is playing)")
			}
			if c.Reason != "" && c.Reason != state {
				fmt.Fprintf(&b, " — %s", c.Reason)
			}
			b.WriteString("\n")
		}
		cliutil.PrintOutput(cmd.Flags(), calls, b.String())
	},
}

var voiceRingbackCmd = &cobra.Command{
	Use:   "ringback",
	Short: "The tone callers hear while this visor rings",
}

// ringbackTypes maps a file extension to the audio type a tone is sent as.
// Kept here rather than asking the OS, whose tables disagree about audio.
var ringbackTypes = map[string]string{
	".mp3":  "audio/mpeg",
	".ogg":  "audio/ogg",
	".oga":  "audio/ogg",
	".opus": "audio/ogg",
	".weba": "audio/webm",
	".webm": "audio/webm",
	".wav":  "audio/wav",
	".m4a":  "audio/mp4",
	".aac":  "audio/aac",
	".flac": "audio/flac",
}

var voiceRingbackSetCmd = &cobra.Command{
	Use:   "set <audio-file>",
	Short: "Set the ringback tone from an audio file (mp3, ogg/opus, webm, wav, m4a, aac, flac; up to 1 MB)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		data, err := os.ReadFile(args[0])
		if err != nil {
			cliutil.PrintFatalError(cmd.Flags(), err)
		}
		kind := ringbackType
		if kind == "" {
			kind = ringbackTypes[strings.ToLower(filepath.Ext(args[0]))]
		}
		if kind == "" {
			cliutil.PrintFatalError(cmd.Flags(), fmt.Errorf("cannot tell the audio type of %s — pass --type", args[0]))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			cliutil.PrintFatalError(cmd.Flags(), err)
		}
		if err := rpcClient.VoiceSetRingback(visorapi.VoiceRingback{Data: data, Mime: kind}); err != nil {
			cliutil.PrintFatalError(cmd.Flags(), err)
		}
		cliutil.PrintOutput(cmd.Flags(), map[string]any{"size": len(data), "type": kind},
			fmt.Sprintf("ringback tone set: %d bytes of %s\n", len(data), kind))
	},
}

var voiceRingbackClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Clear the ringback tone — callers hear the plain ring again",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			cliutil.PrintFatalError(cmd.Flags(), err)
		}
		if err := rpcClient.VoiceSetRingback(visorapi.VoiceRingback{}); err != nil {
			cliutil.PrintFatalError(cmd.Flags(), err)
		}
		cliutil.PrintOutput(cmd.Flags(), "cleared", "ringback tone cleared\n")
	},
}

var voiceRingbackShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the ringback tone this visor plays to its callers",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			cliutil.PrintFatalError(cmd.Flags(), err)
		}
		tone, err := rpcClient.VoiceRingback()
		if err != nil {
			cliutil.PrintFatalError(cmd.Flags(), err)
		}
		if len(tone.Data) == 0 {
			cliutil.PrintOutput(cmd.Flags(), map[string]any{"size": 0}, "no ringback tone — callers hear the plain ring\n")
			return
		}
		if ringbackOut != "" {
			if err := os.WriteFile(ringbackOut, tone.Data, 0o600); err != nil {
				cliutil.PrintFatalError(cmd.Flags(), err)
			}
		}
		cliutil.PrintOutput(cmd.Flags(), map[string]any{"size": len(tone.Data), "type": tone.Mime},
			fmt.Sprintf("ringback tone: %d bytes of %s\n", len(tone.Data), tone.Mime))
	},
}
