// Package skysocksc cmd/skywire-cli/commands/proxy/settings.go
//
// `proxy settings` — the live tuning knobs of a RUNNING skysocks-client.
//
// Everything else that shapes a proxy app is either an argv flag (an app
// restart, which re-dials every tunnel: 30-70 s of settling before a
// measurement is valid) or a compile-time constant (a rebuild and a fleet
// deploy). This command is the third option: the visor holds a value set per
// app and the app pulls it on the keepalive tick it already runs, so a sweep
// costs one tick (tunnel.probe_interval, 5 s by default) and no redeploy.
//
// Structurally this is `route settings` for the app side: the visor RPC pair is
// Get/SetAppSettings, and the knob catalog — names, kinds and compiled
// defaults — comes from pkg/skysocks/skysettings, which the app reads too.
//
//	skywire cli proxy settings                                  # the table
//	skywire cli proxy settings --json                           # machine-readable
//	skywire cli proxy settings chunk.max_bytes=8MiB pool.fill_interval=500ms
//	skywire cli proxy settings --reset tunnel.promote_margin    # one knob back
//	skywire cli proxy settings --reset                          # all of them
package skysocksc

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cliout"
	"github.com/skycoin/skywire/pkg/cliout/cliproxy"
	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
)

var (
	settingsApp   string
	settingsReset bool
)

func init() {
	settingsCmd.Flags().StringVar(&settingsApp, "app", "skysocks-client", "app whose knobs to read or set")
	settingsCmd.Flags().BoolVar(&settingsReset, "reset", false, "drop the named knobs (or all of them) back to the compiled defaults")
	RootCmd.AddCommand(settingsCmd)
}

var settingsCmd = &cobra.Command{
	Use:   "settings [key=value ...]",
	Short: "Show or set a running proxy app's live tuning knobs",
	Long: `Show or set the live tuning knobs of a RUNNING proxy app — the standby-pool
cadence, the tunnel promoter's thresholds and the range-split chunk shape —
without restarting it.

With no arguments the current table is printed: each knob's value, the value the
app compiled with, its kind, and whether the app has installed it yet. A knob
still marked pending reaches the app on its next pull, one tunnel.probe_interval
away (5 s by default).

Values take units: bytes as 8MiB / 512KiB / a plain byte count, durations in Go
syntax (250ms, 5s, 4m), ratios and counts as plain numbers.

Knobs PERSIST: they outlive 'proxy stop' and the app restart it implies, and
the visor writes them to its config so they outlive the visor too. --reset is
the one thing that forgets one. The visor-wide router knobs are the separate
'skywire cli route settings'.

A LIST knob (pool.exclude_pks, pool.require_tp_types) takes comma-separated
tokens; an empty value clears it.

Examples:
  skywire cli proxy settings
  skywire cli proxy settings chunk.max_bytes=8MiB chunk.concurrency=16
  skywire cli proxy settings tunnel.promote_margin=1.1 tunnel.audition_every=20s
  skywire cli proxy settings --reset chunk.max_bytes`,
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		cur, err := rpcClient.GetAppSettings(settingsApp)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		if len(args) > 0 || settingsReset {
			next, nextText, err := applySettingArgs(cur.Values, cur.Text, args, settingsReset)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			if cur, err = rpcClient.SetAppSettings(settingsApp, next, nextText); err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
		}
		internal.Catch(cmd.Flags(), cliout.Print(cmd, settingsRows(settingsApp, cur.Values, cur.Text, cur.Version, cur.Applied)))
	},
}

// applySettingArgs folds `key=value` arguments (or, under reset, bare keys)
// into the value sets the visor already holds. A reset with no keys clears
// everything; with keys it drops exactly those.
//
// The LIST knobs (pool.exclude_pks, pool.require_tp_types) go into the second
// map: their payload is a token set rather than an int64, and the split is
// what keeps the wire honest about which is which.
//
// Split out of the command body so the parsing is testable without an RPC.
func applySettingArgs(current map[string]int64, currentText map[string]string, args []string, reset bool) (map[string]int64, map[string]string, error) {
	next := make(map[string]int64, len(current)+len(args))
	for k, v := range current {
		next[k] = v
	}
	nextText := make(map[string]string, len(currentText)+len(args))
	for k, v := range currentText {
		nextText[k] = v
	}
	if reset && len(args) == 0 {
		return nil, nil, nil
	}
	for _, a := range args {
		name, raw, ok := strings.Cut(a, "=")
		name = strings.TrimSpace(name)
		if reset {
			if ok {
				return nil, nil, fmt.Errorf("--reset takes bare knob names, got %q", a)
			}
			if !knownSetting(name) {
				return nil, nil, fmt.Errorf("unknown setting %q", name)
			}
			delete(next, name)
			delete(nextText, name)
			continue
		}
		if !ok {
			return nil, nil, fmt.Errorf("expected key=value, got %q", a)
		}
		if skysettings.IsList(name) {
			v, err := skysettings.ParseList(name, raw)
			if err != nil {
				return nil, nil, err
			}
			if v == "" {
				delete(nextText, name) // an empty list IS the default
				continue
			}
			nextText[name] = v
			continue
		}
		v, err := skysettings.Parse(name, raw)
		if err != nil {
			return nil, nil, err
		}
		next[name] = v
	}
	if len(next) == 0 {
		next = nil
	}
	if len(nextText) == 0 {
		nextText = nil
	}
	return next, nextText, nil
}

func knownSetting(name string) bool {
	for _, d := range skysettings.Catalog() {
		if d.Name == name {
			return true
		}
	}
	return false
}

// settingsRows renders the catalog against the values the visor holds. A knob
// the visor does not name is at its compiled default; one it names is pending
// until the app reports a version that carries it.
func settingsRows(app string, vals map[string]int64, text map[string]string, version, applied uint64) cliproxy.Settings {
	out := cliproxy.Settings{App: app, Version: version, Applied: applied}
	for _, d := range skysettings.Catalog() {
		row := cliproxy.SettingsEntry{
			Name:    d.Name,
			Value:   skysettings.Format(d.Name, d.Default),
			Default: skysettings.Format(d.Name, d.Default),
			Kind:    string(d.Kind),
			State:   "default",
			Doc:     d.Doc,
		}
		if d.Kind == skysettings.KindList {
			// A list knob's default is the empty set, and its value comes from
			// the visor's text map — never from this process's own registry,
			// which the CLI never applies anything to.
			row.Value, row.Default = "", ""
			if v, ok := text[d.Name]; ok {
				row.Value = v
				row.State = "pending"
				if applied >= version {
					row.State = "applied"
				}
			}
			out.Knobs = append(out.Knobs, row)
			continue
		}
		if v, ok := vals[d.Name]; ok {
			row.Value = skysettings.Format(d.Name, v)
			row.State = "pending"
			if applied >= version {
				row.State = "applied"
			}
		}
		out.Knobs = append(out.Knobs, row)
	}
	return out
}
