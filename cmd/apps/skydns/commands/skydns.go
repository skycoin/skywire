// Package commands cmd/apps/skydns/commands/skydns.go
package commands

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/0magnet/calvin"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/vpn"
)

var upstream string

func init() {
	RootCmd.Flags().StringVar(&upstream, "dns", "", "resolver for names that are not mesh names (empty means the phone's own DNS)")
	launcher.RegisterApp(skyenv.SkyDNSName, RunSkyDNS)
}

// RootCmd is the root command for skydns
var RootCmd = &cobra.Command{
	Use:                   "skydns",
	Short:                 "open .dmsg and .skynet names in every app on an Android phone",
	Long:                  calvin.AsciiFont("skydns"),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		if err := RunSkyDNS(ctx, nil); err != nil {
			log.Fatal(err)
		}
	},
}

// RunSkyDNS answers mesh names on SkyDNS's own tunnel until ctx ends.
func RunSkyDNS(ctx context.Context, args []string) error {
	if len(args) > 0 {
		fs := pflag.NewFlagSet("skydns", pflag.ContinueOnError)
		fs.StringVar(&upstream, "dns", "", "resolver for other names")
		if err := fs.Parse(args); err != nil {
			return fmt.Errorf("failed to parse flags: %w", err)
		}
	}

	appCl := app.NewClient(nil)
	defer appCl.Close()
	logger := appCl.Log()

	if err := appCl.SetDetailedStatus(string(appserver.AppDetailedStatusRunning)); err != nil {
		logger.WithError(err).Warn("Failed to set status")
	}
	defer func() {
		if err := appCl.SetDetailedStatus(string(appserver.AppDetailedStatusStopped)); err != nil {
			logger.WithError(err).Warn("Failed to set status")
		}
	}()

	err := vpn.RunSkyDNS(ctx, vpn.SkyDNSConfig{Dial: vpn.MeshDialer(appCl), Upstream: upstream, Log: logger})
	if err != nil {
		if appErr := appCl.SetError(err.Error()); appErr != nil {
			logger.WithError(appErr).Warn("Failed to set error")
		}
	}
	return err
}
