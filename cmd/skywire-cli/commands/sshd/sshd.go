// Package clisshd cmd/skywire-cli/commands/sshd/sshd.go — `skywire cli
// sshd`, the OpenSSH-equivalent server over skywire identity. Thin
// wrapper around dmsgpty.Host's direct-TCP entry point (PR #2559)
// with sensible defaults for the standalone-daemon shape:
//
//   - TCP-only: no dmsg.Client, no dmsg listener (the visor or the
//     standalone dmsgpty-host already handle the overlay path; sshd
//     is for the direct-TCP case operators reach for ssh-style).
//   - Identity defaulted to the local visor's SK so operators don't
//     manage a separate keypair; --sk-from-visor accepts an explicit
//     path, otherwise the default skywire-config.json is read.
//   - Whitelist read from the dmsgpty config or supplied inline with
//     --allow <pk>,<pk>; same data shape as authorized_keys.
//
// For dual-mode (dmsg + TCP) operators, use the visor's embedded
// Dmsgpty.SshListen or the lower-level `dmsgpty-host` binary directly.
// This command is intentionally narrow.
package clisshd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgpty"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// jsonDecode is a tiny wrapper over encoding/json that doesn't
// require importing dmsgpty's jsoniter alias just for this one call.
func jsonDecode(r io.Reader, v interface{}) error {
	return json.NewDecoder(r).Decode(v)
}

// Flags.
var (
	sshdListen     string
	sshdSKFromConf string
	sshdAllow      []string
	sshdConfPath   string
	sshdLog        string
)

func init() {
	RootCmd.Flags().SortFlags = false
	RootCmd.Flags().StringVarP(&sshdListen, "listen", "l", ":2022",
		"TCP listen address (e.g. :2022, 0.0.0.0:2022, 127.0.0.1:2022)")
	RootCmd.Flags().StringVar(&sshdSKFromConf, "sk-from-visor", "",
		"path to a skywire-config.json whose .sk we use as the server identity; empty means try the default "+visorconfig.SkywireConfig())
	RootCmd.Flags().StringSliceVar(&sshdAllow, "allow", nil,
		"comma-separated client PKs that may connect (authorized_keys equivalent); merged with --conf's whitelist if both are set")
	RootCmd.Flags().StringVarP(&sshdConfPath, "conf", "c", "",
		"optional dmsgpty config.json with whitelist + identity; --allow + --sk-from-visor override matching fields")
	RootCmd.Flags().StringVar(&sshdLog, "log-level", "info",
		"log level: trace|debug|info|warn|error")
}

// RootCmd is `skywire cli sshd` — OpenSSH-equivalent server.
//
// Registered under skywire-cli's root alongside `cli ssh` (client).
// Compared to OpenSSH's sshd:
//
//	sshd_config Port      ↔ --listen
//	authorized_keys       ↔ --allow / --conf's whitelist
//	host key              ↔ --sk-from-visor (or the default visor SK)
//	HostbasedAuthentication ↔ noise XK pins both sides via PK
var RootCmd = &cobra.Command{
	Use:   "sshd",
	Short: "Run a direct-TCP dmsgpty server (ssh-equivalent daemon)",
	Long: `skywire cli sshd — OpenSSH-equivalent server over skywire identity.

Binds a TCP port and serves the dmsgpty protocol over it, gated by an
XK noise handshake against this server's PK + a whitelist of client
PKs. The protocol underneath is identical to the dmsg-overlay
dmsgpty-host; sshd is the discoverable, ssh-shaped surface over the
direct-TCP entry point.

Use this when:
  - You want a peer-to-peer remote shell that doesn't require running
    a full skywire visor on the server (or that runs ALONGSIDE one,
    sharing the visor's PK without contending for the dmsg-disc entry).
  - You have direct IP reachability to the server and want lower
    latency than the dmsg overlay path.
  - You're scripting ssh-like access into your skywire fleet.

Use the visor's embedded dmsgpty.ssh_listen field instead when:
  - You want the same port served by your already-running visor.
  - You want the dmsg-overlay path AND the TCP path on the same
    process (sshd is TCP-only by design).

Examples:
  # Run on :2022 with two PKs allowed, identity from default visor config:
  skywire cli sshd --listen :2022 --allow 02f9...,03d1...

  # Use a non-default visor config for the server identity:
  skywire cli sshd --listen :2022 --sk-from-visor /etc/skywire/skywire.json --allow 02f9...

  # Use a standalone dmsgpty config.json for both identity AND whitelist:
  skywire cli sshd --listen :2022 --conf /etc/skywire/dmsgpty.json`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if sshdListen == "" {
			return fmt.Errorf("sshd: --listen is required")
		}

		log := logging.MustGetLogger("sshd")
		if lvl, err := logging.LevelFromString(sshdLog); err == nil {
			logging.SetLevel(lvl)
		}

		// Resolve server identity (the "host key"). Precedence:
		// 1. --conf <path>'s .sk if present.
		// 2. --sk-from-visor <path>'s .sk if set, else the default
		//    visor config's .sk.
		sk, err := resolveServerSK(sshdConfPath, sshdSKFromConf)
		if err != nil {
			return err
		}
		pk, err := sk.PubKey()
		if err != nil {
			return fmt.Errorf("sshd: derive PK from SK: %w", err)
		}

		// Build the whitelist. --allow is the inline form; --conf
		// supplies the file-based form; both are unioned.
		wl, err := buildSshdWhitelist(sshdConfPath, sshdAllow)
		if err != nil {
			return err
		}

		// Standalone-TCP host: nil dmsg.Client. The TCP entry point
		// in pkg/dmsg/dmsgpty/host_tcp.go nil-guards the dmsgC
		// access at #2569, so this is supported.
		host := dmsgpty.NewHost(nil, wl)

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()

		log.WithField("addr", sshdListen).
			WithField("pk", pk.String()).
			WithField("whitelist_size", len(sshdAllow)).
			Info("sshd: listening")

		var wg sync.WaitGroup
		wg.Add(1)
		errCh := make(chan error, 1)
		go func() {
			defer wg.Done()
			errCh <- host.ListenAndServeTCP(ctx, sshdListen, pk, sk)
		}()

		select {
		case <-ctx.Done():
			log.Info("sshd: shutting down on signal")
		case err := <-errCh:
			if err != nil {
				return fmt.Errorf("sshd: listen %s: %w", sshdListen, err)
			}
		}
		wg.Wait()
		return nil
	},
}

