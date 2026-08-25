// Package commands cmd/dmsg/dmsgprobe/commands/dmsgprobe.go c1-net-dmsg
package commands

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/dmsg/tcpnoise"
	"github.com/skycoin/skywire/pkg/logging"
)

var (
	sk          cipher.SecKey
	probeServer string
	probeVia    string
	logLvl      string
	plog        = logging.MustGetLogger("dmsgprobe")
)

func init() {
	RootCmd.Flags().SortFlags = false
	dmsgclient.InitFlags(RootCmd)
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "fatal", "[ debug | warn | error | fatal | panic | trace | info ]")
	RootCmd.Flags().StringVar(&probeServer, "server", "", "force the probe through this specific dmsg server (pk hex)")
	RootCmd.Flags().StringVar(&probeVia, "via", "", "probe a direct noise-TCP connection instead of dmsg: tcp://<pk>@host:port")
	if os.Getenv("DMSG_SK") != "" {
		sk.Set(os.Getenv("DMSG_SK")) //nolint
	}
	RootCmd.Flags().VarP(&sk, "sk", "s", "secret key to use (a random key is generated if unspecified)")
}

// RootCmd is the standalone `dmsg probe` command. It is the standalone
// (no-visor) counterpart to `skywire cli dmsg probe`: same reachability
// check, but it always bootstraps its own dmsg client (mirrors the
// dmsgcurl ↔ `cli dmsg curl` split). There is no RPC mode here because a
// standalone binary has no visor to ask.
var RootCmd = &cobra.Command{
	Use:   "probe <public-key> <port>",
	Short: "Standalone DMSG port-reachability probe",
	Long: calvin.AsciiFont("dmsg-probe") + `
	Standalone DMSG port-reachability probe — its own dmsg client, no visor.

	Performs a full noise handshake to <pk>:<port> over dmsg; a completed
	handshake means a listener is active (reachable). With --server it forces
	the probe through a specific dmsg server. With --via tcp://<pk>@host:port
	it instead probes a direct noise-TCP connection (no dmsg at all).`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Args: func(cmd *cobra.Command, args []string) error {
		if probeVia != "" {
			return nil
		}
		return cobra.ExactArgs(2)(cmd, args)
	},
	RunE: func(_ *cobra.Command, args []string) error {
		if logLvl != "" {
			if lvl, e := logging.LevelFromString(logLvl); e == nil {
				logging.SetLevel(lvl)
			}
		}

		// Identity: --sk / DMSG_SK, else a fresh ephemeral pair.
		pk, err := sk.PubKey()
		if err != nil {
			_, sk = cipher.GenerateKeyPair()
			pk, err = sk.PubKey()
			if err != nil {
				return fmt.Errorf("derive public key: %w", err)
			}
		}

		// Direct noise-TCP connection probe (no dmsg, no router).
		if probeVia != "" {
			rPK, hostPort, perr := parseViaTCP(probeVia)
			if perr != nil {
				return perr
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			startT := time.Now()
			conn, derr := tcpnoise.Dial(ctx, hostPort, pk, sk, rPK)
			lat := time.Since(startT).Round(time.Millisecond)
			if derr != nil {
				fmt.Printf("tcp://%s@%s — unreachable (%s)\n", rPK, hostPort, lat)
				return nil
			}
			_ = conn.Close() //nolint:errcheck
			fmt.Printf("tcp://%s@%s — reachable (%s)\n", rPK, hostPort, lat)
			return nil
		}

		var dpk cipher.PubKey
		if e := dpk.Set(args[0]); e != nil {
			return fmt.Errorf("invalid public key: %w", e)
		}
		p64, e := strconv.ParseUint(args[1], 10, 16)
		if e != nil {
			return fmt.Errorf("invalid port: %w", e)
		}
		port := uint16(p64)

		var serverPK cipher.PubKey
		serverSet := probeServer != ""
		if serverSet {
			if e := serverPK.Set(probeServer); e != nil {
				return fmt.Errorf("invalid --server pk: %w", e)
			}
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), plog)
		defer cancel()
		probeCtx, probeCancel := context.WithTimeout(ctx, 30*time.Second)
		defer probeCancel()

		dest := "dmsg://" + dpk.Hex() + ":" + strconv.Itoa(int(port))
		dmsgC, stop, err := dmsgclient.InitDmsgWithFlags(probeCtx, plog, pk, sk, &http.Client{}, dest)
		if err != nil || dmsgC == nil {
			return fmt.Errorf("failed to start dmsg client: %w", err)
		}
		defer stop()

		startT := time.Now()
		var reachable bool
		if serverSet {
			reachable = dmsgC.ProbeViaServer(probeCtx, dpk, port, serverPK)
		} else {
			reachable = dmsgC.Probe(probeCtx, dpk, port)
		}
		lat := time.Since(startT).Round(time.Millisecond)

		suffix := ""
		if serverSet {
			suffix = " (via " + probeServer[:min(8, len(probeServer))] + "…)"
		}
		state := "unreachable"
		if reachable {
			state = "reachable"
		}
		fmt.Printf("dmsg://%s:%d%s — %s (%s)\n", dpk, port, suffix, state, lat)
		return nil
	},
}

// parseViaTCP turns "tcp://<66-hex-pk>@host:port" into (pk, "host:port").
func parseViaTCP(spec string) (cipher.PubKey, string, error) {
	const prefix = "tcp://"
	if !strings.HasPrefix(spec, prefix) {
		return cipher.PubKey{}, "", fmt.Errorf("--via must start with %q", prefix)
	}
	rest := spec[len(prefix):]
	at := strings.IndexByte(rest, '@')
	if at <= 0 || at >= len(rest)-1 {
		return cipher.PubKey{}, "", fmt.Errorf("--via tcp: expected tcp://<pk>@host:port, got %q", spec)
	}
	var pk cipher.PubKey
	if err := pk.Set(rest[:at]); err != nil {
		return cipher.PubKey{}, "", fmt.Errorf("--via tcp: invalid pk: %w", err)
	}
	return pk, rest[at+1:], nil
}

// Execute executes the RootCmd.
func Execute() {
	dmsgclient.Execute(RootCmd)
}
