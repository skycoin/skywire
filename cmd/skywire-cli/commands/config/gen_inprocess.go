// Package cliconfig cmd/skywire-cli/commands/config/gen_inprocess.go c4-vis-cli
package cliconfig

import (
	"fmt"
	"sync"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var runGenMu sync.Mutex

// genFatal is the panic a Fatal inside RunGen turns into.
type genFatal struct{ code int }

// RunGen runs `config gen` with args inside the calling process and returns
// its failure as an error. It exists for the iOS core (pkg/mobilecore),
// which cannot exec the command as Android does; the output is the same
// config file `skywire-mobile config gen <args>` writes.
//
// The command keeps its state in package variables and was written for one
// run per process, so RunGen first puts every gen flag back to its default
// and clears the per-run state (resetGenRunState) — a second call must not
// inherit the first one's key, hypervisors or apps. A Fatal inside the
// command, which would otherwise end the host, is turned into the returned
// error. Calls are serialized. Not for flags that print and exit (-q, --all,
// --envfile): those still call os.Exit.
func RunGen(args []string) (err error) {
	runGenMu.Lock()
	defer runGenMu.Unlock()

	cmd := genConfigCmd
	resetFlags(cmd.Flags())
	resetGenRunState()

	prev := logging.SetExitFunc(func(code int) { panic(genFatal{code: code}) })
	defer func() { logging.SetExitFunc(prev) }()
	defer func() {
		if r := recover(); r != nil {
			f, ok := r.(genFatal)
			if !ok {
				panic(r)
			}
			err = fmt.Errorf("config gen failed (exit %d); the reason is in the log", f.code)
		}
	}()

	if err := cmd.ParseFlags(args); err != nil {
		return fmt.Errorf("config gen: %w", err)
	}
	return runCmd(cmd, cmd.Flags().Args())
}

// runCmd runs cmd's own hooks directly rather than through Execute: Execute
// runs from the root command, which in a binary that mounts this package
// under another root would parse that root's arguments instead of args.
func runCmd(cmd *cobra.Command, args []string) error {
	if cmd.PreRun != nil {
		cmd.PreRun(cmd, args)
	}
	if cmd.Run != nil {
		cmd.Run(cmd, args)
	}
	return nil
}

// resetFlags puts every flag in fs back to its default, as if unparsed.
// DefValue is the string the flag was registered with (or the one
// refreshSkyenvDefaults re-evaluated), so Set restores the bound variable.
func resetFlags(fs *pflag.FlagSet) {
	fs.VisitAll(func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue) //nolint:errcheck // the zero --sk does not parse back; reset below
		f.Changed = false
	})
	sk = cipher.SecKey{}
}

// resetGenRunState clears what a gen run leaves behind outside its flags:
// the config it builds up field by field (some fields are appended to), the
// previous config it read for -r, and the output path it resolved.
func resetGenRunState() {
	conf = new(visorconfig.V1)
	oldConfCache = nil
	isOutUnset = false
	confPath = ""
	configName = ""
}
