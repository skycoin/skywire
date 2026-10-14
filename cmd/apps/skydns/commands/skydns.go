// Package commands cmd/apps/skydns/commands/skydns.go
package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/skycoin/skywire/pkg/cmdutil"

	"github.com/0magnet/calvin"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skynetca"
	"github.com/skycoin/skywire/pkg/vpn"
)

var upstream, caDir, setupTun string

func init() {
	RootCmd.Flags().StringVar(&upstream, "dns", "", "resolver for names that are not mesh names (empty means the phone's own DNS)")
	RootCmd.Flags().StringVar(&caDir, "ca", "", "directory with ca.crt and ca.key from \"skywire cli resolver ca gen\": terminate HTTPS to mesh names with it")
	// --setup-tun is what an unprivileged skydns runs through pkexec: the one
	// root step, not something to call by hand.
	RootCmd.Flags().StringVar(&setupTun, "setup-tun", "", "set up the persistent tun for uid:gid as root, then exit")
	if err := RootCmd.Flags().MarkHidden("setup-tun"); err != nil {
		panic(err)
	}
	launcher.RegisterApp(skyenv.SkyDNSName, RunSkyDNS)
}

// RootCmd is the root command for skydns
var RootCmd = &cobra.Command{
	Use:                   "skydns",
	Short:                 "open .dmsg and .skynet names in every app on this device",
	Long:                  calvin.AsciiFont("skydns"),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		if setupTun != "" {
			runSetupTun(setupTun)
			return
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		if err := RunSkyDNS(ctx, nil); err != nil {
			cmdutil.Fatal(err)
		}
	},
}

// RunSkyDNS answers mesh names on SkyDNS's own tunnel until ctx ends.
func RunSkyDNS(ctx context.Context, args []string) error {
	if len(args) > 0 {
		fs := pflag.NewFlagSet("skydns", pflag.ContinueOnError)
		fs.StringVar(&upstream, "dns", "", "resolver for other names")
		fs.StringVar(&caDir, "ca", "", "CA directory for HTTPS to mesh names")
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

	minter, err := caMinter(caDir)
	if err != nil {
		if appErr := appCl.SetError(err.Error()); appErr != nil {
			logger.WithError(appErr).Warn("Failed to set error")
		}
		return err
	}
	err = vpn.RunSkyDNS(ctx, vpn.SkyDNSConfig{Dial: vpn.MeshDialer(appCl), Upstream: upstream, Minter: minter, Log: logger})
	if err != nil {
		if appErr := appCl.SetError(err.Error()); appErr != nil {
			logger.WithError(appErr).Warn("Failed to set error")
		}
	}
	return err
}

// caMinter loads the CA in dir for HTTPS to mesh names. An empty dir means
// plain HTTP only. A CA that cannot be loaded is an error, not a quiet
// fallback, since https:// links would then fail for no visible reason.
func caMinter(dir string) (skynetca.LeafMinter, error) {
	if dir == "" {
		return nil, nil
	}
	ca, key, err := skynetca.LoadCA(filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key"))
	if err != nil {
		return nil, fmt.Errorf("skydns --ca %s: %w", dir, err)
	}
	return skynetca.NewMinter(ca, key, skynetca.LeafOptions{}), nil
}

// runSetupTun parses uid:gid and sets up the persistent tun as root, exiting
// on failure.
func runSetupTun(owner string) {
	u, g, ok := strings.Cut(owner, ":")
	uid, uerr := strconv.Atoi(u)
	gid, gerr := strconv.Atoi(g)
	if !ok || uerr != nil || gerr != nil {
		cmdutil.Fatal(fmt.Errorf("--setup-tun wants uid:gid, got %q", owner))
	}
	if err := vpn.SetupSkyDNSTun(uid, gid); err != nil {
		cmdutil.Fatal(err)
	}
}
