// Package autoconfigcmd pkg/skywireconfig/autoconfigcmd/describe.go c3-vis-core
package autoconfigcmd

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/cipher"
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
	// Check names the rule a value must pass beyond its format, one of the
	// Check* constants, so a form can test it before saving.
	Check string `json:"check,omitempty"`
}

// The value rules a variable can carry beyond its .conf format.
const (
	CheckPK       = "pk"       // one public key
	CheckPKs      = "pks"      // comma-separated public keys
	CheckHostPort = "hostport" // host:port, the host may be empty
	CheckDuration = "duration" // a Go duration such as 10s or 5m
	CheckLogLevel = "loglevel" // a logrus level name
)

// envChecks maps a variable to its rule. Variables not listed take any text.
var envChecks = map[string]string{
	"ADDVPNPK":         CheckPK,
	"PROXYCLIENTPK":    CheckPK,
	"HYPERVISORPKS":    CheckPKs,
	"DMSGPTYPKS":       CheckPKs,
	"PROXYSERVERWL":    CheckPKs,
	"VPNSERVERWL":      CheckPKs,
	"ROUTESETUPPKS":    CheckPKs,
	"TPSETUPPKS":       CheckPKs,
	"SURVEYPKS":        CheckPKs,
	"DMSGRELAYKEYS":    CheckPKs,
	"HVHTTPADDR":       CheckHostPort,
	"HVDESKADDR":       CheckHostPort,
	"SKYCHATADDR":      CheckHostPort,
	"SKYCOINWEBADDR":   CheckHostPort,
	"DMSGRELAYADDR":    CheckHostPort,
	"DMSGSERVERPUBLIC": CheckHostPort,
	"LANDMSGPUBLIC":    CheckHostPort,
	"REGTIMEOUT":       CheckDuration,
	"SHUTDOWNTIMEOUT":  CheckDuration,
	"LOGLVL":           CheckLogLevel,
}

// checkValue applies rule to value. An empty value always passes: it leaves
// the variable blank, which config gen reads as its default.
func checkValue(rule, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	switch rule {
	case CheckPK:
		var pk cipher.PubKey
		if err := pk.Set(value); err != nil {
			return fmt.Errorf("%q is not a public key", value)
		}
	case CheckPKs:
		for _, s := range strings.Split(value, ",") {
			if err := checkValue(CheckPK, s); err != nil {
				return err
			}
		}
	case CheckHostPort:
		_, port, err := net.SplitHostPort(value)
		if err != nil {
			return fmt.Errorf("%q is not host:port", value)
		}
		if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
			return fmt.Errorf("%q has no valid port", value)
		}
	case CheckDuration:
		if _, err := time.ParseDuration(value); err != nil {
			return fmt.Errorf("%q is not a duration like 10s or 5m", value)
		}
	case CheckLogLevel:
		if _, err := logrus.ParseLevel(value); err != nil {
			return fmt.Errorf("%q is not a log level (trace, debug, info, warn, error, fatal, panic)", value)
		}
	}
	return nil
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
			info.Check = envChecks[m.Key]
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
		if strings.TrimSpace(value) == "" {
			return skyenvfile.Edit{Key: m.Key, Unset: true}, nil
		}
		i, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return skyenvfile.Edit{}, fmt.Errorf("--%s: %q is not an integer", flag, value)
		}
		e.Value = skyenvfile.FormatInt(i)
	case EnvFormatBashArray:
		if err := checkValue(envChecks[m.Key], value); err != nil {
			return skyenvfile.Edit{}, fmt.Errorf("--%s: %w", flag, err)
		}
		e.Value = skyenvfile.FormatBashArray(value)
	default:
		if err := checkValue(envChecks[m.Key], value); err != nil {
			return skyenvfile.Edit{}, fmt.Errorf("--%s: %w", flag, err)
		}
		e.Value = skyenvfile.FormatString(value)
	}
	return e, nil
}

// KeyFor returns the .conf variable a flag writes.
func KeyFor(flag string) (string, bool) {
	m, ok := envMap[flag]
	return m.Key, ok
}
