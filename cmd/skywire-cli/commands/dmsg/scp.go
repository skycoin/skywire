// Package clidmsg cmd/skywire-cli/commands/dmsg/scp.go: the
// `skywire cli dmsg scp` subcommand. Drives the dmsgscp client
// against either a peer's dmsgscp Host or the local visor's
// (loopback over its own dmsg PK).
//
// Direction is inferred from which argument carries a `PK:`
// prefix. The PK is 66 hex chars — long enough that the regex
// can't false-match a normal filename.
package clidmsg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgscp"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor"
)

// scpPKPathRe matches a `PK:path` argument. The PK is a 66-char
// lowercase-hex string (33 bytes encoded). The path captures
// everything after the colon (may be empty if the caller intends
// the rootDir itself, though our Host won't currently accept that).
var scpPKPathRe = regexp.MustCompile(`^([a-f0-9]{66}):(.*)$`)

var (
	scpPort      uint16
	scpTimeout   time.Duration
	scpTransport string
)

// scpTransportAuto is the default for --transport: try the local
// visor's VisorSCP RPC first (works for both dmsg and skynet) and
// fall back to a standalone-dmsg dial when no visor is reachable.
const (
	scpTransportAuto   = "auto"
	scpTransportDmsg   = "dmsg"
	scpTransportSkynet = "skynet"
)

func init() {
	scpCmd.Flags().SortFlags = false
	scpCmd.Flags().VarP(&sk, "sk", "s",
		"secret key for the standalone dmsg client (random if unset)")
	scpCmd.Flags().Uint16VarP(&scpPort, "port", "p", dmsgscp.DefaultPort,
		"remote dmsg port for the dmsgscp host")
	scpCmd.Flags().DurationVarP(&scpTimeout, "timeout", "t", 5*time.Minute,
		"transfer timeout (includes dial + payload)")
	scpCmd.Flags().StringVar(&scpTransport, "transport", scpTransportAuto,
		"transport: auto (try visor first, fallback dmsg) | dmsg | skynet")
	scpCmd.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr,
		"local visor RPC server address (env: SKYWIRE_RPC). Used for skynet / auto transports.")
	scpCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "fatal",
		"[ debug | warn | error | fatal | panic | trace | info ]")
	if env := os.Getenv("DMSG_SK"); env != "" {
		sk.Set(env) //nolint:errcheck,gosec
	}
}

var scpCmd = &cobra.Command{
	Use:   "scp <src> <dst>",
	Short: "Copy a file between this host and a remote visor over dmsg",
	Long: `DMSG scp — copy a single file between this machine and a remote
visor's dmsgscp Host. Exactly one of <src>/<dst> must carry a
` + "`PK:path`" + ` prefix (66-char hex public key + colon + path).

Examples:
  skywire cli dmsg scp ./local.bin <pk>:remote.bin       (upload)
  skywire cli dmsg scp <pk>:remote.bin ./local.bin       (download)

The remote path is interpreted relative to the host's configured
rootDir — absolute paths and ` + "`..`" + ` are rejected by the wire
parser. No size cap — large transfers stream through the underlying
transport without buffering the whole payload in memory.

Transport selection (--transport):
  - auto    (default) try the local visor's VisorSCP RPC first
            so the transfer rides whatever transport the visor
            already has up. Falls back to standalone dmsg when no
            visor is reachable on --rpc.
  - dmsg    standalone dmsg path — the CLI bootstraps its own
            dmsg.Client and dials peer:port directly. Works without
            a local visor.
  - skynet  routes the transfer over the skywire router via the
            visor's VisorSCP RPC. Requires a local visor with
            appnet TypeSkynet registered.

Identity: --sk gives the standalone dmsg client a stable PK so the
host's whitelist can authorize it. Without --sk you get a fresh
random PK each invocation and the host will reject you unless its
whitelist has been wide-opened. Identity only matters for the
standalone dmsg path; the VisorSCP RPC uses the visor's own PK.`,
	Args:                  cobra.ExactArgs(2),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		log := logging.MustGetLogger("dmsg-scp")
		if logLvl != "" {
			if lvl, err := logging.LevelFromString(logLvl); err == nil {
				logging.SetLevel(lvl)
			}
		}

		direction, peerPK, remotePath, localPath, err := classifyArgs(args[0], args[1])
		if err != nil {
			return err
		}

		switch scpTransport {
		case scpTransportAuto, scpTransportDmsg, scpTransportSkynet:
		default:
			return fmt.Errorf("dmsg scp: bad --transport %q (want auto|dmsg|skynet)", scpTransport)
		}

		// Skynet path requires the visor (CLI has no appnet runtime).
		if scpTransport == scpTransportSkynet {
			return runVisorSCP(visor.VisorSCPTransportSkynet, peerPK, direction, localPath, remotePath, cmd)
		}

		// Auto path: try the visor's RPC first. The visor's own PK is
		// stable + already on the peer's whitelist, so we save the
		// operator from threading --sk. Fall back to standalone dmsg
		// when --rpc is unreachable (typical for an operator running
		// the CLI on a host without a local visor).
		if scpTransport == scpTransportAuto {
			err := runVisorSCP(visor.VisorSCPTransportDmsg, peerPK, direction, localPath, remotePath, cmd)
			if err == nil {
				return nil
			}
			log.WithError(err).Debug("VisorSCP RPC failed; falling back to standalone dmsg")
		}

		// Standalone dmsg path — the CLI bootstraps its own
		// dmsg.Client and dials peer:port directly.
		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()
		ctx, timeoutCancel := context.WithTimeout(ctx, scpTimeout)
		defer timeoutCancel()

		myPK, mySK := resolveChatIdentity(sk)
		if !cmd.Flags().Changed("sk") && os.Getenv("DMSG_SK") == "" {
			fmt.Fprintf(os.Stderr,
				"WARN: ephemeral identity. Your PK is %s — the host's whitelist must accept this PK or the transfer will be rejected. Use --sk / DMSG_SK= for a stable identity.\n\n",
				myPK)
		}

		dmsgC, closeDmsg, err := startDmsgClient(ctx, log, myPK, mySK)
		if err != nil {
			return fmt.Errorf("dmsg client: %w", err)
		}
		defer closeDmsg()

		c, err := dmsgscp.Dial(ctx, dmsgC, peerPK, scpPort)
		if err != nil {
			return err
		}
		defer c.Close() //nolint:errcheck

		switch direction {
		case scpDirDownload:
			if err := c.Download(remotePath, localPath); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "downloaded %s:%s -> %s\n", peerPK, remotePath, localPath)
		case scpDirUpload:
			if err := c.Upload(localPath, remotePath); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "uploaded %s -> %s:%s\n", localPath, peerPK, remotePath)
		}
		return nil
	},
}

