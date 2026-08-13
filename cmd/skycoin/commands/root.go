// Package commands cmd/skycoin/commands/root.go c4-app-wallet
//
// This is skywire's own assembly of the `skywire skycoin` command tree. It
// reproduces what skycoin's cmd/skycoin-wallet/commands does for the standalone
// skycoin binary — the same five subcommands, the same banner, the same
// -d/-b flags — by mounting skycoin's individual command packages directly
// instead of importing that assembly.
//
// The reason is what the assembly drags in. cmd/skycoin-wallet/commands imports
// cmd/skycoin-web/commands, so importing it links the thin-client wallet's
// server AND its skycoin-lite cipher wasm (src/skycoin-lite/wasm-go, ~1.8 MB
// gzipped) into skywire. That wasm is redundant here: a wallet running on the
// wasm visor gets its cipher from the visor itself, via wasmcipher.Register()
// in cmd/wasm-visor. None of the four other command packages pull it —
// skycoin/commands, skycoin-cli/commands, newcoin/commands and
// explorer/commands each have neither skycoin-web nor skycoin-lite in their
// dependency graphs — so mounting them individually is what lets it go.
//
// The wallet BUNDLE is a separate matter and is not affected: it stays vendored
// via pkg/visor's import of skycoin-web/src/gui (visor.WalletUIFS), so a skycoin
// vendor bump is still the wallet update, with one copy in the tree.
//
// Nothing here changes skycoin. The standalone skycoin binary keeps its own
// assembly, unmodified; this is a second assembly of the same parts.
package commands

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	explorer "github.com/skycoin/skycoin/cmd/explorer/commands"
	newcoin "github.com/skycoin/skycoin/cmd/newcoin/commands"
	skycoincli "github.com/skycoin/skycoin/cmd/skycoin-cli/commands"
	skycoinweb "github.com/skycoin/skycoin/cmd/skycoin-web/commands"
	daemon "github.com/skycoin/skycoin/cmd/skycoin/commands"
	"github.com/skycoin/skycoin/src/fiber"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
)

var (
	bv bool
	di bool
)

// skycoinModulePath is the module whose version this tree reports. The banner
// has to name skycoin's version rather than skywire's, since these are skycoin's
// commands running inside skywire's binary.
const skycoinModulePath = "github.com/skycoin/skycoin"

// RootCmd is the `skywire skycoin` command group.
var RootCmd = &cobra.Command{
	Use:                   "skycoin",
	Short:                 "skycoin daemon & cli",
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, _ []string) {
		if di {
			fmt.Printf("%v\n", buildinfo.DebugBuildInfo())
			return
		}
		if bv {
			fmt.Printf("%v\n", buildinfo.DBIVersion())
			return
		}
		if err := cmd.Help(); err != nil {
			log.Printf("Failed to print help: %v", err)
		}
	},
}

// coinName resolves the display name the banner and subcommand descriptions use.
// FIBER_TOML points at a fibercoin's config; without it this is plain skycoin.
func coinName() string {
	name := "skycoin"

	path := os.Getenv("FIBER_TOML")
	if path == "" {
		return name
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	cfg, err := fiber.NewConfig(filepath.Base(path), filepath.Dir(path))
	if err != nil {
		return name
	}
	if cfg.Node.DisplayName != "" {
		name = cfg.Node.DisplayName
	}

	return strings.ToLower(name)
}

func init() {
	name := coinName()

	long := calvin.AsciiFont(name)
	// The skycoin dependency's version, not skywire's: `skywire skycoin -v`
	// under a skycoin banner should say which skycoin this is.
	if v := buildinfo.DepVersion(skycoinModulePath); v != "" {
		long += "\n" + v
		RootCmd.Version = v
	}
	if goVer := buildinfo.Go(); goVer != "" && goVer != "unknown" {
		long += "\nbuilt with " + goVer
	}
	RootCmd.Long = long

	daemon.RootCmd.Use = "daemon"
	explorer.RootCmd.Use = "explorer"
	// Mounted from skycoin for now. This is the one subcommand that still comes
	// from cmd/skycoin-web/commands, and so the one that still links the
	// skycoin-lite wasm; replacing it with a skywire-native `web` that serves
	// the wasm-visor page is what completes the split.
	skycoinweb.RootCmd.Use = "web"
	skycoinweb.RootCmd.Short = name + " thin client web wallet"

	RootCmd.AddCommand(
		daemon.RootCmd,
		skycoinweb.RootCmd,
		skycoincli.RootCmd,
		newcoin.RootCmd,
		explorer.RootCmd,
	)

	if buildinfo.DebugBuildInfo() != nil {
		RootCmd.Flags().BoolVarP(&di, "info", "d", false, "print runtime/debug.BuildInfo")
	}
	if buildinfo.DBIVersion() != "" {
		RootCmd.Flags().BoolVarP(&bv, "bv", "b", false, "print runtime/debug.BuildInfo.Main.Version")
	}
}
