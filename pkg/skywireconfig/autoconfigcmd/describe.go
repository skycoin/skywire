// Package autoconfigcmd pkg/skywireconfig/autoconfigcmd/describe.go c3-vis-core
package autoconfigcmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/skywireconfig/skyenvfile"
)

// Flag is one autoconfig flag as a settings form renders it: the flag
// itself plus the /etc/skywire.conf variable it writes. The install-command
// generator and the visor's settings page read this same shape, so a flag
// added to New shows up in both without either being edited.
type Flag struct {
	Name        string `json:"name"`
	Short       string `json:"short,omitempty"`
	Type        string `json:"type"` // "bool" | "string" | "int"
	DefaultVal  string `json:"default,omitempty"`
	Description string `json:"description"`

	// EnvKey is the variable this flag writes; empty for flags that only
	// affect one autoconfig run (--verbose, --no-restart).
	EnvKey string `json:"env_key,omitempty"`
	// EnvFormat is the .conf encoding: "bool", "string", "int", "bashArray".
	EnvFormat string `json:"env_format,omitempty"`
	// EnvNegate marks a --no-X flag, which writes KEY=false.
	EnvNegate bool `json:"env_negate,omitempty"`
	// EnvDefault is what config gen uses while the variable is unset.
	EnvDefault string `json:"env_default,omitempty"`
	// EnvNote is guidance about how this variable interacts with another.
	EnvNote string `json:"env_note,omitempty"`
}

// Describe returns every autoconfig flag in registration order, the order
// operators see in --help.
func Describe() []Flag {
	cmd := New(&Values{})
	var out []Flag
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		info := Flag{
			Name:        f.Name,
			Short:       f.Shorthand,
			Type:        f.Value.Type(),
			DefaultVal:  f.DefValue,
			Description: f.Usage,
		}
		if m, ok := envMap[f.Name]; ok {
			info.EnvKey = m.Key
			info.EnvFormat = string(m.Format)
			info.EnvNegate = m.Negate
			info.EnvDefault = m.Default
			info.EnvNote = m.Note
		}
		out = append(out, info)
	})
	return out
}

// EditFor returns the .conf edit that `skywire autoconfig --<flag>=<value>`
// makes. value is the flag's text: "true"/"false" for a bool, a number for an
// int, a comma-separated list for an array.
func EditFor(flag, value string) (skyenvfile.Edit, error) {
	m, ok := envMap[flag]
	if !ok {
		return skyenvfile.Edit{}, fmt.Errorf("%q is not an autoconfig flag that writes skywire.conf", flag)
	}
	e := skyenvfile.Edit{Key: m.Key, Raw: value}
	switch m.Format {
	case EnvFormatBool:
		b, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return skyenvfile.Edit{}, fmt.Errorf("--%s: %q is not a bool", flag, value)
		}
		if m.Negate {
			b = !b
		}
		e.Value = skyenvfile.FormatBool(b)
		e.Raw = e.Value
	case EnvFormatInt:
		i, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return skyenvfile.Edit{}, fmt.Errorf("--%s: %q is not an integer", flag, value)
		}
		e.Value = skyenvfile.FormatInt(i)
	case EnvFormatBashArray:
		e.Value = skyenvfile.FormatBashArray(value)
	default:
		e.Value = skyenvfile.FormatString(value)
	}
	return e, nil
}

// KeyFor returns the .conf variable a flag writes.
func KeyFor(flag string) (string, bool) {
	m, ok := envMap[flag]
	return m.Key, ok
}
