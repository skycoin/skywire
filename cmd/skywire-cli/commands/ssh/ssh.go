// Package clissh cmd/skywire-cli/commands/ssh/ssh.go c4-vis-cli
// the OpenSSH-equivalent client over skywire identity.
//
// Maps onto the existing pty.CLI direct-TCP path (PR #2559/#2560):
// XK noise handshake pins the server PK, the server side authorizes
// the client PK against the dmsgpty whitelist (= authorized_keys
// equivalent), and the resulting stream runs the dmsgpty
// mux — interactive shell or one-shot exec, your pick.
//
// This is a thin alias over `cli dmsg pty exec/start --via tcp://...`
// with the destination spelled `<pk>@<host>:<port>` (matching ssh's
// `user@host:port` muscle memory) and the visor's SK auto-loaded by
// default (--no-visor-key opts out). The underlying machinery lives
// in pkg/dmsg/dmsgpty/cli_tcp.go.
package clissh

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/pty"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// sshDestRE matches both bare `<pk>@<host>:<port>` and the more
// explicit `tcp://<pk>@<host>:<port>` forms. The pk is 66-char hex;
// the address part is anything net.Dial will accept (`host:port`,
// `[v6]:port`, IPv4 with port). The address must contain a colon —
// the default port belongs in the URL, not implicit in this layer,
// to keep parsing unambiguous against IPv6 addresses.
var sshDestRE = regexp.MustCompile(`^(?:tcp://)?([a-f0-9]{66})@(.+:[^:]+)$`)

// Flags (package-level so cobra can wire them once).
var (
	sshSK         cipher.SecKey
	sshNoVisor    bool
	sshEnv        []string
	sshTimeout    string
	sshNoPty      bool
	sshDefPort    string
	sshTransport  string
	sshStandalone bool
	sshVisorKey   bool
	sshLegacyPK   string //nolint:unused // kept for future "ssh <pk>" + --host shape
)

func init() {
	RootCmd.Flags().SortFlags = false
	RootCmd.Flags().VarP(&sshSK, "sk", "s",
		"local client SK for the noise handshake (random if unset; pin for stable whitelist authorization)")
	RootCmd.Flags().BoolVar(&sshNoVisor, "no-visor-key", false,
		"don't borrow the local visor's SK from "+visorconfig.SkywireConfig()+" — use --sk or a random one instead")
	RootCmd.Flags().StringArrayVarP(&sshEnv, "env", "e", nil,
		"extra env var KEY=VALUE; repeatable; exec mode only")
	RootCmd.Flags().StringVarP(&sshTimeout, "timeout", "t", "30s",
		"max command duration in exec mode (e.g. 30s, 2m); host-side cap is 5m")
	RootCmd.Flags().BoolVar(&sshNoPty, "no-pty", false,
		"force exec mode even when no command is given (rarely useful — defaults to interactive when no command, exec when command is present, matching ssh's behavior)")
	RootCmd.Flags().StringVarP(&sshDefPort, "port", "p", "2022",
		"default port when the destination omits one (e.g. 'ssh <pk>@host' resolves to <pk>@host:<port>)")
	RootCmd.Flags().StringVar(&sshTransport, "transport", "auto",
		"transport: auto|tcp (pty shell is direct-TCP only; dmsg|skynet error — use 'cli pty exec' for those)")
	RootCmd.Flags().BoolVar(&sshStandalone, "standalone", false,
		"(not supported for pty shell — accepted for vocabulary parity; errors if set)")
	RootCmd.Flags().BoolVar(&sshVisorKey, "visor-key", true,
		"borrow the local visor's SK from "+visorconfig.SkywireConfig()+" for the noise handshake (default true; --sk wins)")
	_ = RootCmd.Flags().MarkHidden("no-visor-key") //nolint:errcheck
}

