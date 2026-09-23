// Package cliconfig cmd/skywire-cli/commands/config/skyenvdefaults.go c4-vis-cli
package cliconfig

import (
	"strconv"

	"github.com/spf13/pflag"
)

// Flag defaults that come from the SKYENV file are evaluated when the
// flag is registered, which is this package's init phase. That is early
// enough to be wrong: `skywire autoconfig` on js/wasm re-enters `config
// gen` inside the SAME process (autoconfig_exec_js.go), so gen's
// registrations have already run — with whatever SKYENV resolved to
// then — before autoconfig points at the file it just wrote. Measured
// in the browser visor: with WISP=true in /etc/skywire.conf, the
// init-time default of --wisp was false and the generated config had no
// wisp section, while run-time reads of the same file in the same
// process returned the right value; a fresh standalone `config gen`
// emitted the section. Whatever makes the init-time read stale, the
// value a flag defaults to is only well defined once the command runs.
//
// So record the expression alongside the flag and re-evaluate it at run
// time for every flag the caller did not set explicitly. Natively,
// where SKYENV is already set at process start, both reads see the same
// file and behavior is unchanged.

// skyenvFlagDefault is one registered flag whose default comes from the
// SKYENV file: its name, the expression that produces it, the value
// that expression yielded at registration time, and a re-evaluation
// that returns the flag's string form.
type skyenvFlagDefault struct {
	name    string
	expr    string
	initVal string
	eval    func() string
}

// skyenvFlagDefaults is keyed by flag set so that a command (or a test)
// only ever refreshes its own flags.
var skyenvFlagDefaults = map[*pflag.FlagSet][]skyenvFlagDefault{}

func recordSkyenvDefault(fs *pflag.FlagSet, name, expr string, eval func() string) {
	skyenvFlagDefaults[fs] = append(skyenvFlagDefaults[fs], skyenvFlagDefault{
		name:    name,
		expr:    expr,
		initVal: eval(),
		eval:    eval,
	})
}

// refreshSkyenvDefaults re-evaluates every SKYENV-derived default in fs
// against the env file as it is NOW, and applies the result to flags
// the caller did not set on the command line. It writes through
// flag.Value, which is bound to the same variable the registration
// was given, so the bound variable and cobra's view stay in agreement.
// Changed is deliberately left alone: a refreshed default is still a
// default, and callers test Changed to tell the two apart.
func refreshSkyenvDefaults(fs *pflag.FlagSet) {
	for _, d := range skyenvFlagDefaults[fs] {
		f := fs.Lookup(d.name)
		if f == nil || f.Changed {
			continue
		}
		v := d.eval()
		if v == f.Value.String() {
			continue
		}
		if err := f.Value.Set(v); err != nil {
			continue
		}
		f.DefValue = v
	}
}

// skyenvDefaultValues reports the registration-time and current values
// of one recorded flag. Used to log what the refresh changed.
func skyenvDefaultValues(fs *pflag.FlagSet, name string) (initVal, runVal string) {
	for _, d := range skyenvFlagDefaults[fs] {
		if d.name != name {
			continue
		}
		runVal = d.initVal
		if f := fs.Lookup(d.name); f != nil {
			runVal = f.Value.String()
		}
		return d.initVal, runVal
	}
	return "", ""
}

// The registration helpers below mirror the pflag methods they wrap,
// taking the SKYENV expression where pflag takes a literal default.

func skyenvStringVar(fs *pflag.FlagSet, p *string, name, expr, usage string) {
	fs.StringVar(p, name, scriptExecString(expr), usage)
	recordSkyenvDefault(fs, name, expr, func() string { return scriptExecString(expr) })
}

func skyenvStringVarP(fs *pflag.FlagSet, p *string, name, shorthand, expr, usage string) {
	fs.StringVarP(p, name, shorthand, scriptExecString(expr), usage)
	recordSkyenvDefault(fs, name, expr, func() string { return scriptExecString(expr) })
}

func skyenvArrayVar(fs *pflag.FlagSet, p *string, name, expr, usage string) {
	fs.StringVar(p, name, scriptExecArray(expr), usage)
	recordSkyenvDefault(fs, name, expr, func() string { return scriptExecArray(expr) })
}

func skyenvArrayVarP(fs *pflag.FlagSet, p *string, name, shorthand, expr, usage string) {
	fs.StringVarP(p, name, shorthand, scriptExecArray(expr), usage)
	recordSkyenvDefault(fs, name, expr, func() string { return scriptExecArray(expr) })
}

func skyenvBoolVar(fs *pflag.FlagSet, p *bool, name, expr, usage string) {
	fs.BoolVar(p, name, scriptExecBool(expr), usage)
	recordSkyenvDefault(fs, name, expr, func() string { return strconv.FormatBool(scriptExecBool(expr)) })
}

func skyenvBoolVarP(fs *pflag.FlagSet, p *bool, name, shorthand, expr, usage string) {
	fs.BoolVarP(p, name, shorthand, scriptExecBool(expr), usage)
	recordSkyenvDefault(fs, name, expr, func() string { return strconv.FormatBool(scriptExecBool(expr)) })
}

func skyenvIntVar(fs *pflag.FlagSet, p *int, name, expr, usage string) {
	fs.IntVar(p, name, scriptExecInt(expr), usage)
	recordSkyenvDefault(fs, name, expr, func() string { return strconv.Itoa(scriptExecInt(expr)) })
}

func skyenvUintVar(fs *pflag.FlagSet, p *uint, name, expr, usage string) {
	fs.UintVar(p, name, scriptExecUint(expr), usage)
	recordSkyenvDefault(fs, name, expr, func() string { return strconv.FormatUint(uint64(scriptExecUint(expr)), 10) })
}
