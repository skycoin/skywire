package cliskychat

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

var groupAvatarClear bool

func init() {
	groupAvatarCmd.Flags().BoolVar(&groupAvatarClear, "clear", false, "remove the group picture")
	groupCmd.AddCommand(
		groupRequestsCmd, groupApproveCmd, groupDenyCmd,
		groupRemoveCmd, groupBanCmd, groupUnbanCmd, groupMuteCmd, groupUnmuteCmd,
		groupReadOnlyCmd, groupRenameCmd, groupAvatarCmd, groupJoinPoWCmd,
	)
}

// memberCmd builds an admin command that acts on one member PK and prints
// the group record the visor returns.
func memberCmd(use, short, done string, call func(visorapi.API, string, cipher.PubKey) (visorapi.GroupInfo, error)) *cobra.Command {
	return &cobra.Command{
		Use:   use + " <group-id> <pk>",
		Short: short,
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
			info, err := call(rpcClient, args[0], pk)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			internal.PrintOutput(cmd.Flags(), info, fmt.Sprintf("%s %s in group %s\n", done, pk, args[0]))
		},
	}
}

var (
	groupApproveCmd = memberCmd("approve", "Admin: admit a PK whose join request is pending", "approved",
		visorapi.API.GroupApproveJoin)
	groupRemoveCmd = memberCmd("remove", "Admin: remove a member (they can ask to join again)", "removed",
		visorapi.API.GroupRemoveMember)
	groupBanCmd = memberCmd("ban", "Admin: remove a member and refuse their future join requests", "banned",
		visorapi.API.GroupBanMember)
	groupUnbanCmd = memberCmd("unban", "Admin: lift a ban (the PK is not re-added)", "unbanned",
		visorapi.API.GroupUnbanMember)
	groupMuteCmd = memberCmd("mute", "Admin: keep a member in the group but stop them posting", "muted",
		visorapi.API.GroupMuteMember)
	groupUnmuteCmd = memberCmd("unmute", "Admin: let a muted member post again", "unmuted",
		visorapi.API.GroupUnmuteMember)
)

var groupDenyCmd = &cobra.Command{
	Use:   "deny <group-id> <pk>",
	Short: "Admin: decline a pending join request",
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
		if err := rpcClient.GroupDenyJoin(args[0], pk); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), nil, fmt.Sprintf("denied %s in group %s\n", pk, args[0]))
	},
}

var groupRequestsCmd = &cobra.Command{
	Use:   "requests <group-id>",
	Short: "Admin: list the join requests a group has received",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		reqs, err := rpcClient.GroupJoinRequests(args[0])
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		var b strings.Builder
		tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "PK\tSTATUS\tASKED\tNOTE") //nolint:errcheck
		for _, r := range reqs {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.PK, r.Status, r.AskedAt.Format(time.RFC3339), escapeForOneLine(r.Note)) //nolint:errcheck
		}
		_ = tw.Flush() //nolint:errcheck
		internal.PrintOutput(cmd.Flags(), reqs, b.String())
	},
}

var groupReadOnlyCmd = &cobra.Command{
	Use:   "read-only <group-id> <on|off>",
	Short: "Admin: let only admins post (on) or every member (off)",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var on bool
		switch strings.ToLower(args[1]) {
		case "on", "true", "yes":
			on = true
		case "off", "false", "no":
		default:
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("expected on or off, got %q", args[1]))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		info, err := rpcClient.GroupSetReadOnly(args[0], on)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), info, fmt.Sprintf("group %s read-only: %t\n", args[0], info.ReadOnly))
	},
}

var groupRenameCmd = &cobra.Command{
	Use:   "rename <group-id> <name>",
	Short: "Admin: change the group's display name",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		setGroupMeta(cmd, visorapi.GroupSetMetaArgs{ID: args[0], Name: strings.Join(args[1:], " ")})
	},
}

var groupAvatarCmd = &cobra.Command{
	Use:   "avatar <group-id> [image-file]",
	Short: "Admin: set the group's picture from an image file, or remove it with --clear",
	Args:  cobra.RangeArgs(1, 2),
	Run: func(cmd *cobra.Command, args []string) {
		meta := visorapi.GroupSetMetaArgs{ID: args[0], SetAvatar: true}
		switch {
		case groupAvatarClear && len(args) == 1:
		case !groupAvatarClear && len(args) == 2:
			data, err := os.ReadFile(args[1])
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			meta.Avatar = "data:" + http.DetectContentType(data) + ";base64," + base64.StdEncoding.EncodeToString(data)
		default:
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("give an image file or --clear, not both"))
		}
		setGroupMeta(cmd, meta)
	},
}

func setGroupMeta(cmd *cobra.Command, meta visorapi.GroupSetMetaArgs) {
	rpcClient, err := clirpc.Client(cmd.Flags())
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	info, err := rpcClient.GroupSetMeta(meta)
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	internal.PrintOutput(cmd.Flags(), info, fmt.Sprintf("updated group %s (%s)\n", meta.ID, info.Name))
}

var groupJoinPoWCmd = &cobra.Command{
	Use:   "join-pow <group-id> <bits>",
	Short: "Admin: set the proof-of-work a join request must carry (0 turns it off)",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		bits, err := strconv.ParseUint(args[1], 10, 8)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid bits %q: %w", args[1], err))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		info, err := rpcClient.GroupSetJoinPoW(args[0], uint8(bits))
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), info, fmt.Sprintf("group %s join proof-of-work: %d bits\n", args[0], info.JoinPoWBits))
	},
}
