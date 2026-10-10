// Package autoconfigui pkg/skywireconfig/autoconfigui/model.go c4-vis-cli
//
// The interactive form behind `skywire autoconfig i`. The model here turns
// autoconfigcmd.Describe and the current skyenv file into editable fields and
// turns edited fields back into autoconfig flag arguments. The terminal and
// web views only render fields and hand edits back, so neither writes
// skywire.conf: saving always runs the real autoconfig command.
package autoconfigui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/skycoin/skywire/pkg/skywireconfig/autoconfigcmd"
	"github.com/skycoin/skywire/pkg/skywireconfig/skyenvfile"
)

// Field is one setting in the form.
type Field struct {
	Name    string `json:"name"`
	Group   string `json:"group"`
	Type    string `json:"type"` // "bool" | "string" | "int"
	Help    string `json:"help"`
	Note    string `json:"note,omitempty"`
	EnvKey  string `json:"env_key"`
	Default string `json:"default,omitempty"`
	Secret  bool   `json:"secret,omitempty"`
	// Current is the value in the skyenv file, or the default when unset.
	Current string `json:"current"`
	// Set reports whether the file defines the variable.
	Set bool `json:"set"`
	// Value is the edited text, equal to Current until the operator edits.
	Value string `json:"value"`

	negName string // the paired --no-X flag, when there is one
	negate  bool   // the flag is a --no-X flag without a positive twin
	array   bool
}

// Changed reports whether the field differs from what is in the file.
func (f *Field) Changed() bool { return f.Value != f.Current }

// Model is the whole form.
type Model struct {
	Fields []*Field `json:"fields"`
	// NoRestart adds --no-restart so the service is not restarted on apply.
	NoRestart bool `json:"no_restart"`
	// Path is the skyenv file the current values came from.
	Path string `json:"path"`
}

var groupOrder = []string{
	"Hypervisor", "Identity and rewards", "Networking and transports", "Dmsg server", "Deployment",
	"Privacy and routing", "Whitelists", "Resolvers and web bridges", "VPN server",
	"VPN router", "Proxy", "Apps", "Skycoin", "Runtime", "Other",
}

var groupExact = map[string]string{
	"hvpks": "Hypervisor", "ishv": "Hypervisor", "hvdeskaddr": "Hypervisor",
	"pk-endpoint": "Hypervisor", "hv-auth": "Hypervisor", "hvaddr": "Hypervisor",
	"sk": "Identity and rewards", "version": "Identity and rewards",
	"rewardaddr": "Identity and rewards", "public": "Identity and rewards",
	"publicip": "Identity and rewards", "disable-public-autoconn": "Identity and rewards",
	"testenv": "Networking and transports", "url": "Networking and transports",
	"svcconf": "Networking and transports", "minsess": "Networking and transports",
	"maxtransports": "Networking and transports", "stun": "Networking and transports",
	"stcpr": "Networking and transports", "sudph": "Networking and transports",
	"transport-port": "Networking and transports", "lan-dmsg-port": "Networking and transports",
	"lan-dmsg-public": "Networking and transports", "ws-peer": "Networking and transports",
	"min-hops": "Privacy and routing", "ar-transport-limit": "Privacy and routing",
	"no-direct-transports": "Privacy and routing", "pty-rpc-exec": "Privacy and routing",
	"calculate-routes": "Privacy and routing",
	"dmsgpty-pks":      "Whitelists", "survey": "Whitelists", "routesetup": "Whitelists",
	"tpsetup": "Whitelists", "skydeploy": "Deployment", "resolvers": "Resolvers and web bridges",
	"wisp-socks": "Resolvers and web bridges", "vpnserver": "VPN server",
	"killsw": "VPN server", "addvpn": "VPN server", "vpnwl": "VPN server",
	"secure": "VPN server", "netifc": "VPN server",
	"binpath": "Runtime", "loglvl": "Runtime", "timeout": "Runtime", "regtimeout": "Runtime",
}

var groupPrefix = []struct{ prefix, group string }{
	{"dmsg-server", "Dmsg server"}, {"dmsg-relay", "Dmsg server"}, {"deployment", "Deployment"},
	{"vpnrouter", "VPN router"}, {"proxy", "Proxy"}, {"startproxy", "Proxy"},
	{"dmsgweb", "Resolvers and web bridges"}, {"skynetweb", "Resolvers and web bridges"},
	{"skycoin", "Skycoin"}, {"skychat", "Apps"}, {"chat", "Apps"},
	{"servechat", "Apps"}, {"skymail", "Apps"},
}

// GroupOf returns the form section a flag belongs to.
func GroupOf(name string) string {
	if g, ok := groupExact[name]; ok {
		return g
	}
	if n := strings.TrimPrefix(name, "no-"); n != name {
		if g, ok := groupExact[n]; ok {
			return g
		}
		name = n
	}
	for _, p := range groupPrefix {
		if strings.HasPrefix(name, p.prefix) {
			return p.group
		}
	}
	return "Other"
}

