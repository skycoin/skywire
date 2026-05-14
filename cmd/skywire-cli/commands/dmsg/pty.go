// Package clidmsg cmd/skywire-cli/commands/dmsg/pty.go
package clidmsg

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/toqueteos/webbrowser"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/cipher"
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
	// ptyVia, when set, bypasses the local dmsgpty-host RPC path and
	// dials the remote dmsgpty-host's direct-TCP listener instead.
	// Format: `tcp://<pk>@<host>:<port>`. The remote PK is pinned in
	// the noise XK handshake (host-key-check equivalent).
	ptyVia string
	// ptySK is the local identity used for the direct-TCP path's
	// noise handshake. The remote's whitelist gates on the resulting
	// PK, so callers wanting stable authorization should pin --sk
	// (or set DMSGPTY_SK in the environment). Unset = ephemeral.
	ptySK cipher.SecKey
	// ptyViaVisor, when true, borrows the local visor's secret key
	// from its config file for the --via tcp path's noise handshake.
	// Convenience for the common case of running the CLI on a host
	// that also runs a visor: the visor's PK is typically already
	// on the remote's whitelist, so the operator skips having to
	// pin --sk or seed DMSGPTY_SK. An explicit --sk / DMSGPTY_SK
	// always wins.
	ptyViaVisor bool
)

// ptyTCPViaRE matches `tcp://<66-hex-pk>@<host:port>` for the --via
// flag. Anchored so a typo doesn't silently fall through to a
// different scheme.
var ptyTCPViaRE = regexp.MustCompile(`^tcp://([a-f0-9]{66})@(.+)$`)

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
	ptyStartCmd.Flags().StringVar(&ptyVia, "via", "",
		"bypass local visor + dial remote dmsgpty-host's direct-TCP listener: tcp://<pk>@<host>:<port>")
	ptyStartCmd.Flags().VarP(&ptySK, "sk", "s",
		"local secret key for the --via direct-TCP path's noise handshake (random if unset; pin for stable whitelist authorization)")
	ptyStartCmd.Flags().BoolVar(&ptyViaVisor, "via-visor", false,
		"borrow local visor's secret key from "+visorconfig.SkywireConfig()+" for the --via noise handshake (--sk wins if set)")

	// Flags for exec command
	ptyExecCmd.PersistentFlags().StringVarP(&ptyRpcAddr, "rpc", "", "localhost:3435", "RPC server address")
	ptyExecCmd.PersistentFlags().StringVarP(&ptyPort, "port", "p", "22", "port of remote visor dmsgpty")
	ptyExecCmd.Flags().StringVarP(&ptyExecTimeout, "timeout", "t", "30s", "max command duration (e.g. 30s, 2m); host-side cap is 5m")
	ptyExecCmd.Flags().StringArrayVarP(&ptyExecEnv, "env", "e", nil, "extra env var KEY=VALUE; repeatable")
	ptyExecCmd.Flags().StringVar(&ptyVia, "via", "",
		"bypass local visor + dial remote dmsgpty-host's direct-TCP listener: tcp://<pk>@<host>:<port>")
	ptyExecCmd.Flags().VarP(&ptySK, "sk", "s",
		"local secret key for the --via direct-TCP path's noise handshake (random if unset; pin for stable whitelist authorization)")
	ptyExecCmd.Flags().BoolVar(&ptyViaVisor, "via-visor", false,
		"borrow local visor's secret key from "+visorconfig.SkywireConfig()+" for the --via noise handshake (--sk wins if set)")

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
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := cmdutil.SignalContext(context.Background(), nil)
		defer cancel()

		// --via tcp://<pk>@<host:port> bypasses local visor. <pk> in
		// the URL pins the remote (server) PK; positional <pk> arg is
		// then optional and ignored if supplied.
		if ptyVia != "" {
			rPK, addr, err := parseTCPVia(ptyVia)
			if err != nil {
				return err
			}
			myPK, mySK := resolveTCPIdentity(ptySK)
			tcpCli := dmsgpty.DefaultCLI()
			return (&tcpCli).StartRemotePtyTCP(ctx, rPK, addr, myPK, mySK, dmsgpty.DefaultCmd)
		}

		if len(args) < 1 {
			return fmt.Errorf("pty start: <pk> required (or use --via tcp://<pk>@<host:port>)")
		}
		cli := dmsgpty.DefaultCLI()
		addr := internal.ParsePK(cmd.Flags(), "pk", args[0])
		port, _ := strconv.ParseUint(ptyPort, 10, 16) //nolint:errcheck
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
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		timeout, err := time.ParseDuration(ptyExecTimeout)
		if err != nil {
			return fmt.Errorf("--timeout %q: %w", ptyExecTimeout, err)
		}
		ctx, cancel := cmdutil.SignalContext(context.Background(), nil)
		defer cancel()

		// --via tcp://<pk>@<host:port> shifts argument layout: pk is
		// in the URL, so args are just `<command> [args...]` (no
		// leading positional pk).
		if ptyVia != "" {
			if len(args) < 1 {
				return fmt.Errorf("pty exec --via: <command> required")
			}
			rPK, addr, err := parseTCPVia(ptyVia)
			if err != nil {
				return err
			}
			myPK, mySK := resolveTCPIdentity(ptySK)
			name := args[0]
			var cmdArgs []string
			if len(args) > 1 {
				cmdArgs = args[1:]
			}
			tcpCli := dmsgpty.DefaultCLI()
			resp, err := (&tcpCli).ExecRemoteTCP(ctx, rPK, addr, myPK, mySK, name, cmdArgs, ptyExecEnv, nil, timeout)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "exec: %v\n", err) //nolint:errcheck
				os.Exit(1)
			}
			reportExecResult(cmd, resp)
			return nil
		}

		if len(args) < 2 {
			return fmt.Errorf("pty exec: <pk> <command> required (or use --via tcp://<pk>@<host:port>)")
		}
		cli := dmsgpty.DefaultCLI()
		addr := internal.ParsePK(cmd.Flags(), "pk", args[0])
		port, _ := strconv.ParseUint(ptyPort, 10, 16) //nolint:errcheck
		name := args[1]
		var cmdArgs []string
		if len(args) > 2 {
			cmdArgs = args[2:]
		}

		resp, err := cli.ExecRemote(ctx, addr, uint16(port), name, cmdArgs, ptyExecEnv, nil, timeout)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "exec: %v\n", err) //nolint:errcheck
			os.Exit(1)
		}
		reportExecResult(cmd, resp)
		return nil
	},
}

