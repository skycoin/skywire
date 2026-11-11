// Package ui2 pkg/visor/ui2/flags.go c3-vis-core
package ui2

import (
	"strings"

	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// The app flags form, as app-flags-form.component.ts and app-flags.ts.

// FlagRow is one flag of the form with the value the form shows and why it
// was refused, if it was.
type FlagRow struct {
	visorapi.AppFlag
	Err string
}

// AppFlagsView is the flags form of one app.
type AppFlagsView struct {
	PK    string
	App   string
	Flags []FlagRow
	Err   string
}

// FlagsViewOf shows each flag with its value from the launcher args, or its
// default when the args leave it unset.
func FlagsViewOf(pk string, f visorapi.AppFlags) AppFlagsView {
	v := AppFlagsView{PK: pk, App: f.App}
	for _, fl := range f.Flags {
		fl.Default = FlagDefault(fl)
		if !fl.Set {
			fl.Value = fl.Default
		}
		v.Flags = append(v.Flags, FlagRow{AppFlag: fl})
	}
	return v
}

func lookupFlag(flags []visorapi.AppFlag, key string) (visorapi.AppFlag, bool) {
	for _, f := range flags {
		if f.Name == key || (f.Shorthand != "" && f.Shorthand == key) {
			return f, true
		}
	}
	return visorapi.AppFlag{}, false
}

// BuildFlagArgs rewrites args with the given flag values: leading
// positional args and tokens that are not known flags stay, every known
// flag is dropped, then each flag in set is written back, as buildFlagArgs
// in app-flags.ts does.
func BuildFlagArgs(args []string, flags []visorapi.AppFlag, values map[string]string, set map[string]bool) []string {
	var out []string
	i := 0
	for ; i < len(args) && !strings.HasPrefix(args[i], "-"); i++ {
		out = append(out, args[i])
	}
	for ; i < len(args); i++ {
		t := args[i]
		key, _, hasEq := strings.Cut(strings.TrimLeft(t, "-"), "=")
		f, ok := lookupFlag(flags, key)
		if !strings.HasPrefix(t, "-") || !ok {
			out = append(out, t)
			continue
		}
		if !hasEq && f.Type != visorapi.FlagTypeBool && i+1 < len(args) {
			i++
		}
	}
	for _, f := range flags {
		if !set[f.Name] {
			continue
		}
		v := values[f.Name]
		switch f.Type {
		case visorapi.FlagTypeStringList:
			n := 0
			for item := range strings.SplitSeq(v, ",") {
				if item = strings.TrimSpace(item); item != "" {
					out = append(out, "--"+f.Name+"="+item)
					n++
				}
			}
			if n == 0 {
				out = append(out, "--"+f.Name+"=")
			}
		case visorapi.FlagTypeBool:
			if v == "true" {
				out = append(out, "--"+f.Name)
			} else {
				out = append(out, "--"+f.Name+"=false")
			}
		default:
			out = append(out, "--"+f.Name+"="+v)
		}
	}
	return out
}

// FlagDefault is the flag's default as the form shows it. An empty list
// reads "[]" in pflag help output.
func FlagDefault(f visorapi.AppFlag) string {
	if f.Type == visorapi.FlagTypeStringList && f.Default == "[]" {
		return ""
	}
	return f.Default
}
