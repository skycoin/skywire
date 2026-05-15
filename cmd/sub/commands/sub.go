// Package commands cmd/sub/commands/sub.go
//
// Standalone (dmsg-only) UDP→dmsg bridge ("sub" — sky UDP bridge).
// Wraps UDP datagrams in length-prefixed frames, ferries them over
// a dmsg stream, and replays them as UDP on the peer. Mirrors the
// smb shape — one binary, no visor dependency, own dmsg.Client.
//
// Two subcommands:
//
//	sub client   — local UDP listener → peer-dialled dmsg stream
//	sub server   — accept dmsg streams → forward as UDP to target
//
// Plan B for UDP-over-skynet (TCP-over-UDP, reliable + in-order).
// For media-class UDP (RTP, voice, game) use Plan A — the
// packet-level UDP at the route-group layer (RFC #2607).
package commands

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyudpbridge"
)

var (
	// shared
	skHex    string
	skFile   string
	dmsgDisc string
	logLevel string

	// client
	clientListen string
	clientPeer   string
	clientPort   uint16
	clientIdle   time.Duration
	clientDial   time.Duration

	// server
	serverDmsgPort uint16
	serverTarget   string
	serverIdle     time.Duration
)

// RootCmd is the cobra entry for the standalone UDP→dmsg bridge.
// Used both as the top-level binary (cmd/sub) and the integrated
// subcommand `skywire dmsg sub`.
var RootCmd = &cobra.Command{
	Use:                   "sub",
	Short:                 "Standalone UDP→dmsg bridge — ferries length-prefixed UDP datagrams over dmsg streams",
	Long:                  calvin.AsciiFont("sub"),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
}

var clientCmd = &cobra.Command{
	Use:           "client",
	Short:         "Listen UDP locally, frame + ferry to a peer over dmsg",
	SilenceErrors: true,
	SilenceUsage:  true,
	Run: func(cmd *cobra.Command, _ []string) {
		ctx, cancel := signalCtx(cmd.Context())
		defer cancel()
		if err := runClient(ctx); err != nil {
			log.Fatal(err)
		}
	},
}

var serverCmd = &cobra.Command{
	Use:           "server",
	Short:         "Accept dmsg streams, unframe, forward as UDP to a local target",
	SilenceErrors: true,
	SilenceUsage:  true,
	Run: func(cmd *cobra.Command, _ []string) {
		ctx, cancel := signalCtx(cmd.Context())
		defer cancel()
		if err := runServer(ctx); err != nil {
			log.Fatal(err)
		}
	},
}

func init() {
	for _, c := range []*cobra.Command{clientCmd, serverCmd} {
		c.Flags().StringVar(&skHex, "sk", "", "secret key hex (env: SKYUDP_SK). Empty + no --sk-file → ephemeral keypair")
		c.Flags().StringVar(&skFile, "sk-file", "", "path to a file containing the secret key hex (newline-trimmed)")
		c.Flags().StringVar(&dmsgDisc, "dmsg-disc", "", "dmsg discovery URL (defaults to deployment.Prod.DmsgDiscovery)")
		c.Flags().StringVar(&logLevel, "log-level", "info", "log level: debug|info|warn|error")
	}

	clientCmd.Flags().StringVar(&clientListen, "listen", "127.0.0.1:5353", "local UDP listen address")
	clientCmd.Flags().StringVar(&clientPeer, "peer", "", "peer visor public key hex (required)")
	clientCmd.Flags().Uint16Var(&clientPort, "remote-port", 0, "dmsg port to dial on the peer (required)")
	clientCmd.Flags().DurationVar(&clientIdle, "idle", skyudpbridge.DefaultIdleTimeout, "per-source flow idle timeout")
	clientCmd.Flags().DurationVar(&clientDial, "dial-timeout", skyudpbridge.DefaultDialTimeout, "peer-dial timeout")

	serverCmd.Flags().Uint16Var(&serverDmsgPort, "dmsg-port", 0, "dmsg port to listen on (required)")
	serverCmd.Flags().StringVar(&serverTarget, "target", "", "local UDP target the unframed datagrams are forwarded to (required, e.g. 127.0.0.1:53)")
	serverCmd.Flags().DurationVar(&serverIdle, "idle", skyudpbridge.DefaultIdleTimeout, "per-stream UDP-socket idle timeout")

	RootCmd.AddCommand(clientCmd, serverCmd)
}

// Execute is invoked from the package-main entrypoint.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

func signalCtx(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		cancel()
	}()
	return ctx, cancel
}

