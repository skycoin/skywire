// Package clidmsg cmd/skywire-cli/commands/dmsg/cat.go: the
// `skywire cli dmsg cat` and `skywire cli dmsg cat listen`
// subcommands. Splice stdio with a remote peer over either dmsg or
// the skywire router.
//
// Client form  : `cli dmsg cat <pk>:<port>` — opens a stream, copies
//
//	stdin → stream and stream → stdout, exits when
//	either side EOFs.
//
// Listen form  : `cli dmsg cat listen <port>` — accepts one inbound
//
//	stream (after a whitelist check), then splices
//	stdio. One-shot by design; multi-conn into the
//	same stdout would interleave bytes meaninglessly.
//
// The client form has a standalone-dmsg fallback so it works without
// a local visor; the listen form does not — listening requires the
// dmsgscp whitelist, which the visor owns.
package clidmsg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	catTransport string
	catTimeout   time.Duration
	catRoutes    int
	catVerbose   bool
)

// catTransport* mirror scp's set so the same --transport flag value
// keeps the same meaning across both commands.
const (
	catTransportAuto   = "auto"
	catTransportDmsg   = "dmsg"
	catTransportSkynet = "skynet"
)

func init() {
	catCmd.Flags().SortFlags = false
	catCmd.Flags().StringVar(&catTransport, "transport", catTransportAuto,
		"transport: auto (try visor first, fallback dmsg) | dmsg | skynet")
	catCmd.Flags().DurationVarP(&catTimeout, "timeout", "t", 60*time.Second,
		"dial / accept timeout")
	catCmd.Flags().IntVar(&catRoutes, "routes", 1,
		"number of skynet routes (future-use; skynet transport only)")
	catCmd.Flags().BoolVarP(&catVerbose, "verbose", "v", false,
		"print connection info to stderr")
	catCmd.Flags().VarP(&sk, "sk", "s",
		"secret key for the standalone dmsg client fallback (random if unset)")
	catCmd.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr,
		"local visor RPC server address (env: SKYWIRE_RPC)")
	catCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "fatal",
		"[ debug | warn | error | fatal | panic | trace | info ]")

	catListenCmd.Flags().SortFlags = false
	catListenCmd.Flags().DurationVarP(&catTimeout, "timeout", "t", 5*time.Minute,
		"accept timeout — how long to wait for an inbound stream")
	catListenCmd.Flags().BoolVarP(&catVerbose, "verbose", "v", false,
		"print connection info to stderr")
	catListenCmd.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr,
		"local visor RPC server address (env: SKYWIRE_RPC)")
	catListenCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "fatal",
		"[ debug | warn | error | fatal | panic | trace | info ]")

	catCmd.AddCommand(catListenCmd)
}

