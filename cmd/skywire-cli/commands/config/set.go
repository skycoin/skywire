// Package cliconfig cmd/skywire-cli/commands/config/set.go c4-vis-cli
//
// `skywire cli config set` — edit the RUNNING visor's config, field by field,
// through the visor itself.
//
// Editing skywire-config.json by hand while a visor runs does not stick: the
// visor re-marshals its in-memory config over the file on every setter that
// persists, so a hand-edit is erased at the next flush (an is_public=true patch
// typed into the file was reverted within the hour). `config update` edits the
// FILE and has the same problem on a running visor. This command routes the
// edit through the visor's own SetConfigFields RPC instead, so the visor's
// flush carries the change rather than clobbering it.
//
// Reading a value back is `config show`, which already reads the RUNNING
// visor's config (sk redacted) and honors the global --jq filter:
//
//	skywire cli config show --jq .transport.transport_port
package cliconfig

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor"
)

var setRestart bool

func init() {
	setCmd.Flags().BoolVar(&setRestart, "restart", false, "halt the visor after a restart-required change (the service manager restarts it)")
	setCmd.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr, "RPC server address (env: SKYWIRE_RPC)")
	RootCmd.AddCommand(setCmd)
}

// liveFieldHelp renders the live-field table from pkg/visor so the documented
// list and the implemented list cannot drift.
func liveFieldHelp() string {
	var b strings.Builder
	for _, f := range visor.LiveConfigFields() {
		fmt.Fprintf(&b, "  %-34s %s\n", f.Path, f.Desc)
	}
	return b.String()
}

var setCmd = &cobra.Command{
	Use:   "set <dotted.path>=<value> [<dotted.path>=<value>…]",
	Short: "Set config fields on the running visor",
	Long: `Set one or more config fields on the RUNNING visor.

The edit goes through the visor, so the visor's own flush carries it instead of
overwriting it — which is what happens to a hand-edit of skywire-config.json
while a visor is running.

Paths are the config's JSON field names joined by dots. A launcher app is
addressed by NAME rather than array position:

  skywire cli config set is_public=true
  skywire cli config set 'launcher.apps[skysocks].auto_start=false'
  skywire cli config set transport.transport_port=7777 routing.min_hops=1
  skywire cli config set log_level=debug --json
  skywire cli config set --via dmsg://<pk> is_public=true

Values are JSON. A bare word that is not valid JSON is taken as a string, so
log_level=debug and log_level='"debug"' are the same thing. Lists and objects
need real JSON: hypervisors='["<pk>","<pk>"]'.

Every field is validated against the config struct before ANY is applied, so a
typo in the third argument leaves the first two untouched. Unknown paths, type
mismatches and edits to sk/pk (or any *_sk / *secret_key field) are refused.

Output is one line per field:

  <path>: <old> -> <new> (live|restart-required)

"live" means a running subsystem took the value immediately. "restart-required"
means it was written to the config file and applies at the next visor start;
--restart halts the visor so the service manager (or the dev loop) restarts it.

Fields applied LIVE today — everything else is restart-required:

` + liveFieldHelp() + `
Read a value back with ` + "`config show`" + `, which reads the running visor and
honors the global --jq filter:

  skywire cli config show --jq .transport.transport_port`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		fields, err := parseSetArgs(args)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("visor not reachable: %w (config set edits the RUNNING visor; use `config update` to edit a config file)", err))
		}
		changes, err := rpcClient.SetConfigFields(fields)
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}

		var text strings.Builder
		needRestart := false
		for _, c := range changes {
			text.WriteString(c.String() + "\n")
			if !c.Live {
				needRestart = true
			}
		}
		if needRestart && !setRestart {
			text.WriteString("restart the visor for the restart-required fields to take effect (--restart does it)\n")
		}
		if needRestart && setRestart {
			// Same as `cli visor halt`: the RPC connection dies with the
			// visor, so the call's error is not a failure signal.
			rpcClient.Shutdown() //nolint:errcheck,gosec
			text.WriteString("visor halted; the service manager will restart it\n")
		}
		internal.PrintOutput(cmd.Flags(), changes, text.String())
	},
}

// parseSetArgs turns `path=value` arguments into the RPC payload. The value is
// used as JSON when it parses as JSON, and otherwise quoted into a JSON string
// — so `log_level=debug` works without the operator shell-escaping quotes,
// while `routing.min_hops=1` and `hypervisors=["…"]` keep their real types.
func parseSetArgs(args []string) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage, len(args))
	for _, arg := range args {
		eq := strings.IndexByte(arg, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("malformed argument %q: expected <dotted.path>=<value>", arg)
		}
		path := strings.TrimSpace(arg[:eq])
		val := arg[eq+1:]
		if path == "" {
			return nil, fmt.Errorf("malformed argument %q: empty path", arg)
		}
		if _, dup := out[path]; dup {
			return nil, fmt.Errorf("%s given twice", path)
		}
		if json.Valid([]byte(val)) {
			out[path] = json.RawMessage(val)
			continue
		}
		quoted, err := json.Marshal(val)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out[path] = quoted
	}
	return out, nil
}
