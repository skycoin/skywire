// Package clidmsgpty cmd/skywire-cli/commands/dmsgpty/root.go
package clidmsgpty

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/pty"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	ptyPort       string
	masterLogger  = logging.NewMasterLogger()
	packageLogger = masterLogger.PackageLogger("dmsgpty")
	rpcAddr       string
	path          string
	pk            string
	url           string
	pkg           bool

	execTimeout string
	execEnv     []string
)

// This dmsgpty/ subtree is the legacy entrypoint; the active path is
// cmd/skywire-cli/commands/dmsg/pty.go. We keep both wired so muscle
// memory works either way, but the canonical doc lives in dmsg/pty.go.

func init() {
	visorsCmd.PersistentFlags().StringVarP(&rpcAddr, "rpc", "", "localhost:3435", "RPC server address")
	shellCmd.PersistentFlags().StringVarP(&rpcAddr, "rpc", "", "localhost:3435", "RPC server address")
	shellCmd.PersistentFlags().StringVarP(&ptyPort, "port", "p", "22", "port of remote visor dmsgpty")
	execCmd.PersistentFlags().StringVarP(&rpcAddr, "rpc", "", "localhost:3435", "RPC server address")
	execCmd.PersistentFlags().StringVarP(&ptyPort, "port", "p", "22", "port of remote visor dmsgpty")
	execCmd.Flags().StringVarP(&execTimeout, "timeout", "t", "30s", "max command duration (e.g. 30s, 2m); host-side cap is 5m")
	execCmd.Flags().StringArrayVarP(&execEnv, "env", "e", nil, "extra env var KEY=VALUE; repeatable")
}

// RootCmd is the command that contains sub-commands which interacts with pty.
var RootCmd = &cobra.Command{
	Use:   "dmsgpty",
	Short: "Interact with remote visors",
}

func init() {
	RootCmd.AddCommand(
		visorsCmd,
		shellCmd,
		execCmd,
	)
}

var visorsCmd = &cobra.Command{
	Use:   "list",
	Short: "List connected visors",
	Run: func(cmd *cobra.Command, _ []string) {
		remoteVisors, err := rpcClient(cmd.Flags()).RemoteVisors()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("RPC connection failed; is skywire running?: %v", err))
		}

		var msg string
		for idx, pk := range remoteVisors {
			msg += fmt.Sprintf("%d. %s\n", idx+1, pk)
		}
		internal.PrintOutput(cmd.Flags(), remoteVisors, msg)
	},
}

var shellCmd = &cobra.Command{
	Use:   "start <pk>",
	Short: "Start dmsgpty session",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cli := pty.DefaultCLI()
		addr := internal.ParsePK(cmd.Flags(), "pk", args[0])
		port, _ := strconv.ParseUint(ptyPort, 10, 16) //nolint:errcheck
		ctx, cancel := cmdutil.SignalContext(context.Background(), nil)
		defer cancel()
		return cli.StartRemotePty(ctx, addr, uint16(port), pty.DefaultCmd)
	},
}

var execCmd = &cobra.Command{
	Use:   "exec <pk> <command> [args...]",
	Short: "Run a one-shot command on a remote visor (no TTY)",
	Long: `Runs a single command on the remote visor identified by <pk>, captures
stdout/stderr/exit-code, and prints them locally. Same dmsgpty whitelist
gates this as the shell session command (` + "`cli dmsg pty start`" + `).

Use when scripting against a remote visor — the interactive shell needs
a real TTY which pipes/CI runners don't have. Output is captured in full
(up to 16 MiB stdout + 16 MiB stderr, host-side cap), so favor commands
that produce bounded output (status / config / log slices) over streaming
ones (` + "`tail -f`" + `, ` + "`journalctl -f`" + `) — use ` + "`start`" + ` for those.

Examples:
  skywire cli dmsg pty exec <pk> -- systemctl status skywire-update.timer
  skywire cli dmsg pty exec <pk> --timeout 10s -- journalctl -u skywire --since '5 min ago'
  skywire cli dmsg pty exec <pk> -- /bin/sh -c 'cat /etc/systemd/system/skywire-update.service'

The local CLI exit code mirrors the remote command's exit code (0 on
success, the remote's exit code on non-zero exit, 124 on timeout, 1 on
RPC-layer failure). stdout flows to local stdout, stderr to local stderr.`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cli := pty.DefaultCLI()
		addr := internal.ParsePK(cmd.Flags(), "pk", args[0])
		port, _ := strconv.ParseUint(ptyPort, 10, 16) //nolint:errcheck
		timeout, err := time.ParseDuration(execTimeout)
		if err != nil {
			return fmt.Errorf("--timeout %q: %w", execTimeout, err)
		}
		name := args[1]
		var cmdArgs []string
		if len(args) > 2 {
			cmdArgs = args[2:]
		}
		ctx, cancel := cmdutil.SignalContext(context.Background(), nil)
		defer cancel()

		resp, err := cli.ExecRemote(ctx, addr, uint16(port), name, cmdArgs, execEnv, nil, timeout)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "exec: %v\n", err) //nolint:errcheck
			os.Exit(1)
		}
		if len(resp.Stdout) > 0 {
			_, _ = cmd.OutOrStdout().Write(resp.Stdout) //nolint:errcheck
		}
		if len(resp.Stderr) > 0 {
			_, _ = cmd.ErrOrStderr().Write(resp.Stderr) //nolint:errcheck
		}
		// Truncation notice on stderr so callers don't silently
		// trust an incomplete buffer — adopting cat-style behavior
		// rather than failing loud is operator-friendly for
		// diagnostic dumps but the marker prevents false confidence.
		if resp.StdoutTruncated {
			fmt.Fprintf(cmd.ErrOrStderr(), "[stdout truncated at 16MiB]\n") //nolint:errcheck
		}
		if resp.StderrTruncated {
			fmt.Fprintf(cmd.ErrOrStderr(), "[stderr truncated at 16MiB]\n") //nolint:errcheck
		}
		if resp.TimedOut {
			fmt.Fprintf(cmd.ErrOrStderr(), "[timed out after %dms]\n", resp.DurationMS) //nolint:errcheck
			os.Exit(124)
		}
		if resp.ExitCode != 0 {
			os.Exit(resp.ExitCode)
		}
		return nil
	},
}

func rpcClient(cmdFlags *pflag.FlagSet) visor.API {
	const rpcDialTimeout = time.Second * 5
	conn, err := net.DialTimeout("tcp", rpcAddr, rpcDialTimeout)
	if err != nil {
		internal.PrintFatalError(cmdFlags, fmt.Errorf("RPC connection failed; is skywire running?: %v", err))
	}
	return visor.NewRPCClient(packageLogger, conn, visor.RPCPrefix, 0)
}