// Groups returns the group names that have fields, in display order.
func (m *Model) Groups() []string {
	have := map[string]bool{}
	for _, f := range m.Fields {
		have[f.Group] = true
	}
	var out []string
	for _, g := range groupOrder {
		if have[g] {
			out = append(out, g)
		}
	}
	return out
}

// In returns the fields of one group.
func (m *Model) In(group string) []*Field {
	var out []*Field
	for _, f := range m.Fields {
		if f.Group == group {
			out = append(out, f)
		}
	}
	return out
}

var secretFlags = map[string]bool{"sk": true, "vpnrouter-passphrase": true, "dmsgweb-sk": true}

// NewModel builds the form from the flag descriptions and the skyenv file at
// path. A missing file is not an error, every field then shows its default.
func NewModel(flags []autoconfigcmd.Flag, path string) *Model {
	vals, _ := skyenvfile.Values(path) //nolint:errcheck // a missing file means defaults
	m := &Model{Path: path}
	byName := map[string]autoconfigcmd.Flag{}
	for _, fl := range flags {
		byName[fl.Name] = fl
	}
	for _, fl := range flags {
		if fl.EnvKey == "" {
			continue
		}
		f := &Field{
			Name: fl.Name, Group: GroupOf(fl.Name), Type: fl.Type, Help: fl.Description,
			Note: fl.EnvNote, EnvKey: fl.EnvKey, Default: fl.EnvDefault,
			Secret: secretFlags[fl.Name], array: fl.EnvFormat == string(autoconfigcmd.EnvFormatBashArray),
		}
		if fl.EnvNegate {
			if _, ok := byName[strings.TrimPrefix(fl.Name, "no-")]; ok {
				continue // folded into the positive flag
			}
			f.negate = true
		} else if neg, ok := byName["no-"+fl.Name]; ok && neg.EnvKey == fl.EnvKey {
			f.negName = neg.Name
		}
		raw, set := vals[fl.EnvKey]
		f.Set = set
		if !set {
			raw = fl.EnvDefault
		}
		switch {
		case f.Type == "bool":
			b, _ := strconv.ParseBool(strings.TrimSpace(raw)) //nolint:errcheck // junk reads as false
			if f.negate {
				b = !b
			}
			raw = strconv.FormatBool(b)
		case f.array:
			raw = strings.Join(strings.Fields(raw), ",")
		}
		if f.Secret {
			raw = ""
		}
		f.Current, f.Value = raw, raw
		m.Fields = append(m.Fields, f)
	}
	return m
}

// Set edits a field by flag name.
func (m *Model) Set(name, value string) error {
	for _, f := range m.Fields {
		if f.Name == name {
			f.Value = value
			return nil
		}
	}
	return fmt.Errorf("unknown setting %q", name)
}

// Args returns the autoconfig flag arguments for the changed fields, one
// `--flag=value` per element, so they can be passed without a shell.
func (m *Model) Args() ([]string, error) {
	var args []string
	for _, f := range m.Fields {
		if !f.Changed() {
			continue
		}
		a, err := f.arg()
		if err != nil {
			return nil, err
		}
		args = append(args, a)
	}
	if m.NoRestart {
		args = append(args, "--no-restart")
	}
	return args, nil
}

func (f *Field) arg() (string, error) {
	v := strings.TrimSpace(f.Value)
	switch f.Type {
	case "bool":
		b, err := strconv.ParseBool(v)
		if err != nil {
			return "", fmt.Errorf("--%s: %q is not a bool", f.Name, f.Value)
		}
		if f.negName != "" {
			if b {
				return "--" + f.Name, nil
			}
			return "--" + f.negName, nil
		}
		return "--" + f.Name + "=" + strconv.FormatBool(b), nil
	case "int":
		i, err := strconv.Atoi(v)
		if err != nil {
			return "", fmt.Errorf("--%s: %q is not an integer", f.Name, f.Value)
		}
		return "--" + f.Name + "=" + strconv.Itoa(i), nil
	}
	return "--" + f.Name + "=" + f.Value, nil
}

// Command renders the equivalent shell command for the changed fields.
func (m *Model) Command() (string, error) {
	args, err := m.Args()
	if err != nil {
		return "", err
	}
	parts := []string{"skywire", "autoconfig"}
	for _, a := range args {
		if name, val, ok := strings.Cut(a, "="); ok {
			a = name + "=" + shellQuote(val)
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " "), nil
}

// shellQuote single-quotes s unless it only holds characters a shell leaves
// alone.
func shellQuote(s string) string {
	plain := s != "" && !strings.ContainsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			strings.ContainsRune("-_.,:/@%+", r))
	})
	if plain {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
