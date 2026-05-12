// Package cliskychat cmd/skywire-cli/commands/skychat/group.go:
// operator-facing CLI for D1 group chat. Thin wrappers over the
// visor's GroupX RPC methods.
//
// Subcommands:
//
//	skywire cli skychat group create  --name <name> [--mode public|private] [--member <pk> ...]
//	skywire cli skychat group list
//	skywire cli skychat group info    <group-id>
//	skywire cli skychat group invite  <group-id>           # re-emits the invite link
//	skywire cli skychat group join    <invite-link>
//	skywire cli skychat group add     <group-id> <pk>      # owner: extend allowlist
//	skywire cli skychat group send    <group-id> <text>    # owner only in v1
//	skywire cli skychat group listen  [--since RFC3339]    # poll inbox, stream
//	skywire cli skychat group leave   <group-id>           # member side
//	skywire cli skychat group delete  <group-id>           # owner side
//
// All commands talk to the LOCAL visor via clirpc.Client; the visor
// owns the group.Manager. No DMSG dial happens in the CLI itself.
package cliskychat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	skychatgroup "github.com/skycoin/skywire/cmd/apps/skychat/group"
	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	groupCreateName    string
	groupCreateMode    string
	groupCreateMembers []string

	groupListenSince string
	groupListenPoll  time.Duration
)

func init() {
	groupCreateCmd.Flags().StringVarP(&groupCreateName, "name", "n", "", "human-readable group name (required)")
	groupCreateCmd.Flags().StringVarP(&groupCreateMode, "mode", "m", "public", "public | private — private encrypts feed messages with an AES key shipped in the invite link")
	groupCreateCmd.Flags().StringSliceVar(&groupCreateMembers, "member", nil, "initial member PK; repeat for many (the owner is implicit)")
	groupCreateCmd.MarkFlagRequired("name") //nolint:errcheck,gosec

	groupListenCmd.Flags().StringVar(&groupListenSince, "since", "", "RFC3339 lower bound for the first poll; empty = drain current inbox window then follow")
	groupListenCmd.Flags().DurationVar(&groupListenPoll, "interval", time.Second, "poll interval")

	groupCmd.AddCommand(
		groupCreateCmd, groupListCmd, groupInfoCmd, groupInviteCmd,
		groupJoinCmd, groupAddCmd, groupSendCmd, groupListenCmd,
		groupLeaveCmd, groupDeleteCmd,
	)
	RootCmd.AddCommand(groupCmd)
}

var groupCmd = &cobra.Command{
	Use:   "group",
	Short: "D1 owner-centric group chat over CXO feeds",
	Long: `Group chat over CXO TreeStore feeds.

Architecture (v1, D1): one member owns the group's CXO feed and
publishes messages; other members subscribe (read-only in v1; a
follow-up adds member-side relay). Owner generates an invite link
that carries the group ID, owner PK, port, and (for private groups)
the AES-GCM key.`,
}

var groupCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new group (this visor becomes the owner)",
	Run: func(cmd *cobra.Command, _ []string) {
		mode := skychatgroup.Mode(groupCreateMode)
		if !mode.IsValid() {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid --mode %q (use public or private)", groupCreateMode))
		}
		members, err := parsePKs(groupCreateMembers)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		info, link, err := rpcClient.GroupCreate(visor.GroupCreateArgs{
			Name:           groupCreateName,
			Mode:           mode,
			InitialMembers: members,
		})
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		printGroupCreated(cmd, info, link)
	},
}

func printGroupCreated(cmd *cobra.Command, info visor.GroupInfo, link string) {
	out := struct {
		Info   visor.GroupInfo `json:"info"`
		Invite string          `json:"invite"`
	}{Info: info, Invite: link}
	human := fmt.Sprintf("group created\n  id:     %s\n  name:   %s\n  mode:   %s\n  port:   %d\n  members: %d\n\ninvite link (share with members):\n  %s\n",
		info.ID, info.Name, info.Mode, info.Port, len(info.Members), link)
	internal.PrintOutput(cmd.Flags(), out, human)
}

var groupListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all groups this visor knows about",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		all, err := rpcClient.GroupList()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		var buf strings.Builder
		w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tMODE\tROLE\tSTATUS\tMEMBERS\tLAST_MESSAGE") //nolint:errcheck
		for _, g := range all {
			last := "-"
			if !g.LastMessageAt.IsZero() {
				last = g.LastMessageAt.UTC().Format(time.RFC3339)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n", //nolint:errcheck
				g.ID, g.Name, g.Mode, g.Role, g.Status, len(g.Members), last)
		}
		_ = w.Flush() //nolint:errcheck
		internal.PrintOutput(cmd.Flags(), all, buf.String())
	},
}

var groupInfoCmd = &cobra.Command{
	Use:   "info <group-id>",
	Short: "Show one group's full record",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		info, err := rpcClient.GroupGet(args[0])
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		body, _ := json.MarshalIndent(info, "", "  ") //nolint:errcheck
		internal.PrintOutput(cmd.Flags(), info, string(body)+"\n")
	},
}

var groupInviteCmd = &cobra.Command{
	Use:   "invite <group-id>",
	Short: "Re-emit the invite link for an owner-side group",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		link, err := rpcClient.GroupInvite(args[0])
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), link, link+"\n")
	},
}

