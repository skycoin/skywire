// Package visor pkg/visor/api_app_flags.go c3-vis-core
package visor

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/skywireconfig/autoconfigcmd"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// appFlagChecks gives app flags the value rules the settings form already
// applies to skyenv variables (autoconfigcmd.Check*). Durations get theirs by type.
var appFlagChecks = map[string]string{
	"srv":             autoconfigcmd.CheckPK,
	"pk":              autoconfigcmd.CheckPK,
	"market-pk":       autoconfigcmd.CheckPK,
	"cxo-group-owner": autoconfigcmd.CheckPK,
	"whitelist":       autoconfigcmd.CheckPKs,
	"tcp-whitelist":   autoconfigcmd.CheckPKs,
	"addr":            autoconfigcmd.CheckHostPort,
	"rpc":             autoconfigcmd.CheckHostPort,
	"pair-rpc":        autoconfigcmd.CheckHostPort,
	"cxo-listen":      autoconfigcmd.CheckHostPort,
	"tcp-listen":      autoconfigcmd.CheckHostPort,
}

// skipAppFlag drops flags that are not settings. The launcher owns the
// routing port through AppConfig.Port.
func skipAppFlag(name string) bool {
	return name == "help" || name == "version" || name == "port"
}

// AppFlags describes the flags the named app accepts and the values its
// launcher args give them. In-process apps report the flag set they
// registered; any other app's flags are read from its cached --help output.
func (v *Visor) AppFlags(appName string) (visorapi.AppFlags, error) {
	if v.appL == nil {
		return visorapi.AppFlags{}, ErrAppLauncherNotAvailable
	}
	var ac *appserver.AppConfig
	for i := range v.conf.Launcher.Apps {
		if v.conf.Launcher.Apps[i].Name == appName {
			ac = &v.conf.Launcher.Apps[i]
			break
		}
	}
	if ac == nil {
		return visorapi.AppFlags{}, fmt.Errorf("app %q not found", appName)
	}

	out := visorapi.AppFlags{App: appName, Source: visorapi.FlagSourceRegistry}
	if flags, ok := registeredAppFlags(*ac); ok {
		out.Flags = flags
	} else {
		help, err := v.AppHelp(appName)
		if err != nil {
			return visorapi.AppFlags{}, err
		}
		out.Source = visorapi.FlagSourceHelp
		out.Flags = parseHelpFlags(help)
	}
	applyArgValues(out.Flags, ac.Args)
	return out, nil
}

// registeredAppFlags looks up the flag set an in-process app registered,
// by binary, name, or the app name in "app <name>" args.
func registeredAppFlags(ac appserver.AppConfig) ([]visorapi.AppFlag, bool) {
	candidates := []string{ac.Binary, ac.Name}
	if len(ac.Args) > 1 && ac.Args[0] == "app" {
		candidates = append(candidates, ac.Args[1])
	}
	for _, name := range candidates {
		if name == "" {
			continue
		}
		flags := []visorapi.AppFlag{}
		if launcher.VisitAppFlags(name, func(f *pflag.Flag) {
			if f.Hidden || skipAppFlag(f.Name) {
				return
			}
			flags = append(flags, flagFromPflag(f))
		}) {
			return flags, true
		}
	}
	return nil, false
}

func flagFromPflag(f *pflag.Flag) visorapi.AppFlag {
	typ := flagType(f.Value.Type())
	def := f.DefValue
	if typ == visorapi.FlagTypeStringList {
		def = strings.TrimSuffix(strings.TrimPrefix(def, "["), "]")
	}
	return visorapi.AppFlag{
		Name:      f.Name,
		Shorthand: f.Shorthand,
		Type:      typ,
		Default:   def,
		Usage:     f.Usage,
		Check:     flagCheck(f.Name, typ),
	}
}

