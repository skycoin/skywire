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
//	skywire cli skychat group add     <group-id> <pk>      # admin: extend allowlist
//	skywire cli skychat group promote <group-id> <pk>      # admin: grant roster authority
//	skywire cli skychat group demote  <group-id> <pk>      # admin: revoke roster authority (founder is immutable)
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
	"errors"
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
	"github.com/skycoin/skywire/pkg/visor/rpcgrpc"
)

var (
	groupCreateName    string
	groupCreateMode    string
	groupCreateMembers []string

	groupListenSince string
	groupListenPoll  time.Duration
	groupListenRaw   bool

	groupHistoryLimit      int
	groupHistoryListGroups bool
	groupHistoryRaw        bool
)

func init() {
	groupCreateCmd.Flags().StringVarP(&groupCreateName, "name", "n", "", "human-readable group name (required)")
	groupCreateCmd.Flags().StringVarP(&groupCreateMode, "mode", "m", "public", "public | private — private encrypts feed messages with an AES key shipped in the invite link")
	groupCreateCmd.Flags().StringSliceVar(&groupCreateMembers, "member", nil, "initial member PK; repeat for many (the owner is implicit)")
	groupCreateCmd.MarkFlagRequired("name") //nolint:errcheck,gosec

	groupListenCmd.Flags().StringVar(&groupListenSince, "since", "", "RFC3339 lower bound for the first poll; empty = drain current inbox window then follow")
	groupListenCmd.Flags().DurationVar(&groupListenPoll, "interval", time.Second, "poll interval")
	// --raw and --json (persistent) are mutually exclusive; default
	// escapes \n and \r so each event is exactly one stdout line.
	// See cmd/skywire-cli/commands/skychat/escape.go for rationale.
	groupListenCmd.Flags().BoolVar(&groupListenRaw, "raw", false, "emit unescaped multi-line message bodies (humans reading directly; not safe for line-based log aggregators)")

	groupHistoryCmd.Flags().IntVarP(&groupHistoryLimit, "limit", "n", 100, "max messages to return (0 = all stored)")
	groupHistoryCmd.Flags().BoolVar(&groupHistoryListGroups, "list-groups", false, "list every group ID that has persisted messages, then exit")
	groupHistoryCmd.Flags().BoolVar(&groupHistoryRaw, "raw", false, "emit unescaped multi-line message bodies")

	groupCmd.AddCommand(
		groupCreateCmd, groupListCmd, groupInfoCmd, groupInviteCmd,
		groupJoinCmd, groupAddCmd, groupPromoteCmd, groupDemoteCmd,
		groupSendCmd, groupListenCmd, groupHistoryCmd,
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
		// JSON path stays unchanged: tooling and the prior
		// human-readable-was-JSON behavior are preserved when --json
		// is set. The default human path renders a labeled summary
		// plus a per-peer liveness table when peer_last_inbound has
		// entries — the operator-facing surface for the telemetry
		// added in #2628.
		human := renderGroupInfo(info)
		internal.PrintOutput(cmd.Flags(), info, human)
	},
}

