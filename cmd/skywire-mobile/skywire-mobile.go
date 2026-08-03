// Package main cmd/skywire-mobile/skywire-mobile.go c4-vis-cli
/*
skywire-mobile is the lite multicall core for the mobile app: the visor, the
CLI's `config` subtree and the four client apps in ONE binary, shipped to
Android as libskywire-mobile.so. It is a build VARIANT of this repo — build it
with `-tags mobile,withoutsystray` (make build-mobile / android-mobile), which
strips the embedded desktop assets (geoip db, manager UI, browser wallet,
tpviz) while keeping the visor's authenticated local API intact.

Apps run in-process by default: importing the four app command packages below
registers their launcher entries (launcher.RegisterApp) at import time, so a
config entry with `binary: ""` resolves to the in-proc RunFunc. The `app`
subcommand is the per-app exec fallback — a config entry with a non-empty
`binary` execs `<binary> app <name> …`, and the whole pipeline (arg stripping,
isRawProcessApp) keys on the subcommand being named exactly "app".
*/
package main

import (
	"log"

	"github.com/spf13/cobra"

	sc "github.com/skycoin/skywire/cmd/apps/skychat/commands"
	xc "github.com/skycoin/skywire/cmd/apps/skydex-client/commands"
	ssc "github.com/skycoin/skywire/cmd/apps/skysocks-client/commands"
	vpnc "github.com/skycoin/skywire/cmd/apps/vpn-client/commands"
	cliconfig "github.com/skycoin/skywire/cmd/skywire-cli/commands/config"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/flags"
	"github.com/skycoin/skywire/pkg/visor"
)

// RootCmd is the skywire-mobile root: visor + config + the app subtree.
var RootCmd = &cobra.Command{
	Use:   "skywire-mobile",
	Short: "Skywire mobile core (visor + config + client apps)",
	Long: `skywire-mobile — the lite skywire core for the mobile app:
the visor, config generation, and the skychat / skysocks-client /
vpn-client / skydex-client apps in one binary. Apps run in-process
inside the visor by default; 'app <name>' runs one standalone
(the launcher's exec fallback).`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
}

// appsCmd MUST be named "app": the launcher's exec fallback runs
// `<binary> app <name> …` and isRawProcessApp keys on that args shape.
var appsCmd = &cobra.Command{
	Use:   "app",
	Short: "skywire native applications",
}

func init() {
	appsCmd.AddCommand(sc.RootCmd, ssc.RootCmd, vpnc.RootCmd, xc.RootCmd)
	RootCmd.AddCommand(visor.RootCmd, cliconfig.RootCmd, appsCmd)
	// visor.RootCmd computes its Use from os.Args[0]; pin the mounted name —
	// the same post-mount rename cmd/skywire/commands does.
	visor.RootCmd.Use = "visor"
	// Install the flag-aware `help` on the visor subtree (as cmd/skywire's
	// init loop does): visor.RootCmd is runnable with no Args validator, so
	// without its own help command `visor help` would be swallowed as an
	// ignored positional and START the visor.
	flags.InstallHelp(visor.RootCmd)
	flags.InitFlags(RootCmd, false)
}

func main() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
