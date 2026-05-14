// Package commands cmd/apps/skymail-bridge/commands/skymail-bridge.go
//
// Visor-managed skymail-bridge: SMTP→skywire-routing bridge that
// reuses the visor's running dmsg session via the app SDK.
//
// All protocol logic (SMTP parser, recipient address parser, peer
// relay) lives in pkg/skymailbridge. This file is the thin
// glue: parses flags, sets up the app.Client + Dialer adapter,
// opens the TCP listener, hands off to skymailbridge.Serve.
//
// For deployments without a full visor, see cmd/skymail-bridge
// (standalone variant using dmsg.Client directly).
package commands

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"

	cobra "github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skymailbridge"

	"github.com/sirupsen/logrus"
)

var (
	bindAddr   string
	suffix     string
	mode       string
	heloName   string
	remotePort uint16
	appPort    uint16
)

func init() {
	launcher.RegisterApp(skyenv.SkymailBridgeName, RunSkymailBridge)
	RootCmd.Flags().StringVar(&bindAddr, "addr", skyenv.SkymailBridgeAddr, "local SMTP listen address (Postfix transport_map target)")
	RootCmd.Flags().StringVar(&suffix, "suffix", skymailbridge.DefaultSuffix, "TLD suffix that routes over skynet")
	RootCmd.Flags().StringVar(&mode, "mode", "b", "envelope mode: a=verbatim RCPT TO, b=strip .<pk><suffix> before forwarding")
	RootCmd.Flags().StringVar(&heloName, "helo", skymailbridge.DefaultHeloName, "HELO/EHLO name presented to the peer's Postfix")
	RootCmd.Flags().Uint16Var(&remotePort, "remote-port", uint16(skyenv.SmtpPort), "skywire routing port to dial on the peer")
	RootCmd.Flags().Uint16Var(&appPort, "port", 0, "routing port for communication between app and visor (0 = visor-assigned)")
}

// RootCmd is the cobra entry for `skymail-bridge`.
var RootCmd = &cobra.Command{
	Use:                   skyenv.SkymailBridgeName,
	Short:                 "SMTP-aware proxy that relays .skynet recipient envelopes over skywire",
	Long:                  calvin.AsciiFont(skyenv.SkymailBridgeName),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, args []string) {
		ctx := context.Background()
		if err := RunSkymailBridge(ctx, args); err != nil {
			log.Fatal(err)
		}
	},
}

// Execute runs RootCmd. Called by the package-main entrypoint.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

// RunSkymailBridge is registered with launcher.RegisterApp so the
// visor's launcher can spawn the app in-process. Re-parses flags
// from args when the visor passes them down.
func RunSkymailBridge(ctx context.Context, args []string) error {
	if len(args) > 0 {
		fs := pflag.NewFlagSet(skyenv.SkymailBridgeName, pflag.ContinueOnError)
		fs.StringVar(&bindAddr, "addr", skyenv.SkymailBridgeAddr, "local SMTP listen address")
		fs.StringVar(&suffix, "suffix", skymailbridge.DefaultSuffix, "TLD suffix routed over skynet")
		fs.StringVar(&mode, "mode", "b", "envelope mode")
		fs.StringVar(&heloName, "helo", skymailbridge.DefaultHeloName, "HELO/EHLO name")
		fs.Uint16Var(&remotePort, "remote-port", uint16(skyenv.SmtpPort), "remote routing port")
		fs.Uint16Var(&appPort, "port", 0, "routing port")
		if err := fs.Parse(args); err != nil {
			return fmt.Errorf("parse flags: %w", err)
		}
	}

	cfg := skymailbridge.Config{
		Suffix:     suffix,
		Mode:       mode,
		HeloName:   heloName,
		RemotePort: remotePort,
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	appCl := app.NewClient(nil)
	defer appCl.Close()
	logger := appCl.Log()

	port := appCl.Config().RoutingPort
	if appPort != 0 {
		port = routing.Port(appPort)
	}
	if err := appCl.SetAppPort(port); err != nil {
		logger.WithError(err).Warn("SetAppPort")
	}

	bi := buildinfo.Get()
	logger.Infof("skymail-bridge %s built on %s (commit %s) — mode=%s suffix=%s",
		bi.Version, bi.Date, bi.Commit, cfg.Mode, cfg.Suffix)

	defer setAppStatus(appCl, logger, appserver.AppDetailedStatusStopped)

	lis, err := net.Listen("tcp", bindAddr)
	if err != nil {
		setAppErr(appCl, logger, err)
		return fmt.Errorf("listen %s: %w", bindAddr, err)
	}
	logger.Infof("skymail-bridge accepting SMTP on %s", bindAddr)
	setAppStatus(appCl, logger, appserver.AppDetailedStatusRunning)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Graceful shutdown on SIGINT/SIGTERM mirrors skysocks-client.
	termCh := make(chan os.Signal, 1)
	signal.Notify(termCh, os.Interrupt)
	go func() {
		select {
		case <-termCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	dialer := &appDialer{cl: appCl}
	if err := skymailbridge.Serve(ctx, lis, dialer, cfg, logger); err != nil && !errors.Is(err, context.Canceled) {
		setAppErr(appCl, logger, err)
		return err
	}
	return nil
}

// appDialer satisfies skymailbridge.Dialer using the visor's app
// SDK. Dialing through appCl keeps a single dmsg session shared
// across all visor apps; no second identity, no second discovery
// connect.
type appDialer struct{ cl *app.Client }

func (d *appDialer) Dial(_ context.Context, peer cipher.PubKey, port uint16) (net.Conn, error) {
	return d.cl.Dial(appnet.Addr{Net: appnet.TypeSkynet, PubKey: peer, Port: routing.Port(port)})
}

func setAppErr(appCl *app.Client, logger logrus.FieldLogger, err error) {
	if appErr := appCl.SetError(err.Error()); appErr != nil {
		logger.WithError(appErr).WithField("original_error", err).Warn("Failed to set error")
	}
}

func setAppStatus(appCl *app.Client, logger logrus.FieldLogger, status appserver.AppDetailedStatus) {
	if err := appCl.SetDetailedStatus(string(status)); err != nil {
		logger.WithError(err).WithField("status", status).Warn("Failed to set status")
	}
}
