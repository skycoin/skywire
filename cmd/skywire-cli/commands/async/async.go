// Package cliasync cmd/skywire-cli/commands/async/async.go c4-vis-cli
//
// `skywire cli async`: dmsg-async, store and forward for offline keys.
package cliasync

import (
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgasync"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

var (
	sendPort uint16
	sendRef  string
	takePort []uint
)

func init() {
	sendCmd.Flags().Uint16VarP(&sendPort, "port", "p", 0, "the recipient's dmsg port the message is for (required)")
	sendCmd.Flags().StringVar(&sendRef, "ref", "", "32 hex digits naming the message, so a retry is stored once")
	takeCmd.Flags().UintSliceVarP(&takePort, "port", "p", nil, "ports to take messages for (required, repeatable)")
	RootCmd.AddCommand(statusCmd, sendCmd, takeCmd, syncCmd)
}

// RootCmd is `skywire cli async`, which shows the status when bare.
var RootCmd = &cobra.Command{
	Use:   "async",
	Short: "Store and forward for offline keys (dmsg-async)",
	Long: "dmsg-async holds small messages for a key that is offline until it comes\n" +
		"back. A visor's messages wait on its inbox hosts, its hypervisors unless\n" +
		"configured, and a hypervisor holds messages for the visors it manages.\n" +
		"Configure it in the dmsg_async section of the visor config. Bare `async`\n" +
		"shows the status.",
	Example: `  skywire cli async
  skywire cli async send 02a4... -p 1 "are you there?"
  skywire cli async take -p 1
  skywire cli async sync`,
	Run: func(cmd *cobra.Command, _ []string) { status(cmd) },
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the inbox host and this visor's inbox",
	Run:   func(cmd *cobra.Command, _ []string) { status(cmd) },
}

var sendCmd = &cobra.Command{
	Use:   "send <pk> <message>",
	Short: "Leave a message for an offline key on its inbox hosts",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var to cipher.PubKey
		if err := to.Set(args[0]); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid public key: %w", err))
		}
		if sendPort == 0 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--port is required"))
		}
		res, err := api(cmd).DmsgAsyncSend(visorapi.DmsgAsyncSendRequest{To: to, Port: sendPort, Payload: []byte(args[1]), Ref: sendRef})
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), res, fmt.Sprintf("left on %s (ref %s)\n", res.Host, res.Ref))
	},
}

var takeCmd = &cobra.Command{
	Use:   "take",
	Short: "Fetch and remove what waits for this visor on the given ports",
	Long: "Fetch and remove what waits for this visor on the given ports. A port the\n" +
		"visor itself handles is refused, so this does not take its messages.",
	Run: func(cmd *cobra.Command, _ []string) {
		if len(takePort) == 0 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--port is required"))
		}
		ports := make([]uint16, 0, len(takePort))
		for _, p := range takePort {
			if p == 0 || p > 65535 {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid port %d", p))
			}
			ports = append(ports, uint16(p))
		}
		msgs, err := api(cmd).DmsgAsyncTake(ports)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if msgs == nil {
			msgs = []dmsgasync.Message{}
		}
		var b strings.Builder
		if len(msgs) == 0 {
			b.WriteString("Nothing waiting.\n")
		}
		for _, m := range msgs {
			fmt.Fprintf(&b, "%s from %s port %d via %s\n%s\n", //nolint:errcheck
				m.Deposited.Local().Format(time.DateTime), m.From, m.Port, m.Host, m.Payload)
		}
		internal.PrintOutput(cmd.Flags(), msgs, b.String())
	},
}

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Register with the inbox hosts, publish the record and fetch now",
	Run: func(cmd *cobra.Command, _ []string) {
		if err := api(cmd).DmsgAsyncSync(); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		status(cmd)
	},
}

func status(cmd *cobra.Command) {
	st, err := api(cmd).DmsgAsyncStatus()
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	if h := st.Host; h != nil {
		fmt.Fprintf(tw, "Host\t%s store, %d messages, %d bytes, %d keys registered\n", //nolint:errcheck
			h.Store, h.Stats.Messages, h.Stats.Bytes, len(h.Registered))
		for _, pk := range h.Registered {
			fmt.Fprintf(tw, "  holds for\t%s\n", pk) //nolint:errcheck
		}
	} else {
		fmt.Fprintln(tw, "Host\toff") //nolint:errcheck
	}
	in := st.Inbox
	if !in.Enabled {
		fmt.Fprintln(tw, "Inbox\toff") //nolint:errcheck
	} else {
		pub := "yes"
		if !in.Published {
			pub = "no"
			if in.PublishErr != "" {
				pub = "no: " + in.PublishErr
			}
		}
		fmt.Fprintf(tw, "Inbox\tpublished %s, fetched %d\n", pub, in.Fetched) //nolint:errcheck
		if in.Record != nil {
			fmt.Fprintf(tw, "  record\tseq %d, expires %s\n", in.Record.Seq, in.Record.Expires.Local().Format(time.DateTime)) //nolint:errcheck
		}
		for _, h := range in.Hosts {
			state := "registered " + h.Registered.Local().Format(time.DateTime)
			if h.Registered.IsZero() {
				state = "not registered"
			}
			if h.Error != "" {
				state += ": " + h.Error
			}
			fmt.Fprintf(tw, "  host\t%s %s\n", h.PK, state) //nolint:errcheck
		}
		if len(in.Handlers) > 0 {
			ps := make([]string, len(in.Handlers))
			for i, p := range in.Handlers {
				ps[i] = strconv.Itoa(int(p))
			}
			fmt.Fprintf(tw, "  handled ports\t%s\n", strings.Join(ps, ", ")) //nolint:errcheck
		}
	}
	tw.Flush() //nolint:errcheck,gosec
	internal.PrintOutput(cmd.Flags(), st, b.String())
}

func api(cmd *cobra.Command) visorapi.API {
	rpc, err := clirpc.Client(cmd.Flags())
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	return rpc
}