var groupJoinCmd = &cobra.Command{
	Use:   "join <invite-link>",
	Short: "Join a group by invite link",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		info, err := rpcClient.GroupJoin(visor.GroupJoinArgs{Invite: args[0]})
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		human := fmt.Sprintf("joined group %q\n  id:    %s\n  owner: %s\n  mode:  %s\n",
			info.Name, info.ID, info.OwnerPK, info.Mode)
		internal.PrintOutput(cmd.Flags(), info, human)
	},
}

var groupAddCmd = &cobra.Command{
	Use:   "add <group-id> <pk>",
	Short: "Owner: extend the group allowlist by one member PK",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var pk cipher.PubKey
		if err := pk.Set(args[1]); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid pk %q: %w", args[1], err))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		info, err := rpcClient.GroupAddMember(args[0], pk)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		human := fmt.Sprintf("group %s now has %d members\n", args[0], len(info.Members))
		internal.PrintOutput(cmd.Flags(), info, human)
	},
}

var groupSendCmd = &cobra.Command{
	Use:   "send <group-id> <text>",
	Short: "Publish a message to a group (owner-only in v1)",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		id := args[0]
		text := strings.Join(args[1:], " ")
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if err := rpcClient.GroupSend(visor.GroupSendArgs{ID: id, Text: text}); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), nil, fmt.Sprintf("sent to %s\n", id))
	},
}

var groupListenCmd = &cobra.Command{
	Use:   "listen",
	Short: "Stream inbound group messages (across every joined group)",
	Long: `Poll the visor's group inbox and print messages as they
arrive. Mirrors ` + "`skywire cli skychat listen`" + ` for 1:1 chat but for
group feeds. Ctrl+C exits.

Auto-reconnects on visor restart: the underlying RPC connection
dies when the visor halts (rebuild-restart loop). The poller
catches "connection is shut down" / EOF / similar errors,
backs off (1s→30s exponential), re-creates the rpc client, and
resumes polling. Reduces the burn-the-CLI-Ctrl+C-relaunch
friction operators hit during a development cycle.`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		var since time.Time
		if groupListenSince != "" {
			since, err = time.Parse(time.RFC3339, groupListenSince)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid --since: %w", err))
			}
		}
		fmt.Println("Listening for group messages. Ctrl+C to exit.")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ticker := time.NewTicker(groupListenPoll)
		defer ticker.Stop()

		const (
			minBackoff = 1 * time.Second
			maxBackoff = 30 * time.Second
		)
		backoff := minBackoff
		// reconnected suppresses repeat error spam on the same dead
		// connection; we log the first failure, then go quiet until
		// reconnection succeeds.
		var lastErrLogged bool
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			msgs, err := rpcClient.GroupPoll(since)
			if err != nil {
				if !lastErrLogged {
					fmt.Fprintf(os.Stderr, "group poll: %v (reconnecting in %s)\n", err, backoff) //nolint:errcheck
					lastErrLogged = true
				}
				// Sleep before re-creating the RPC client so a
				// visor that's still mid-rebuild has time to
				// finish. Honor Ctrl+C while sleeping.
				t := time.NewTimer(backoff)
				select {
				case <-t.C:
				case <-ctx.Done():
					t.Stop()
					return
				}
				// ClientQuiet (vs Client) suppresses the per-retry
				// "RPC connection failed" stderr spam — caller knows
				// the dial failed because cErr is non-nil. Without
				// this, every backoff tick during a visor restart
				// printed an identical ERROR line, exactly the
				// failure mode #2514 was supposed to eliminate.
				if newCl, cErr := clirpc.ClientQuiet(cmd.Flags()); cErr == nil {
					rpcClient = newCl
					// Don't yet reset lastErrLogged — the new
					// client might still 503 if the visor is
					// up but the RPC service isn't. Reset only
					// when the next poll actually returns nil.
				}
				if backoff < maxBackoff {
					backoff *= 2
					if backoff > maxBackoff {
						backoff = maxBackoff
					}
				}
				continue
			}
			if lastErrLogged {
				fmt.Fprintln(os.Stderr, "group poll: reconnected") //nolint:errcheck
				lastErrLogged = false
				backoff = minBackoff
			}
			for _, m := range msgs {
				if m.TS.After(since) {
					since = m.TS
				}
				fmt.Printf("[%s] [%s] %s: %s\n",
					m.TS.UTC().Format(time.RFC3339), m.GroupID, m.SenderPK, m.Text)
			}
		}
	},
}

var groupLeaveCmd = &cobra.Command{
	Use:   "leave <group-id>",
	Short: "Member: leave a group (tears down the subscriber)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if err := rpcClient.GroupLeave(args[0]); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), nil, fmt.Sprintf("left %s\n", args[0]))
	},
}

var groupDeleteCmd = &cobra.Command{
	Use:   "delete <group-id>",
	Short: "Owner: delete a group (tears down the publisher)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if err := rpcClient.GroupDelete(args[0]); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), nil, fmt.Sprintf("deleted %s\n", args[0]))
	},
}

// parsePKs converts a comma/repeated-flag list of hex strings to
// cipher.PubKey slice. Empty input returns nil (no members beyond
// owner). cobra's StringSliceVar honors comma-split + repeat-flag.
func parsePKs(raw []string) ([]cipher.PubKey, error) {
	out := make([]cipher.PubKey, 0, len(raw))
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		var pk cipher.PubKey
		if err := pk.Set(s); err != nil {
			return nil, fmt.Errorf("invalid PK %q: %w", s, err)
		}
		out = append(out, pk)
	}
	return out, nil
}