// RootCmd is the `skywire cli pty shell` command — OpenSSH-equivalent client
// over skywire identity. Registered separately under skywire-cli's
// root alongside the `sshd` server command.
var RootCmd = &cobra.Command{
	Use:   "ssh <pk>@<host>[:<port>] [-- <command> [args...]]",
	Short: "Open a remote shell or exec a command on a peer visor",
	Long: `skywire cli pty shell — OpenSSH-equivalent client over skywire identity.

Dials a peer's direct-TCP dmsgpty endpoint (the surface exposed by
'skywire cli pty host' or by 'pty.ssh_listen' in the visor config),
runs an XK noise handshake pinning the server's PK, and proxies an
interactive shell or a one-shot exec.

Compared to OpenSSH:
  ssh's host-key check  ↔ noise XK pins the server PK from the
                          destination URL
  authorized_keys       ↔ dmsgpty whitelist on the server side
  password / pubkey     ↔ client PK alone, derived from the local
                          visor's SK by default (override with --sk
                          or --visor-key=false)
  TCP :22 default       ↔ TCP :2022 default (--port to override)
  ssh -t / ssh <cmd>    ↔ no positional command → interactive pty
                          positional command after '--' → exec mode

Examples:
  # Interactive shell on a peer visor at 1.2.3.4:2022
  skywire cli pty shell 0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c@1.2.3.4:2022

  # One-shot exec
  skywire cli pty shell 0323...@1.2.3.4:2022 -- systemctl status skywire

  # Use a pinned client SK instead of the visor's
  skywire cli pty shell 0323...@1.2.3.4:2022 --sk <hex> -- whoami

Transport: this command is direct-TCP only. --transport auto and
--transport tcp both select that single path; --transport dmsg /
skynet error with a pointer to 'cli pty exec', which has the via-visor
paths. A tcp://<pk>@host:port destination is accepted (the scheme is
optional); dmsg:// / skynet:// destinations are rejected here.
--standalone is not supported (errors if set).

The underlying transport is identical to 'cli dmsg pty exec/start
--via tcp://<pk>@<host>:<port>'; this command is the discoverable
ssh-shaped alias over that path.`,
	Args:                  cobra.MinimumNArgs(1),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if sshStandalone {
			return fmt.Errorf("pty shell: --standalone not supported (this command is direct-TCP only); for a via-visor path use `cli pty exec` / `cli pty start`")
		}
		dest := args[0]

		// A tcp:// scheme on the destination is stripped and accepted;
		// dmsg:// / skynet:// are rejected — pty shell is direct-TCP
		// only. --transport auto|tcp map to the same TCP path.
		scheme, rest := internal.SplitTargetScheme(dest)
		eff, err := internal.ResolveTransport(sshTransport, cmd.Flags().Changed("transport"), scheme)
		if err != nil {
			return fmt.Errorf("pty shell: %w", err)
		}
		switch eff {
		case internal.TransportAuto, internal.TransportTCP:
			// direct-TCP — the only path this command has.
		default:
			return fmt.Errorf("pty shell is direct-TCP only; use `cli pty exec --transport %s` for the via-visor path", eff)
		}
		dest = rest

		// If the destination omits a port, splice in --port. Doing it
		// here (not in the regex) avoids ambiguity with IPv6 literals.
		dest = injectDefaultPort(dest, sshDefPort)

		rPK, addr, err := parseSSHDestination(dest)
		if err != nil {
			return err
		}

		// --visor-key is the canonical spelling; the legacy --no-visor-key
		// is a hidden opt-out alias. --visor-key wins when both are set.
		useVisorKey := sshVisorKey
		if !cmd.Flags().Changed("visor-key") && cmd.Flags().Changed("no-visor-key") {
			useVisorKey = !sshNoVisor
		}
		myPK, mySK := resolveSSHIdentity(sshSK, useVisorKey)

		ctx, cancel := cmdutil.SignalContext(context.Background(), nil)
		defer cancel()

		cli := pty.DefaultCLI()

		// Interactive when no positional command follows.
		if len(args) == 1 {
			if sshNoPty {
				return fmt.Errorf("pty shell: --no-pty requires a command to exec")
			}
			return (&cli).StartRemotePtyTCP(ctx, rPK, addr, myPK, mySK, pty.DefaultCmd)
		}

		// Exec mode.
		timeout, err := time.ParseDuration(sshTimeout)
		if err != nil {
			return fmt.Errorf("pty shell: --timeout %q: %w", sshTimeout, err)
		}
		name := args[1]
		var cmdArgs []string
		if len(args) > 2 {
			cmdArgs = args[2:]
		}
		resp, err := (&cli).ExecRemoteTCP(ctx, rPK, addr, myPK, mySK, name, cmdArgs, sshEnv, nil, timeout)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "pty shell: %v\n", err) //nolint:errcheck
			os.Exit(1)
		}
		reportSSHExec(cmd, resp)
		return nil
	},
}