// reportExecResult renders a CommandExecResult to the caller's
// stdout/stderr and exits with the matching code. Mirrors the
// inline rendering the dmsg-overlay path used pre-refactor; lifted
// here so the --via tcp path can share the same surface. Returns
// only on a clean (exit 0, not truncated, not timed out) completion
// — other shapes call os.Exit directly to surface the right exit
// code to the caller's shell.
func reportExecResult(cmd *cobra.Command, resp *dmsgpty.CommandExecResult) {
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
}

// parseTCPVia splits a `tcp://<pk>@<host:port>` URL into the pinned
// remote PK and the dial-target address. The PK must be the
// canonical 66-char-hex form; the address is passed through to
// net.Dial verbatim so any net.Dial-acceptable form (host:port,
// [v6]:port, etc.) works.
func parseTCPVia(via string) (cipher.PubKey, string, error) {
	m := ptyTCPViaRE.FindStringSubmatch(via)
	if m == nil {
		return cipher.PubKey{}, "", fmt.Errorf("pty: --via %q must be tcp://<66-hex-pk>@<host:port>", via)
	}
	var pk cipher.PubKey
	if err := pk.Set(m[1]); err != nil {
		return cipher.PubKey{}, "", fmt.Errorf("pty: --via remote PK invalid: %w", err)
	}
	return pk, m[2], nil
}

// resolveTCPIdentity returns the (PK, SK) used as the local
// identity in the --via tcp noise handshake. Resolution order:
//
//  1. skFlag (--sk) if non-zero, OR DMSGPTY_SK env if non-empty.
//     Explicit identity always wins.
//  2. --via-visor: read skywire.json and use the visor's SK. The
//     visor's PK is typically already on remote whitelists, so
//     this is the zero-config convenience path for operators
//     running CLI alongside a visor.
//  3. Fresh ephemeral keypair. Whitelist-pinned remotes will
//     reject it; useful for testing against open-whitelist hosts
//     or proving connectivity.
//
// Fatal-exits with a helpful message on a malformed identity
// source — the caller can't proceed without a valid keypair.
func resolveTCPIdentity(skFlag cipher.SecKey) (cipher.PubKey, cipher.SecKey) {
	var zero cipher.SecKey
	sk := skFlag
	if sk == zero {
		if env := os.Getenv("DMSGPTY_SK"); env != "" {
			_ = sk.Set(env) //nolint:errcheck,gosec
		}
	}
	if sk == zero && ptyViaVisor {
		confPath := visorconfig.SkywireConfig()
		conf, err := visorconfig.ReadFile(confPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "pty --via-visor: read %s: %v\n", confPath, err) //nolint:errcheck
			os.Exit(1)
		}
		if conf.SK == zero {
			fmt.Fprintf(os.Stderr, "pty --via-visor: visor config %s has empty SK\n", confPath) //nolint:errcheck
			os.Exit(1)
		}
		sk = conf.SK
	}
	if sk == zero {
		pk, fresh := cipher.GenerateKeyPair()
		return pk, fresh
	}
	pk, err := sk.PubKey()
	if err != nil {
		// SecKey.Set already validated the SK shape; PubKey derivation
		// is deterministic. Treat unexpected failure as fatal — the
		// caller can't proceed without a valid identity.
		fmt.Fprintf(os.Stderr, "pty --via: failed to derive PK from --sk: %v\n", err) //nolint:errcheck
		os.Exit(1)
	}
	return pk, sk
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