// renderGroupInfo formats a GroupInfo for the operator-facing
// `cli skychat group info` default (non-JSON) output. The shape
// mirrors `group list` columns for the header fields, then adds
// a per-peer last-inbound table when the visor populated
// peer_last_inbound (group is a live member-side session with one
// or more peerSubs). A zero time is rendered as "never" to make
// "subscriber up, this peer is silent" obvious vs an absent row.
func renderGroupInfo(info visor.GroupInfo) string {
	var buf strings.Builder
	last := "-"
	if !info.LastMessageAt.IsZero() {
		last = info.LastMessageAt.UTC().Format(time.RFC3339)
	}
	fmt.Fprintf(&buf, "id:                %s\n", info.ID)            //nolint:errcheck
	fmt.Fprintf(&buf, "name:              %s\n", info.Name)          //nolint:errcheck
	fmt.Fprintf(&buf, "owner:             %s\n", info.OwnerPK)       //nolint:errcheck
	fmt.Fprintf(&buf, "port:              %d\n", info.Port)          //nolint:errcheck
	fmt.Fprintf(&buf, "mode:              %s\n", info.Mode)          //nolint:errcheck
	fmt.Fprintf(&buf, "role:              %s\n", info.Role)          //nolint:errcheck
	fmt.Fprintf(&buf, "status:            %s\n", info.Status)        //nolint:errcheck
	fmt.Fprintf(&buf, "created_at:        %s\n", info.CreatedAt.UTC().Format(time.RFC3339))  //nolint:errcheck
	fmt.Fprintf(&buf, "joined_at:         %s\n", info.JoinedAt.UTC().Format(time.RFC3339))   //nolint:errcheck
	fmt.Fprintf(&buf, "last_message_at:   %s\n", last)               //nolint:errcheck
	fmt.Fprintf(&buf, "subscriber_alive:  %t\n", info.SubscriberAlive) //nolint:errcheck
	fmt.Fprintf(&buf, "members (%d):\n", len(info.Members))          //nolint:errcheck
	for _, pk := range info.Members {
		fmt.Fprintf(&buf, "  %s\n", pk) //nolint:errcheck
	}
	if len(info.Admins) > 0 {
		fmt.Fprintf(&buf, "admins (%d):\n", len(info.Admins)) //nolint:errcheck
		for _, pk := range info.Admins {
			fmt.Fprintf(&buf, "  %s\n", pk) //nolint:errcheck
		}
	}
	if len(info.PeerLastInbound) > 0 {
		fmt.Fprintf(&buf, "peer_last_inbound (%d):\n", len(info.PeerLastInbound)) //nolint:errcheck
		w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  PEER\tLAST_INBOUND\tAGE") //nolint:errcheck
		now := time.Now().UTC()
		for pk, ts := range info.PeerLastInbound {
			when := "never"
			age := "-"
			if !ts.IsZero() {
				when = ts.UTC().Format(time.RFC3339)
				age = now.Sub(ts.UTC()).Truncate(time.Second).String()
			}
			fmt.Fprintf(w, "  %s\t%s\t%s\n", pk, when, age) //nolint:errcheck
		}
		_ = w.Flush() //nolint:errcheck
	}
	return buf.String()
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

var groupPromoteCmd = &cobra.Command{
	Use:   "promote <group-id> <pk>",
	Short: "Admin: grant roster authority (add/remove members, issue invites) to PK",
	Long: `Promote PK to admin on the named group.

Any existing admin (founder, or any visor previously promoted) can run
this. Idempotent: promoting an already-admin returns the current
record without writing. The promoted PK gains roster authority from
the moment this call returns on this visor; other visors learn of the
change through subsequent roster gossip (admin-mirror feeds; follow-up
PR).`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var pk cipher.PubKey
		if err := pk.Set(args[1]); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid pk %q: %w", args[1], err))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		info, err := rpcClient.GroupPromoteAdmin(args[0], pk)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		human := fmt.Sprintf("group %s now has %d admin(s)\n", args[0], len(info.Admins))
		internal.PrintOutput(cmd.Flags(), info, human)
	},
}

