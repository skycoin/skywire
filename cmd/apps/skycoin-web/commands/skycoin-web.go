// Package commands registers the skycoin-web thin-client wallet as an
// internal skywire launcher app. Running it in-process (rather than as a
// raw external `skywire skycoin web` subprocess) is what lets the SAME
// launch path serve it on the host-native visor and, unchanged, on the
// wasm visor — see project_skycoin_web_internal_app_convergence. The
// per-platform serving body is the only seam; registration + lifecycle are
// shared, exactly like skychat.
package commands

import (
	"context"
	"strings"

	skycoinweb "github.com/skycoin/skycoin/cmd/skycoin-web/commands"

	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/skyenv"
)

func init() {
	launcher.RegisterApp(skyenv.SkycoinWebName, RunSkycoinWeb)
}

// RunSkycoinWeb runs the vendored skycoin-web thin-client wallet server
// in-process, cancellable via ctx (the launcher cancels ctx to stop the
// app; the upstream serve(ctx) shuts its http.Server down gracefully).
// args come from the app's AppConfig.Args; any leading positional launch
// tokens (the external "skycoin web" / "app skycoin-web" prefix) are
// stripped so only the skycoin-web flags reach its cobra command.
func RunSkycoinWeb(ctx context.Context, args []string) error {
	i := 0
	for i < len(args) && !strings.HasPrefix(args[i], "-") {
		i++
	}
	cmd := skycoinweb.RootCmd
	cmd.SetArgs(args[i:])
	return cmd.ExecuteContext(ctx)
}