// parseSSHDestination splits `<pk>@<host>:<port>` (with optional
// `tcp://` prefix) into the remote PK and the dial-target address.
// The PK must be canonical 66-hex; addr is passed through to
// net.Dial verbatim. Any error message includes the offending
// destination string for operator readability.
func parseSSHDestination(dest string) (cipher.PubKey, string, error) {
	m := sshDestRE.FindStringSubmatch(dest)
	if m == nil {
		return cipher.PubKey{}, "", fmt.Errorf("pty shell: destination %q must be <66-hex-pk>@<host:port> (optionally prefixed tcp://)", dest)
	}
	var pk cipher.PubKey
	if err := pk.Set(m[1]); err != nil {
		return cipher.PubKey{}, "", fmt.Errorf("pty shell: destination PK invalid: %w", err)
	}
	return pk, m[2], nil
}

// injectDefaultPort splices a port into the destination if the user
// gave only `<pk>@<host>`. IPv6 literals must already carry their own
// `:port` because there's no unambiguous insertion point — we only
// inject when there's no `:` after the `@` boundary at all.
func injectDefaultPort(dest, defPort string) string {
	// Match the @-boundary; everything after must contain a colon to be
	// considered well-formed. If it does not, append :defPort.
	at := -1
	for i := 0; i < len(dest); i++ {
		if dest[i] == '@' {
			at = i
			break
		}
	}
	if at < 0 || at == len(dest)-1 {
		return dest // let parseSSHDestination produce the real error
	}
	host := dest[at+1:]
	for i := 0; i < len(host); i++ {
		if host[i] == ':' {
			return dest // already has a port (or IPv6 with port)
		}
	}
	return dest + ":" + defPort
}

// resolveSSHIdentity loads the client SK with this precedence:
//  1. --sk flag, when explicitly set (non-zero).
//  2. DMSGPTY_SK env var, when --sk is unset.
//  3. The local visor's SK from skywire-config.json, when
//     useVisorKey is true (the default — matches the user-friendly
//     "no separate identity to manage" feel of ssh + key agent).
//  4. A fresh random keypair, when none of the above resolved.
//
// Mirrors resolveTCPIdentity in cmd/skywire-cli/commands/dmsg/pty.go
// but defaults useVisorKey true (the existing --via-visor flag was
// opt-in; for the ssh-shaped command the visor-borrow default is the
// right discoverability choice).
func resolveSSHIdentity(skFlag cipher.SecKey, useVisorKey bool) (cipher.PubKey, cipher.SecKey) {
	var zero cipher.SecKey
	sk := skFlag
	if sk == zero {
		if env := os.Getenv("DMSGPTY_SK"); env != "" {
			_ = sk.Set(env) //nolint:errcheck,gosec
		}
	}
	if sk == zero && useVisorKey {
		confPath := visorconfig.SkywireConfig()
		conf, err := visorconfig.ReadFile(confPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "pty shell: --visor-key read %s: %v\n", confPath, err) //nolint:errcheck
			os.Exit(1)
		}
		if conf.SK == zero {
			fmt.Fprintf(os.Stderr, "pty shell: --visor-key: visor config %s has empty SK\n", confPath) //nolint:errcheck
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
		fmt.Fprintf(os.Stderr, "pty shell: failed to derive PK from --sk: %v\n", err) //nolint:errcheck
		os.Exit(1)
	}
	return pk, sk
}

// reportSSHExec is the exec-mode result-renderer. Mirrors
// reportExecResult in cmd/skywire-cli/commands/dmsg/pty.go but lives
// here so this command can stand alone if dmsg/pty.go is ever split
// into a separate binary. Local exit code mirrors the remote
// command's exit code (0 / N / 124 timeout / 1 RPC error).
func reportSSHExec(cmd *cobra.Command, resp *pty.CommandExecResult) {
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