var groupDemoteCmd = &cobra.Command{
	Use:   "demote <group-id> <pk>",
	Short: "Admin: revoke roster authority from PK (founder cannot be demoted)",
	Long: `Demote PK on the named group.

Any existing admin can run this. The founder (the original group
creator, immutable per Record.OwnerPK) cannot be demoted — that's
the recovery anchor that keeps a group reachable even if every other
admin's visor is offline. Demoting a non-admin PK is a no-op
(idempotent).`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var pk cipher.PubKey
		if err := pk.Set(args[1]); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid pk %q: %w", args[1], err))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		info, err := rpcClient.GroupDemoteAdmin(args[0], pk)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		human := fmt.Sprintf("group %s now has %d admin(s)\n", args[0], len(info.Admins))
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
friction operators hit during a development cycle.

Output modes (mutually exclusive — passing both --raw and --json
errors out):
  text (default): one line per message,
    "[<ts>] [<group-id>] <sender-pk>: <body>". Embedded newlines and
    carriage returns in the body are escaped to literal \n / \r so
    each message is exactly ONE line of stdout. This is the mode
    agents and log-line aggregators should use.
  --raw: same format, body NOT escaped. For humans reading directly.
  --json: NDJSON, one object per line:
    {"ts":..., "group_id":..., "sender_pk":..., "body":...}
    JSON's native string encoding handles multi-line bodies; this
    mode is also one-line-per-event.`,
	Run: func(cmd *cobra.Command, _ []string) {
		jsonMode, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck
		if jsonMode && groupListenRaw {
			internal.PrintFatalError(cmd.Flags(), errors.New("--raw and --json are mutually exclusive"))
		}
		var since time.Time
		if groupListenSince != "" {
			t, err := time.Parse(time.RFC3339, groupListenSince)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid --since: %w", err))
			}
			since = t
		}
		if !jsonMode {
			fmt.Println("Listening for group messages. Ctrl+C to exit.")
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// gRPC server-streaming replaces the net/rpc poll loop that
		// used to live here. The old loop hit the visor's 5-minute
		// per-conn deadline at init_apps.go:542 every tick window;
		// gRPC over HTTP/2 keepalives stays connected for the
		// stream's natural lifetime. The reconnect-on-error wrapper
		// below still handles visor restarts (visor halt → client's
		// stream errors → we reopen + resume).
		const (
			minBackoff = 1 * time.Second
			maxBackoff = 30 * time.Second
		)
		backoff := minBackoff
		var lastErrLogged bool
		// onMsg renders one message — extracted because the same
		// rendering path runs against both the gRPC event type and
		// (when we eventually replay backlog) any seed messages.
		onMsg := func(tsNs int64, groupID, senderPK, body string) {
			ts := time.Unix(0, tsNs).UTC()
			if ts.After(since) {
				since = ts
			}
			if jsonMode {
				ev := struct {
					TS       time.Time `json:"ts"`
					GroupID  string    `json:"group_id"`
					SenderPK string    `json:"sender_pk"`
					Body     string    `json:"body"`
				}{TS: ts, GroupID: groupID, SenderPK: senderPK, Body: body}
				b, mErr := json.Marshal(ev)
				if mErr != nil {
					return
				}
				fmt.Println(string(b))
				return
			}
			out := body
			if !groupListenRaw {
				out = escapeForOneLine(body)
			}
			fmt.Printf("[%s] [%s] %s: %s\n", ts.Format(time.RFC3339), groupID, senderPK, out)
		}

		for {
			if ctx.Err() != nil {
				return
			}
			grpcClient, err := rpcgrpc.NewPingClient(clirpc.Addr)
			if err != nil {
				if !lastErrLogged {
					fmt.Fprintf(os.Stderr, "group listen: gRPC dial %v (reconnecting in %s)\n", err, backoff) //nolint:errcheck
					lastErrLogged = true
				}
				t := time.NewTimer(backoff)
				select {
				case <-t.C:
				case <-ctx.Done():
					t.Stop()
					return
				}
				if backoff < maxBackoff {
					backoff *= 2
					if backoff > maxBackoff {
						backoff = maxBackoff
					}
				}
				continue
			}
			// Stream errors return here; we reopen above. Success
			// path: the stream blocks indefinitely until ctx is
			// canceled (Ctrl+C).
			err = grpcClient.StreamGroupMessages(ctx, "", since.UnixNano(), nil, func(evt *rpcgrpc.GroupMessageEvent) {
				if lastErrLogged {
					fmt.Fprintln(os.Stderr, "group listen: reconnected") //nolint:errcheck
					lastErrLogged = false
					backoff = minBackoff
				}
				onMsg(evt.TimestampNs, evt.GroupId, evt.SenderPk, evt.Body)
			})
			_ = grpcClient.Close() //nolint:errcheck
			if ctx.Err() != nil {
				return
			}
			if err != nil && !lastErrLogged {
				fmt.Fprintf(os.Stderr, "group listen: %v (reconnecting in %s)\n", err, backoff) //nolint:errcheck
				lastErrLogged = true
			}
			t := time.NewTimer(backoff)
			select {
			case <-t.C:
			case <-ctx.Done():
				t.Stop()
				return
			}
			if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
		}
	},
}

var groupHistoryCmd = &cobra.Command{
	Use:   "history <group-id>",
	Short: "Print persisted group messages from the visor's history store",
	Long: `Read group-message history from the visor's persistent store.

Unlike 'listen', which streams live messages, history is a one-shot
read of what's already on disk. Survives visor restarts.

Requires persistence to be enabled in the visor config:

  "skychat": {
    "group_history_db": "group-history.db"
  }

Output (default): newest-last, one line per message, same format as
'listen'. --json emits NDJSON. --list-groups inverts: print every
group ID that has stored messages, then exit.

Examples:
  skywire cli skychat group history <group-id>
  skywire cli skychat group history <group-id> --limit 50
  skywire cli skychat group history --list-groups`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		jsonMode, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck
		if jsonMode && groupHistoryRaw {
			internal.PrintFatalError(cmd.Flags(), errors.New("--raw and --json are mutually exclusive"))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		if groupHistoryListGroups {
			groups, err := rpcClient.GroupHistoryGroups()
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			if jsonMode {
				b, _ := json.Marshal(groups) //nolint:errcheck
				fmt.Println(string(b))
				return
			}
			for _, g := range groups {
				fmt.Println(g)
			}
			return
		}

		if len(args) < 1 {
			internal.PrintFatalError(cmd.Flags(), errors.New("history: <group-id> required (or use --list-groups)"))
		}
		groupID := args[0]
		msgs, err := rpcClient.GroupHistory(groupID, groupHistoryLimit)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		for _, m := range msgs {
			if jsonMode {
				ev := struct {
					TS       time.Time `json:"ts"`
					GroupID  string    `json:"group_id"`
					SenderPK string    `json:"sender_pk"`
					Body     string    `json:"body"`
				}{TS: m.TS.UTC(), GroupID: m.GroupID, SenderPK: m.SenderPK.Hex(), Body: m.Text}
				b, mErr := json.Marshal(ev)
				if mErr != nil {
					continue
				}
				fmt.Println(string(b))
				continue
			}
			body := m.Text
			if !groupHistoryRaw {
				body = escapeForOneLine(body)
			}
			fmt.Printf("[%s] [%s] %s: %s\n",
				m.TS.UTC().Format(time.RFC3339), m.GroupID, m.SenderPK, body)
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
