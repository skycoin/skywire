// Package clidmsg cmd/skywire-cli/commands/dmsg/pty.go
package clidmsg

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/toqueteos/webbrowser"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgpty"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	ptyPort        string
	ptyRpcAddr     string
	ptyPath        string
	ptyPK          string
	ptyURL         string
	ptyPkg         bool
	ptyLogger      = logging.MustGetLogger("dmsgpty")
	ptyExecTimeout string
	ptyExecEnv     []string
)

func init() {
	// pty command
	ptyCmd.AddCommand(
		ptyListCmd,
		ptyStartCmd,
		ptyExecCmd,
		ptyUICmd,
		ptyURLCmd,
	)

	// Flags for list command
	ptyListCmd.PersistentFlags().StringVarP(&ptyRpcAddr, "rpc", "", "localhost:3435", "RPC server address")

	// Flags for start command
	ptyStartCmd.PersistentFlags().StringVarP(&ptyRpcAddr, "rpc", "", "localhost:3435", "RPC server address")
	ptyStartCmd.PersistentFlags().StringVarP(&ptyPort, "port", "p", "22", "port of remote visor dmsgpty")

	// Flags for exec command
	ptyExecCmd.PersistentFlags().StringVarP(&ptyRpcAddr, "rpc", "", "localhost:3435", "RPC server address")
	ptyExecCmd.PersistentFlags().StringVarP(&ptyPort, "port", "p", "22", "port of remote visor dmsgpty")
	ptyExecCmd.Flags().StringVarP(&ptyExecTimeout, "timeout", "t", "30s", "max command duration (e.g. 30s, 2m); host-side cap is 5m")
	ptyExecCmd.Flags().StringArrayVarP(&ptyExecEnv, "env", "e", nil, "extra env var KEY=VALUE; repeatable")

	// Flags for ui command
	ptyUICmd.Flags().StringVarP(&ptyPath, "input", "i", "", "read from specified config file")
	ptyUICmd.Flags().BoolVarP(&ptyPkg, "pkg", "p", false, "read from "+visorconfig.SkywireConfig())
	ptyUICmd.Flags().StringVarP(&ptyPK, "visor", "v", "", "public key of visor to connect to")

	// Flags for url command
	ptyURLCmd.Flags().StringVarP(&ptyPath, "input", "i", "", "read from specified config file")
	ptyURLCmd.Flags().BoolVarP(&ptyPkg, "pkg", "p", false, "read from "+visorconfig.SkywireConfig())
	ptyURLCmd.Flags().StringVarP(&ptyPK, "visor", "v", "", "public key of visor to connect to")
}

// ptyCmd is the command for dmsgpty functionality
var ptyCmd = &cobra.Command{
	Use:   "pty",
	Short: "Interact with remote visors",
	Long:  "Commands for interacting with remote visors via dmsgpty",
}

var ptyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List connected visors",
	Run: func(cmd *cobra.Command, _ []string) {
		remoteVisors, err := ptyRPCClient(cmd.Flags()).RemoteVisors()
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

var ptyStartCmd = &cobra.Command{
	Use:   "start <pk>",
	Short: "Start dmsgpty session",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cli := dmsgpty.DefaultCLI()
		addr := internal.ParsePK(cmd.Flags(), "pk", args[0])
		port, _ := strconv.ParseUint(ptyPort, 10, 16) //nolint:errcheck
		ctx, cancel := cmdutil.SignalContext(context.Background(), nil)
		defer cancel()
		return cli.StartRemotePty(ctx, addr, uint16(port), dmsgpty.DefaultCmd)
	},
}

