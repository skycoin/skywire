// Package commands cmd/apps/skycoin-web/commands/skycoin-web.go c4-app-wallet
//
// skycoin-web as an internal launcher app, the same on a native visor and in a
// browser tab. It runs the vendored wallet server in-process and publishes its
// handler to the launcher registry, where the hypervisor serves it under
// /wallet/. A port opens only when the app's args name one.
package commands

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/0magnet/bottle/vnet"
	skycoinweb "github.com/skycoin/skycoin/cmd/skycoin-web/commands"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// RootCmd is the `skywire app skycoin` command group. skycoin-web mounts under
// it as `skywire app skycoin web`, matching the other visor apps under
// `skywire app <name>` (skychat, skysocks, vpn, …). The name is left
// unhyphenated ("skycoin web", not "skycoin-web") so future skycoin apps —
// `skywire app skycoin daemon`, `skywire app skycoin explorer` — can hang off
// the same group without a rename.
var RootCmd = &cobra.Command{
	Use:   "skycoin",
	Short: "skycoin apps",
}

func init() {
	launcher.RegisterApp(skyenv.SkycoinWebName, RunSkycoinWeb)
	// Mount the vendored skycoin-web command as the `web` subcommand.
	skycoinweb.RootCmd.Use = "web"
	RootCmd.AddCommand(skycoinweb.RootCmd)
	// The resolving proxy listens on the visor's loopback, which in a browser
	// tab is the page's virtual one.
	skycoinweb.NodeDial = func(_ context.Context, network, addr string) (net.Conn, error) {
		return vnet.DialTimeout(network, addr, 30*time.Second)
	}
}

// mounted gives the handler an identity. The wallet's handler is a func, which
// ClearHTTPHandler's comparison cannot take.
type mounted struct{ http.Handler }

// publisher returns a Mount for one run. It puts that run's handler in the
// launcher registry and takes out only that one, so the old run of a restart,
// stopping after the new one has started, cannot remove its successor.
// skycoin-web defers Mount(nil) by value, so each run keeps its own.
func publisher() func(http.Handler) {
	var mine *mounted
	return func(h http.Handler) {
		if h == nil {
			if mine != nil {
				launcher.ClearHTTPHandler(skyenv.SkycoinWebName, mine)
			}
			return
		}
		mine = &mounted{h}
		launcher.RegisterHTTPHandler(skyenv.SkycoinWebName, mine)
	}
}

// RunSkycoinWeb runs the vendored skycoin-web thin-client wallet server
// in-process, cancellable via ctx (the launcher cancels ctx to stop the
// app; the upstream serve(ctx) shuts its http.Server down gracefully).
// args come from the app's AppConfig.Args; any leading positional launch
// tokens (the external "skycoin web" / "app skycoin-web" prefix) are
// stripped so only the skycoin-web flags reach its cobra command.
func RunSkycoinWeb(ctx context.Context, args []string) error {
	// The app client completes the launcher's in-process handshake and carries
	// the status, as the embedded dmsgweb app does.
	appCl := app.NewClient(nil)
	defer appCl.Close()
	appCl.SetStatusOrLog(appserver.AppDetailedStatusRunning)
	if err := runWallet(ctx, args); err != nil {
		appCl.SetErrorOrLog(err)
		return err
	}
	appCl.SetStatusOrLog(appserver.AppDetailedStatusStopped)
	return nil
}

func runWallet(ctx context.Context, args []string) error {
	i := 0
	for i < len(args) && !strings.HasPrefix(args[i], "-") {
		i++
	}
	// RunE directly, not Execute: cobra executes from the root of the tree this
	// command is mounted in, with os.Args, which re-ran the visor's own command.
	cmd := skycoinweb.RootCmd
	if err := parseFresh(cmd.Flags(), args[i:]); err != nil {
		return err
	}
	if err := cmd.ValidateFlagGroups(); err != nil {
		return err
	}
	cmd.SetContext(ctx)
	skycoinweb.Mount = publisher()
	return cmd.RunE(cmd, cmd.Flags().Args())
}

// parseFresh parses args as if fs had never been parsed. The command is a
// package global, so a restart would otherwise keep the last run's values.
// A slice flag that was set once appends to whatever it holds, default
// included, so slices start empty and get their default back only if args
// leave them unset.
func parseFresh(fs *pflag.FlagSet, args []string) error {
	fs.VisitAll(func(f *pflag.Flag) {
		if s, ok := f.Value.(pflag.SliceValue); ok {
			_ = s.Replace(nil) //nolint:errcheck
		} else {
			_ = f.Value.Set(f.DefValue) //nolint:errcheck
		}
		f.Changed = false
	})
	if err := fs.Parse(args); err != nil {
		return err
	}
	fs.VisitAll(func(f *pflag.Flag) {
		s, ok := f.Value.(pflag.SliceValue)
		if !ok || f.Changed {
			return
		}
		var def []string
		if d := strings.Trim(f.DefValue, "[]"); d != "" {
			def = strings.Split(d, ",")
		}
		_ = s.Replace(def) //nolint:errcheck
	})
	return nil
}