func runClient(ctx context.Context) error {
	if clientPeer == "" {
		return errors.New("--peer is required")
	}
	if clientPort == 0 {
		return errors.New("--remote-port is required")
	}
	var peer cipher.PubKey
	if err := peer.UnmarshalText([]byte(clientPeer)); err != nil {
		return fmt.Errorf("parse --peer: %w", err)
	}

	logger := initLogger("sub-client")
	pk, sk, err := resolveKeypair()
	if err != nil {
		return fmt.Errorf("resolve keypair: %w", err)
	}
	logger.WithField("pk", pk.Hex()).Info("dmsg identity")

	dmsgC, stop, err := startDmsgClient(ctx, logger, pk, sk)
	if err != nil {
		return fmt.Errorf("start dmsg client: %w", err)
	}
	defer stop()

	cfg := skyudpbridge.ClientConfig{
		ListenUDP:   clientListen,
		Peer:        peer,
		PeerPort:    clientPort,
		IdleTimeout: clientIdle,
		DialTimeout: clientDial,
	}
	logger.Infof("sub client listening UDP %s → dmsg %s:%d", cfg.ListenUDP, peer.Hex()[:8], cfg.PeerPort)
	if err := skyudpbridge.RunClient(ctx, cfg, &dmsgDialer{c: dmsgC}, logger); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func runServer(ctx context.Context) error {
	if serverDmsgPort == 0 {
		return errors.New("--dmsg-port is required")
	}
	if serverTarget == "" {
		return errors.New("--target is required")
	}

	logger := initLogger("sub-server")
	pk, sk, err := resolveKeypair()
	if err != nil {
		return fmt.Errorf("resolve keypair: %w", err)
	}
	logger.WithField("pk", pk.Hex()).Info("dmsg identity")

	dmsgC, stop, err := startDmsgClient(ctx, logger, pk, sk)
	if err != nil {
		return fmt.Errorf("start dmsg client: %w", err)
	}
	defer stop()

	lis, err := dmsgC.Listen(serverDmsgPort)
	if err != nil {
		return fmt.Errorf("dmsg listen :%d: %w", serverDmsgPort, err)
	}
	go func() {
		<-ctx.Done()
		_ = lis.Close() //nolint:errcheck,gosec
	}()

	cfg := skyudpbridge.ServerConfig{
		TargetUDP:   serverTarget,
		IdleTimeout: serverIdle,
	}
	logger.Infof("sub server accepting dmsg :%d → UDP %s", serverDmsgPort, cfg.TargetUDP)
	return skyudpbridge.RunServer(ctx, cfg, lis.Accept, logger)
}

func initLogger(name string) *logging.Logger {
	logger := logging.MustGetLogger(name)
	if lvl, err := logging.LevelFromString(logLevel); err == nil {
		logging.SetLevel(lvl)
	}
	return logger
}

func resolveKeypair() (cipher.PubKey, cipher.SecKey, error) {
	candidate := skHex
	if candidate == "" {
		candidate = os.Getenv("SKYUDP_SK")
	}
	if candidate == "" && skFile != "" {
		data, err := os.ReadFile(skFile) //nolint:gosec
		if err != nil {
			return cipher.PubKey{}, cipher.SecKey{}, fmt.Errorf("read sk-file: %w", err)
		}
		candidate = strings.TrimSpace(string(data))
	}
	if candidate == "" {
		pk, sk := cipher.GenerateKeyPair()
		return pk, sk, nil
	}

	var sk cipher.SecKey
	if err := sk.UnmarshalText([]byte(candidate)); err != nil {
		return cipher.PubKey{}, cipher.SecKey{}, fmt.Errorf("parse sk: %w", err)
	}
	pk, err := sk.PubKey()
	if err != nil {
		return cipher.PubKey{}, cipher.SecKey{}, fmt.Errorf("derive pk: %w", err)
	}
	return pk, sk, nil
}

func startDmsgClient(ctx context.Context, log *logging.Logger, pk cipher.PubKey, sk cipher.SecKey) (*dmsg.Client, func(), error) {
	discURL := dmsgDisc
	if discURL == "" {
		discURL = deployment.Prod.DmsgDiscovery
	}
	if discURL == "" {
		return nil, nil, errors.New("no dmsg discovery URL (set --dmsg-disc)")
	}

	httpC := &http.Client{Timeout: 30 * time.Second}
	discClient := disc.NewHTTP(discURL, httpC, log)
	dmsgCfg := dmsg.DefaultConfig()
	dmsgCfg.MinSessions = 1

	dmsgC := dmsg.NewClient(pk, sk, discClient, dmsgCfg)
	go dmsgC.Serve(ctx)

	select {
	case <-ctx.Done():
		_ = dmsgC.Close() //nolint:errcheck,gosec
		return nil, nil, ctx.Err()
	case <-dmsgC.Ready():
		log.Debug("dmsg client ready")
	case <-time.After(30 * time.Second):
		_ = dmsgC.Close() //nolint:errcheck,gosec
		return nil, nil, errors.New("timeout waiting for dmsg client")
	}

	stop := func() {
		_ = dmsgC.Close() //nolint:errcheck,gosec
	}
	return dmsgC, stop, nil
}

// dmsgDialer satisfies skyudpbridge.Dialer by speaking directly to
// the peer over dmsg.
type dmsgDialer struct{ c *dmsg.Client }

func (d *dmsgDialer) Dial(ctx context.Context, peer cipher.PubKey, port uint16) (net.Conn, error) {
	stream, err := d.c.Dial(ctx, dmsg.Addr{PK: peer, Port: port})
	if err != nil {
		return nil, err
	}
	return stream, nil
}
