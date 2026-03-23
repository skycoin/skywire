// Package commands vpn-server.go
package commands

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/vpn"
)

const (
	netType = appnet.TypeSkynet
)

var (
	localPKStr string
	localSKStr string
	whitelist  string
	networkIfc string
	secure     bool
	appPort    uint16
)

func init() {
	launcher.RegisterApp("vpn-server", RunVPNServer)
	RootCmd.Flags().StringVar(&localPKStr, "pk", "", "local pubkey")
	RootCmd.Flags().StringVar(&localSKStr, "sk", "", "local seckey")
	RootCmd.Flags().StringVar(&whitelist, "whitelist", "", "comma-separated list of public keys allowed to connect (empty = allow all)")
	RootCmd.Flags().StringVar(&networkIfc, "netifc", "", "Default network interface for multiple available interfaces")
	RootCmd.Flags().BoolVar(&secure, "secure", true, "Forbid connections from clients to server local network")
	RootCmd.Flags().Uint16Var(&appPort, "port", 0, "routing port for communication between app and visor")
}

// RootCmd is the root command for skywire-cli
var RootCmd = &cobra.Command{
	Use:                   "vpn-server",
	Short:                 "skywire vpn server application",
	Long:                  calvin.AsciiFont("vpn-server"),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, args []string) {
		ctx := context.Background()
		if err := RunVPNServer(ctx, args); err != nil {
			log.Fatal(err)
		}
	},
}

// RunVPNServer runs the VPN server app logic.
func RunVPNServer(ctx context.Context, args []string) error {
	// Parse flags when called via internal launcher
	if len(args) > 0 {
		fs := pflag.NewFlagSet("vpn-server", pflag.ContinueOnError)
		fs.StringVar(&localPKStr, "pk", "", "local pubkey")
		fs.StringVar(&localSKStr, "sk", "", "local seckey")
		fs.StringVar(&whitelist, "whitelist", "", "comma-separated public keys")
		fs.StringVar(&networkIfc, "netifc", "", "network interface")
		fs.BoolVar(&secure, "secure", true, "secure mode")
		fs.Uint16Var(&appPort, "port", 0, "routing port")
		if err := fs.Parse(args); err != nil {
			return fmt.Errorf("failed to parse flags: %w", err)
		}
	}

	appCl := app.NewClient(nil)
	defer appCl.Close()
	logger := appCl.Log()

	port := appCl.Config().RoutingPort
	if appPort != 0 {
		port = routing.Port(appPort)
		setAppPort(appCl, logger, port)
	}

	bi := buildinfo.Get()
	logger.Infof("Version %q built on %q against commit %q", bi.Version, bi.Date, bi.Commit)

	if runtime.GOOS != "linux" {
		err := errors.New("OS is not supported")
		logger.Error(err)
		setAppErr(appCl, logger, err)
		return err
	}

	localPK := cipher.PubKey{}
	if localPKStr != "" {
		if err := localPK.UnmarshalText([]byte(localPKStr)); err != nil {
			logger.WithError(err).Error("Invalid local PK")
			setAppErr(appCl, logger, err)
			return err
		}
	}

	localSK := cipher.SecKey{}
	if localSKStr != "" {
		if err := localSK.UnmarshalText([]byte(localSKStr)); err != nil {
			logger.WithError(err).Error("Invalid local SK")
			setAppErr(appCl, logger, err)
			return err
		}
	}

	osSigs := make(chan os.Signal, 2)

	sigs := []os.Signal{syscall.SIGTERM, syscall.SIGINT}
	for _, sig := range sigs {
		signal.Notify(osSigs, sig)
	}

	l, err := appCl.Listen(netType, port)
	if err != nil {
		logger.WithError(err).WithField("port", port).Error("Error listening network")
		setAppErr(appCl, logger, err)
		return err
	}

	// Parse whitelist
	var whitelistPKs []cipher.PubKey
	if whitelist != "" {
		for _, pkStr := range strings.Split(whitelist, ",") {
			pkStr = strings.TrimSpace(pkStr)
			if pkStr == "" {
				continue
			}
			var pk cipher.PubKey
			if err := pk.UnmarshalText([]byte(pkStr)); err != nil {
				logger.WithError(err).WithField("pk", pkStr).Error("Invalid whitelist public key")
				setAppErr(appCl, logger, err)
				return err
			}
			whitelistPKs = append(whitelistPKs, pk)
		}
	}

	srvCfg := vpn.ServerConfig{
		Whitelist:        whitelistPKs,
		Secure:           secure,
		NetworkInterface: networkIfc,
	}
	srv, err := vpn.NewServer(srvCfg, appCl)
	if err != nil {
		logger.WithError(err).Error("Error creating VPN server")
		setAppErr(appCl, logger, err)
		return err
	}
	defer func() {
		if err := srv.Close(); err != nil {
			logger.WithError(err).Warn("Error closing server")
		}
	}()

	errCh := make(chan error)
	go func() {
		if err := srv.Serve(l); err != nil {
			errCh <- err
		}

		close(errCh)
	}()

	defer setAppStatus(appCl, logger, appserver.AppDetailedStatusStopped)

	select {
	case <-osSigs:
		return nil
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		if err != nil {
			logger.WithError(err).Error("Error serving")
			return err
		}
	}

	return nil
}

func setAppErr(appCl *app.Client, log logrus.FieldLogger, err error) {
	if appErr := appCl.SetError(err.Error()); appErr != nil {
		log.WithError(appErr).WithField("original_error", err).Warn("Failed to set error")
	}
}

func setAppStatus(appCl *app.Client, log logrus.FieldLogger, status appserver.AppDetailedStatus) {
	if err := appCl.SetDetailedStatus(string(status)); err != nil {
		log.WithError(err).WithField("status", status).Warn("Failed to set status")
	}
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}

func setAppPort(appCl *app.Client, log logrus.FieldLogger, port routing.Port) {
	if err := appCl.SetAppPort(port); err != nil {
		log.WithError(err).WithField("port", port).Warn("Failed to set port")
	}
}