var catCmd = &cobra.Command{
	Use:   "cat <pk>:<port>",
	Short: "Splice stdio with a remote peer over dmsg or skynet",
	Long: `DMSG cat — open a stream to a remote peer and splice the
local process's stdio with it. stdin flows to the peer, the peer's
output flows to stdout. Exits when either side EOFs.

Examples:
  echo hello | skywire cli dmsg cat <pk>:1234
  skywire cli dmsg cat <pk>:1234 < payload.bin > response.bin

Transport selection (--transport):
  - auto    (default) try the local visor's VisorCat RPC first so
            the stream rides whatever transport the visor has up.
            Falls back to standalone dmsg when no visor is reachable
            on --rpc.
  - dmsg    standalone dmsg path — the CLI bootstraps its own
            dmsg.Client and dials peer:port directly. Works without
            a local visor.
  - skynet  routes the stream over the skywire router via the
            visor's VisorCat RPC. Requires a local visor with
            appnet TypeSkynet registered.

Identity: --sk gives the standalone dmsg client a stable PK so the
remote's whitelist can authorize it. Without --sk you get a fresh
random PK each invocation. Identity only matters for the standalone
dmsg path; the VisorCat RPC uses the visor's own PK.`,
	Args:                  cobra.ExactArgs(1),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		log := logging.MustGetLogger("dmsg-cat")
		if logLvl != "" {
			if lvl, err := logging.LevelFromString(logLvl); err == nil {
				logging.SetLevel(lvl)
			}
		}

		peerPK, port, err := splitPKPort(args[0])
		if err != nil {
			return err
		}

		switch catTransport {
		case catTransportAuto, catTransportDmsg, catTransportSkynet:
		default:
			return fmt.Errorf("dmsg cat: bad --transport %q (want auto|dmsg|skynet)", catTransport)
		}

		// Skynet path requires the visor — CLI has no appnet runtime.
		if catTransport == catTransportSkynet {
			return runVisorCatDial(visor.VisorCatTransportSkynet, peerPK, port, cmd)
		}

		// Auto path: try the visor's RPC first. The visor's own PK is
		// stable + already on the peer's whitelist, so we save the
		// operator from threading --sk.
		if catTransport == catTransportAuto {
			err := runVisorCatDial(visor.VisorCatTransportDmsg, peerPK, port, cmd)
			if err == nil {
				return nil
			}
			log.WithError(err).Debug("VisorCat RPC failed; falling back to standalone dmsg")
		}

		// Standalone dmsg path.
		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()
		ctx, timeoutCancel := context.WithTimeout(ctx, catTimeout)
		defer timeoutCancel()

		myPK, mySK := resolveChatIdentity(sk)
		if !cmd.Flags().Changed("sk") && os.Getenv("DMSG_SK") == "" {
			fmt.Fprintf(os.Stderr,
				"WARN: ephemeral identity. Your PK is %s — the remote's whitelist must accept this PK or the stream will be rejected. Use --sk for a stable identity.\n",
				myPK)
		}

		dmsgC, closeDmsg, err := startDmsgClient(ctx, log, myPK, mySK)
		if err != nil {
			return fmt.Errorf("dmsg client: %w", err)
		}
		defer closeDmsg()

		stream, err := dmsgC.DialStream(ctx, dmsg.Addr{PK: peerPK, Port: port})
		if err != nil {
			return fmt.Errorf("dial %s:%d: %w", peerPK, port, err)
		}
		defer stream.Close() //nolint:errcheck

		if catVerbose {
			fmt.Fprintf(os.Stderr, "connected to %s:%d (standalone dmsg)\n", peerPK, port)
		}
		return spliceStdio(stream)
	},
}

var catListenCmd = &cobra.Command{
	Use:   "listen <port>",
	Short: "Accept one inbound stream and splice stdio with it",
	Long: `DMSG cat listen — bind both a dmsg and a skynet listener on
<port>, authorize the first inbound peer against the dmsgscp
whitelist, then splice the local process's stdio with the accepted
stream. The first stream to arrive on either transport wins; the
other listener is closed.

Auth is identical to dmsgscp's: the peer's PK must be in the visor's
Dmsgscp.Whitelist (or, when empty, Dmsgpty.Whitelist), plus any
configured hypervisors, plus the visor's own PK.

One-shot: the listener exits after the first stream completes — a
persistent listener feeding multiple peers into the same stdout
would interleave bytes meaninglessly.

Examples:
  skywire cli dmsg cat listen 1234 > captured.bin
  cat payload.bin | skywire cli dmsg cat listen 1234

This command requires a local visor on --rpc; there is no standalone
fallback because listening requires the dmsgscp whitelist.`,
	Args:                  cobra.ExactArgs(1),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		port64, err := strconv.ParseUint(args[0], 10, 16)
		if err != nil {
			return fmt.Errorf("dmsg cat listen: bad port %q: %w", args[0], err)
		}
		port := uint16(port64)

		return runVisorCatListen(port, cmd)
	},
}

// runVisorCatDial delegates the dial to the visor and dials the
// returned loopback to splice stdio.
func runVisorCatDial(transport string, peerPK cipher.PubKey, port uint16, cmd *cobra.Command) error {
	// Drop the default 30s RPC call timeout — VisorCat splices stdio
	// to a remote stream and the RPC handler doesn't return until the
	// caller's connection EOFs. Any cat session past 30s would hit the
	// global cap. catTimeout (the --timeout flag) bounds the visor-side
	// work. Same pattern as runVisorSCP (#2543).
	prevTimeout := clirpc.Timeout
	clirpc.Timeout = 0
	defer func() { clirpc.Timeout = prevTimeout }()
	rc, err := clirpc.Client(cmd.Flags())
	if err != nil {
		return fmt.Errorf("VisorCat RPC client: %w", err)
	}
	req := visor.VisorCatRequest{
		Mode:      visor.VisorCatModeDial,
		RemotePK:  peerPK,
		Port:      port,
		Transport: transport,
		Timeout:   catTimeout,
	}
	resp, err := rc.VisorCat(req)
	if err != nil {
		return fmt.Errorf("VisorCat (%s): %w", transport, err)
	}
	conn, err := net.Dial("tcp", resp.LocalAddr)
	if err != nil {
		return fmt.Errorf("dial loopback %s: %w", resp.LocalAddr, err)
	}
	defer conn.Close() //nolint:errcheck
	if catVerbose {
		fmt.Fprintf(os.Stderr, "connected to %s:%d (via visor %s)\n", peerPK, port, transport)
	}
	return spliceStdio(conn)
}