// resolveServerSK loads the server's SK with this precedence:
//  1. If confPath is set and its .sk is non-empty, use that (a
//     standalone dmsgpty config.json).
//  2. Otherwise, if skFromConfPath is set, read .sk from that path
//     (a skywire-config.json).
//  3. Otherwise, fall back to the default skywire-config.json
//     (visorconfig.SkywireConfig()). This is the friendly default —
//     operators with a visor installed don't need extra flags.
func resolveServerSK(confPath, skFromConfPath string) (cipher.SecKey, error) {
	var zero cipher.SecKey
	if confPath != "" {
		c, err := readDmsgptyConf(confPath)
		if err == nil && c.SK != "" {
			var sk cipher.SecKey
			if err := sk.Set(c.SK); err != nil {
				return zero, fmt.Errorf("sshd: --conf %s sk invalid: %w", confPath, err)
			}
			return sk, nil
		}
	}
	path := skFromConfPath
	if path == "" {
		path = visorconfig.SkywireConfig()
	}
	conf, err := visorconfig.ReadFile(path)
	if err != nil {
		return zero, fmt.Errorf("sshd: read visor config %s: %w (use --sk-from-visor or --conf)", path, err)
	}
	if conf.SK == zero {
		return zero, fmt.Errorf("sshd: visor config %s has empty SK", path)
	}
	return conf.SK, nil
}

// buildSshdWhitelist merges --allow (inline) and --conf's WL (file).
// Returns an in-memory whitelist (dmsgpty.NewMemoryWhitelist) so
// the host doesn't write back to the conf file on the
// allow/disallow RPCs the CLI exposes for the dmsg-overlay path.
//
// Returns an error rather than an empty whitelist when nothing is
// specified — refusing to start an open server is the safer default
// (anyone with the server's address + a fresh keypair could otherwise
// connect, since noise alone doesn't authorize, only the wl does).
func buildSshdWhitelist(confPath string, inline []string) (dmsgpty.Whitelist, error) {
	keys := make(map[cipher.PubKey]struct{})

	if confPath != "" {
		c, err := readDmsgptyConf(confPath)
		if err != nil {
			return nil, fmt.Errorf("sshd: --conf %s: %w", confPath, err)
		}
		for _, s := range c.WL {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			var pk cipher.PubKey
			if err := pk.Set(s); err != nil {
				return nil, fmt.Errorf("sshd: --conf %s: invalid pk %q: %w", confPath, s, err)
			}
			keys[pk] = struct{}{}
		}
	}

	for _, raw := range inline {
		for _, s := range strings.Split(raw, ",") {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			var pk cipher.PubKey
			if err := pk.Set(s); err != nil {
				return nil, fmt.Errorf("sshd: --allow %q invalid: %w", s, err)
			}
			keys[pk] = struct{}{}
		}
	}

	if len(keys) == 0 {
		return nil, fmt.Errorf("sshd: refusing to start with an empty whitelist (use --allow <pk>,... or --conf <path>)")
	}

	// Materialize into an in-memory whitelist.
	pks := make([]cipher.PubKey, 0, len(keys))
	for pk := range keys {
		pks = append(pks, pk)
	}
	wl := dmsgpty.NewMemoryWhitelist()
	if err := wl.Add(pks...); err != nil {
		return nil, fmt.Errorf("sshd: build whitelist: %w", err)
	}
	return wl, nil
}

// readDmsgptyConf reads a standalone dmsgpty config.json. Wraps
// the public DefaultConfig to avoid duplicating the JSON shape.
func readDmsgptyConf(path string) (dmsgpty.Config, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return dmsgpty.Config{}, err
	}
	defer f.Close() //nolint:errcheck
	conf := dmsgpty.DefaultConfig()
	if err := jsonDecode(f, &conf); err != nil {
		return dmsgpty.Config{}, err
	}
	return conf, nil
}
