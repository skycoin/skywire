// Package commands cmd/smb/commands/smb.go
//
// Standalone SMTP→dmsg bridge ("smb") — no visor dependency. The
// binary creates its own dmsg.Client (either with a provided
// secret key or an ephemeral one), then runs the same SMTP server
// loop pkg/skymailbridge ships to the visor-side flavor.
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
	"github.com/skycoin/skywire/pkg/skymailbridge"
)

var (
	bindAddr   string
	suffix     string
	mode       string
	heloName   string
	remotePort uint16
	skHex      string
	skFile     string
	dmsgDisc   string
	logLevel   string
)

func init() {
	RootCmd.Flags().StringVar(&bindAddr, "addr", "127.0.0.1:1025", "local SMTP listen address (Postfix transport_map target)")
	RootCmd.Flags().StringVar(&suffix, "suffix", skymailbridge.DefaultSuffix, "TLD suffix routed over dmsg")
	RootCmd.Flags().StringVar(&mode, "mode", "b", "envelope mode: a=verbatim, b=strip .<pk><suffix> from RCPT TO")
	RootCmd.Flags().StringVar(&heloName, "helo", skymailbridge.DefaultHeloName, "HELO/EHLO name presented to the peer's Postfix")
	RootCmd.Flags().Uint16Var(&remotePort, "remote-port", 25, "dmsg port to dial on the peer (where its Postfix smtpd is exposed)")
	RootCmd.Flags().StringVar(&skHex, "sk", "", "secret key hex (env: SKYMAIL_SK). When empty + no --sk-file, an ephemeral key is generated")
	RootCmd.Flags().StringVar(&skFile, "sk-file", "", "path to a file containing the secret key hex (newline-trimmed)")
	RootCmd.Flags().StringVar(&dmsgDisc, "dmsg-disc", "", "dmsg discovery URL (defaults to deployment.Prod.DmsgDiscovery)")
	RootCmd.Flags().StringVar(&logLevel, "log-level", "info", "log level: debug|info|warn|error")
}

// RootCmd is the cobra entry for the standalone SMTP→dmsg bridge.
// Same RootCmd serves both the top-level binary (cmd/smb) and the
// integrated subcommand (`skywire dmsg smb`). Short Use keeps the
// subcommand quick to type; the Short description carries the full
// description so operators reading `dmsg --help` know what it is.
var RootCmd = &cobra.Command{
	Use:                   "smb",
	Short:                 "Standalone SMTP→dmsg bridge — relays *.skynet envelopes via own dmsg client",
	Long:                  calvin.AsciiFont("smb"),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(cmd *cobra.Command, _ []string) {
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()

		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sig
			cancel()
		}()

		if err := run(ctx); err != nil {
			log.Fatal(err)
		}
	},
}

// Execute is invoked from the package-main entrypoint.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	cfg := skymailbridge.Config{
		Suffix:     suffix,
		Mode:       mode,
		HeloName:   heloName,
		RemotePort: remotePort,
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	logger := logging.MustGetLogger("smb")
	if lvl, err := logging.LevelFromString(logLevel); err == nil {
		logging.SetLevel(lvl)
	}

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

	lis, err := net.Listen("tcp", bindAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", bindAddr, err)
	}
	logger.Infof("smb (dmsg-direct SMTP bridge) accepting SMTP on %s (mode=%s suffix=%s)", bindAddr, cfg.Mode, cfg.Suffix)

	dialer := &dmsgDialer{c: dmsgC}
	if err := skymailbridge.Serve(ctx, lis, dialer, cfg, logger); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// resolveKeypair returns the (PubKey, SecKey) to use for the dmsg
// client, sourced from --sk (hex), --sk-file, $SKYMAIL_SK, or a
// freshly generated keypair.
func resolveKeypair() (cipher.PubKey, cipher.SecKey, error) {
	candidate := skHex
	if candidate == "" {
		candidate = os.Getenv("SKYMAIL_SK")
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

// startDmsgClient brings up a standalone dmsg.Client and waits for
// it to be ready. Returns the client + a stop closure for shutdown.
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

// dmsgDialer satisfies skymailbridge.Dialer by speaking directly to
// the peer over dmsg. The peer is expected to be listening on the
// requested dmsg port (typically arranged via `skywire cli serve`
// on the receiver's visor, or by a peer instance of this binary).
type dmsgDialer struct{ c *dmsg.Client }

func (d *dmsgDialer) Dial(ctx context.Context, peer cipher.PubKey, port uint16) (net.Conn, error) {
	stream, err := d.c.Dial(ctx, dmsg.Addr{PK: peer, Port: port})
	if err != nil {
		return nil, err
	}
	return stream, nil
}