// flagType maps a pflag type name, or the type word of a help line, to an
// AppFlag type. An empty word is a bool, which help prints without a type.
func flagType(t string) string {
	switch {
	case t == "" || t == "bool":
		return visorapi.FlagTypeBool
	case t == "duration":
		return visorapi.FlagTypeDuration
	case t == "strings" || strings.HasPrefix(t, "stringSlice") || strings.HasPrefix(t, "stringArray"):
		return visorapi.FlagTypeStringList
	case strings.HasPrefix(t, "int") || strings.HasPrefix(t, "uint"):
		return visorapi.FlagTypeInt
	}
	return visorapi.FlagTypeString
}

func flagCheck(name, typ string) string {
	if typ == visorapi.FlagTypeDuration {
		return autoconfigcmd.CheckDuration
	}
	if typ == visorapi.FlagTypeString {
		return appFlagChecks[name]
	}
	return ""
}

var (
	// pflag:  "  -c, --config string   usage (default "x")"
	pflagLine = regexp.MustCompile(`^\s+(?:-([A-Za-z0-9]), )?--([A-Za-z0-9][A-Za-z0-9_.-]*)(?: ([A-Za-z0-9]+))?(?:\s{2,}(.*))?$`)
	// Go flag: "  -addr string" with the usage on the next, tab-indented line.
	goFlagLine = regexp.MustCompile(`^  -([A-Za-z0-9][A-Za-z0-9_.-]*)(?: ([A-Za-z0-9]+))?(?:\t(.*))?$`)
	defaultRe  = regexp.MustCompile(`\s*\(default (.*)\)$`)
)

// parseHelpFlags reads flags from pflag/cobra or Go flag help output, for
// apps with no registered flag set.
func parseHelpFlags(help string) []visorapi.AppFlag {
	flags := []visorapi.AppFlag{}
	prev := -1
	for _, line := range strings.Split(help, "\n") {
		line = strings.TrimRight(line, " \r")
		var name, short, word, usage string
		if m := pflagLine.FindStringSubmatch(line); m != nil {
			short, name, word, usage = m[1], m[2], m[3], m[4]
		} else if m := goFlagLine.FindStringSubmatch(line); m != nil {
			name, word, usage = m[1], m[2], m[3]
		} else {
			// A tab or deep indent continues the previous flag's usage.
			t := strings.TrimSpace(line)
			if prev >= 0 && t != "" && (strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "    ")) {
				flags[prev].Usage = strings.TrimSpace(flags[prev].Usage + " " + t)
				continue
			}
			prev = -1
			continue
		}
		prev = -1
		if skipAppFlag(name) {
			continue
		}
		flags = append(flags, visorapi.AppFlag{Name: name, Shorthand: short, Type: flagType(word), Usage: usage})
		prev = len(flags) - 1
	}
	for i := range flags {
		f := &flags[i]
		if m := defaultRe.FindStringSubmatch(f.Usage); m != nil {
			f.Usage = strings.TrimSpace(f.Usage[:len(f.Usage)-len(m[0])])
			def := m[1]
			if s, err := strconv.Unquote(def); err == nil {
				def = s
			}
			f.Default = strings.TrimSuffix(strings.TrimPrefix(def, "["), "]")
		} else if f.Type == visorapi.FlagTypeBool {
			f.Default = "false"
		}
		f.Check = flagCheck(f.Name, f.Type)
	}
	return flags
}

// applyArgValues marks the flags the launcher args set and records their
// values. A list flag given several times joins its values with commas.
func applyArgValues(flags []visorapi.AppFlag, args []string) {
	byName := map[string]int{}
	for i, f := range flags {
		byName[f.Name] = i
		if f.Shorthand != "" {
			byName[f.Shorthand] = i
		}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" || a == "--" {
			continue
		}
		key, val, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		idx, ok := byName[key]
		if !ok {
			continue
		}
		f := &flags[idx]
		if !hasVal {
			if f.Type == visorapi.FlagTypeBool {
				val = "true"
			} else if i+1 < len(args) {
				i++
				val = args[i]
			}
		}
		if f.Set && f.Type == visorapi.FlagTypeStringList {
			f.Value += "," + val
		} else {
			f.Value = val
		}
		f.Set = true
	}
}