// runVisorSCP delegates the transfer to the local visor via the
// VisorSCP RPC. The visor opens the stream over the chosen
// transport (dmsg or skynet) and runs the dmsgscp protocol entirely
// inside its own process — bytes never traverse the CLI, only the
// initial request + completion ack go over the local RPC channel.
//
// LocalPath is normalized to an absolute path before sending so the
// visor (which doesn't share the CLI's cwd) reads/writes the
// expected file.
func runVisorSCP(transport string, peerPK cipher.PubKey, direction scpDirection, localPath, remotePath string, cmd *cobra.Command) error {
	abs, err := filepath.Abs(localPath)
	if err != nil {
		return fmt.Errorf("VisorSCP: resolve local path: %w", err)
	}
	dir := visor.VisorSCPUpload
	if direction == scpDirDownload {
		dir = visor.VisorSCPDownload
	}
	req := visor.VisorSCPRequest{
		RemotePK:   peerPK,
		Port:       scpPort,
		Direction:  dir,
		LocalPath:  abs,
		RemotePath: remotePath,
		Transport:  transport,
		Timeout:    scpTimeout,
	}
	rc, err := clirpc.Client(cmd.Flags())
	if err != nil {
		return fmt.Errorf("VisorSCP RPC client: %w", err)
	}
	if err := rc.VisorSCP(req); err != nil {
		return fmt.Errorf("VisorSCP (%s): %w", transport, err)
	}
	switch direction {
	case scpDirDownload:
		fmt.Fprintf(os.Stderr, "downloaded %s:%s -> %s (via visor %s)\n", peerPK, remotePath, abs, transport)
	case scpDirUpload:
		fmt.Fprintf(os.Stderr, "uploaded %s -> %s:%s (via visor %s)\n", abs, peerPK, remotePath, transport)
	}
	return nil
}

type scpDirection int

const (
	scpDirDownload scpDirection = iota
	scpDirUpload
)

// classifyArgs returns (direction, peerPK, remotePath, localPath).
// Exactly one of src/dst must carry a `PK:` prefix — both or
// neither is rejected.
func classifyArgs(src, dst string) (scpDirection, cipher.PubKey, string, string, error) {
	srcPK, srcPath, srcHasPK := splitPKPath(src)
	dstPK, dstPath, dstHasPK := splitPKPath(dst)

	switch {
	case srcHasPK && dstHasPK:
		return 0, cipher.PubKey{}, "", "",
			errors.New("dmsg scp: only one of <src>/<dst> may carry a PK: prefix")
	case !srcHasPK && !dstHasPK:
		return 0, cipher.PubKey{}, "", "",
			errors.New("dmsg scp: exactly one of <src>/<dst> must carry a PK: prefix")
	case srcHasPK:
		// PK on src = download.
		return scpDirDownload, srcPK, srcPath, dst, nil
	default:
		// PK on dst = upload.
		return scpDirUpload, dstPK, dstPath, src, nil
	}
}

// splitPKPath splits a `pkhex:path` argument. Returns (pk, path, true)
// on a match, or (_, _, false) if the argument is a plain local path.
// A leading `PK:` with the right shape is required — we don't fall
// back to "looks like a hex string" heuristics.
func splitPKPath(s string) (cipher.PubKey, string, bool) {
	m := scpPKPathRe.FindStringSubmatch(s)
	if m == nil {
		return cipher.PubKey{}, "", false
	}
	var pk cipher.PubKey
	if err := pk.Set(m[1]); err != nil {
		// PK validation failed — surface as "no match" rather than
		// erroring here; the regex should have caught any bad shape
		// but the underlying cipher.PubKey.Set may add e.g. tweak
		// checks that the regex doesn't.
		return cipher.PubKey{}, "", false
	}
	// Strip any leading "./" the user might have typed defensively.
	path := strings.TrimPrefix(m[2], "./")
	return pk, path, true
}