// runVisorCatListen delegates the listen to the visor and dials the
// returned loopback once the visor has accepted an inbound stream.
// The visor always binds BOTH dmsg and skynet listeners — there is no
// transport choice at this layer.
func runVisorCatListen(port uint16, cmd *cobra.Command) error {
	// Drop the default 30s RPC call timeout — same reasoning as
	// runVisorCatDial. Listen sessions trivially outlast 30s while
	// waiting for an inbound peer.
	prevTimeout := clirpc.Timeout
	clirpc.Timeout = 0
	defer func() { clirpc.Timeout = prevTimeout }()
	rc, err := clirpc.Client(cmd.Flags())
	if err != nil {
		return fmt.Errorf("VisorCat RPC client: %w", err)
	}
	req := visor.VisorCatRequest{
		Mode:    visor.VisorCatModeListen,
		Port:    port,
		Timeout: catTimeout,
	}
	if catVerbose {
		fmt.Fprintf(os.Stderr, "listening on dmsg+skynet :%d (via visor)\n", port)
	}
	resp, err := rc.VisorCat(req)
	if err != nil {
		return fmt.Errorf("VisorCat listen: %w", err)
	}
	conn, err := net.Dial("tcp", resp.LocalAddr)
	if err != nil {
		return fmt.Errorf("dial loopback %s: %w", resp.LocalAddr, err)
	}
	defer conn.Close() //nolint:errcheck
	if catVerbose {
		fmt.Fprintf(os.Stderr, "stream accepted; splicing stdio\n")
	}
	return spliceStdio(conn)
}

// splitPKPort parses a `pkhex:port` argument used by `cli dmsg cat`.
// Distinct from splitPKPath in scp.go because cat's RHS is a numeric
// port (uint16), not a filesystem path.
func splitPKPort(s string) (cipher.PubKey, uint16, error) {
	// Find the first colon that separates the 66-char hex PK from
	// the port. We can't just SplitN because a stray colon early in
	// the string would mis-classify — anchor on the 66-char PK shape.
	if len(s) < 67 || s[66] != ':' {
		return cipher.PubKey{}, 0, fmt.Errorf("dmsg cat: expected <pk>:<port>, got %q", s)
	}
	var pk cipher.PubKey
	if err := pk.Set(s[:66]); err != nil {
		return cipher.PubKey{}, 0, fmt.Errorf("dmsg cat: bad PK: %w", err)
	}
	port64, err := strconv.ParseUint(s[67:], 10, 16)
	if err != nil {
		return cipher.PubKey{}, 0, fmt.Errorf("dmsg cat: bad port: %w", err)
	}
	return pk, uint16(port64), nil
}

// spliceStdio runs bidirectional io.Copy between conn and the
// process's stdio. Returns when either side EOFs or errors. The
// stdin → conn half drives close-on-eof of conn so the remote sees
// our stdin close.
func spliceStdio(conn net.Conn) error {
	var (
		once    sync.Once
		closeFn = func() { _ = conn.Close() }
	)
	done := make(chan error, 2)
	go func() {
		_, err := io.Copy(conn, os.Stdin)
		// Half-close the conn write side so the remote sees EOF on
		// its read half. Plain Close is the portable approximation —
		// dmsg.Stream and TCP both surface it as EOF on the peer's
		// next read.
		once.Do(closeFn)
		done <- err
	}()
	go func() {
		_, err := io.Copy(os.Stdout, conn)
		once.Do(closeFn)
		done <- err
	}()
	first := <-done
	// Drain the second goroutine.
	<-done
	if first == nil || errors.Is(first, io.EOF) || errors.Is(first, net.ErrClosed) {
		return nil
	}
	return first
}