var ptyExecCmd = &cobra.Command{
	Use:   "exec <pk> <command> [args...]",
	Short: "Run a one-shot command on a remote visor (no TTY)",
	Long: `Runs a single command on the remote visor identified by <pk>, captures
stdout/stderr/exit-code, and prints them locally. Same dmsgpty whitelist
gates this as the interactive shell command (` + "`cli dmsg pty start`" + `).

Use when scripting against a remote visor — the interactive shell needs
a real TTY which pipes/CI runners don't have. Output is captured in full
(up to 16 MiB stdout + 16 MiB stderr, host-side cap), so favor commands
that produce bounded output (status / config / log slices) over streaming
ones — use ` + "`start`" + ` for those.

Examples:
  skywire cli dmsg pty exec <pk> -- systemctl status skywire-update.timer
  skywire cli dmsg pty exec <pk> --timeout 10s -- journalctl -u skywire --since '5 min ago'
  skywire cli dmsg pty exec <pk> -- /bin/sh -c 'cat /etc/systemd/system/skywire-update.service'

The local CLI exit code mirrors the remote command's exit code (0 on
success, the remote's exit code on non-zero exit, 124 on timeout, 1 on
RPC-layer failure). stdout flows to local stdout, stderr to local stderr.`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cli := dmsgpty.DefaultCLI()
		addr := internal.ParsePK(cmd.Flags(), "pk", args[0])
		port, _ := strconv.ParseUint(ptyPort, 10, 16) //nolint:errcheck
		timeout, err := time.ParseDuration(ptyExecTimeout)
		if err != nil {
			return fmt.Errorf("--timeout %q: %w", ptyExecTimeout, err)
		}
		name := args[1]
		var cmdArgs []string
		if len(args) > 2 {
			cmdArgs = args[2:]
		}
		ctx, cancel := cmdutil.SignalContext(context.Background(), nil)
		defer cancel()

		resp, err := cli.ExecRemote(ctx, addr, uint16(port), name, cmdArgs, ptyExecEnv, nil, timeout)
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

var ptyUICmd = &cobra.Command{
	Use:   "ui",
	Short: "Open dmsgpty UI in default browser",
	Run: func(cmd *cobra.Command, _ []string) {
		if ptyPK == "" {
			if ptyPkg {
				ptyPath = visorconfig.SkywireConfig()
			}
			if ptyPath != "" {
				conf, err := visorconfig.ReadFile(ptyPath)
				if err != nil {
					log.Fatal("Failed to read in config file:", err)
				}
				ptyURL = fmt.Sprintf("http://127.0.0.1:8000/pty/%s", conf.PK.Hex())
			} else {
				client := ptyRPCClient(cmd.Flags())
				overview, err := client.Overview()
				if err != nil {
					log.Fatal("Failed to connect; is skywire running?\n", err)
				}
				ptyURL = fmt.Sprintf("http://127.0.0.1:8000/pty/%s", overview.PubKey.Hex())
			}
		} else {
			ptyURL = fmt.Sprintf("http://127.0.0.1:8000/pty/%s", ptyPK)
		}
		if err := webbrowser.Open(ptyURL); err != nil {
			log.Fatal("Failed to open dmsgpty UI in browser:", err)
		}
	},
}

var ptyURLCmd = &cobra.Command{
	Use:   "url",
	Short: "Show dmsgpty UI URL",
	Run: func(cmd *cobra.Command, _ []string) {
		if ptyPK == "" {
			if ptyPkg {
				ptyPath = visorconfig.SkywireConfig()
			}
			if ptyPath != "" {
				conf, err := visorconfig.ReadFile(ptyPath)
				if err != nil {
					internal.Catch(cmd.Flags(), fmt.Errorf("Failed to read in config file: %v", err))
				}
				ptyURL = fmt.Sprintf("http://127.0.0.1:8000/pty/%s", conf.PK.Hex())
			} else {
				client := ptyRPCClient(cmd.Flags())
				overview, err := client.Overview()
				if err != nil {
					internal.Catch(cmd.Flags(), fmt.Errorf("Failed to connect; is skywire running?: %v", err))
				}
				ptyURL = fmt.Sprintf("http://127.0.0.1:8000/pty/%s", overview.PubKey.Hex())
			}
		} else {
			ptyURL = fmt.Sprintf("http://127.0.0.1:8000/pty/%s", ptyPK)
		}

		output := struct {
			URL string `json:"url"`
		}{
			URL: ptyURL,
		}

		internal.PrintOutput(cmd.Flags(), output, fmt.Sprintln(ptyURL))
	},
}

func ptyRPCClient(cmdFlags *pflag.FlagSet) visor.API {
	const rpcDialTimeout = time.Second * 5
	conn, err := net.DialTimeout("tcp", ptyRpcAddr, rpcDialTimeout)
	if err != nil {
		internal.PrintFatalError(cmdFlags, fmt.Errorf("RPC connection failed; is skywire running?: %v", err))
	}
	return visor.NewRPCClient(ptyLogger, conn, visor.RPCPrefix, 0)
}
