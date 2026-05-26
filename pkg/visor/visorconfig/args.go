// Package visorconfig — args.go
//
// Renders the per-app Args slice as a single shell-like string in
// the on-disk config file. The hand-edit experience is the goal:
//
//	"args": "skycoin daemon --port 6420 --data-dir /var/skycoin/sky"
//
// is far easier to read and tweak than the array form. The Go
// internals (launcher, RPC, HTTP API) keep []string — only the
// JSON file representation changes. Old configs with array-form
// args still load.
package visorconfig

import (
	"encoding/json"
	"fmt"
	"strings"

	appspec "github.com/skycoin/skywire/pkg/app/appserver/spec"
	"github.com/skycoin/skywire/pkg/routing"
)

// appsList wraps []appspec.AppConfig so the JSON I/O layer can
// rewrite Args between string and []string without touching the
// shared appserver type. Uses the WASM-clean spec subpackage so
// pkg/visor/visorconfig stays compilable under GOOS=js — the type
// is identical to appspec.AppConfig (alias) so callers that read
// the on-disk config through this package keep working unchanged.
type appsList []appspec.AppConfig

// MarshalJSON emits each AppConfig's Args as a shell-quoted string.
func (a appsList) MarshalJSON() ([]byte, error) {
	out := make([]appConfigOnDisk, len(a))
	for i, c := range a {
		out[i] = toOnDisk(c)
	}
	return json.Marshal(out)
}

// UnmarshalJSON accepts both the new string form and the legacy
// array form on a per-app basis. Mixed configs work too.
func (a *appsList) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	out := make([]appspec.AppConfig, len(raw))
	for i, r := range raw {
		cfg, err := unmarshalAppConfig(r)
		if err != nil {
			return fmt.Errorf("apps[%d]: %w", i, err)
		}
		out[i] = cfg
	}
	*a = out
	return nil
}

// appConfigOnDisk mirrors appspec.AppConfig with Args as a string.
// Only used at the JSON boundary.
type appConfigOnDisk struct {
	Name         string       `json:"name"`
	Binary       string       `json:"binary,omitempty"`
	Args         string       `json:"args,omitempty"`
	AutoStart    bool         `json:"auto_start"`
	Port         routing.Port `json:"port"`
	User         string       `json:"user,omitempty"`
	Group        string       `json:"group,omitempty"`
	WorkDir      string       `json:"work_dir,omitempty"`
	Env          []string     `json:"env,omitempty"`
	LauncherMode string       `json:"launcher_mode,omitempty"`
}

func toOnDisk(c appspec.AppConfig) appConfigOnDisk {
	return appConfigOnDisk{
		Name:         c.Name,
		Binary:       c.Binary,
		Args:         joinArgs(c.Args),
		AutoStart:    c.AutoStart,
		Port:         c.Port,
		User:         c.User,
		Group:        c.Group,
		WorkDir:      c.WorkDir,
		Env:          c.Env,
		LauncherMode: c.LauncherMode,
	}
}

func unmarshalAppConfig(raw json.RawMessage) (appspec.AppConfig, error) {
	// Shadow AppConfig.Args with a RawMessage at the outer scope so
	// we can dispatch on its JSON shape (string vs array).
	var pre struct {
		appspec.AppConfig
		Args json.RawMessage `json:"args,omitempty"`
	}
	if err := json.Unmarshal(raw, &pre); err != nil {
		return appspec.AppConfig{}, err
	}
	cfg := pre.AppConfig
	if len(pre.Args) == 0 {
		cfg.Args = nil
		return cfg, nil
	}
	// Prefer the new string form; fall back to the array form for
	// existing on-disk configs.
	var s string
	if err := json.Unmarshal(pre.Args, &s); err == nil {
		parsed, perr := splitArgs(s)
		if perr != nil {
			return appspec.AppConfig{}, fmt.Errorf("parsing args string: %w", perr)
		}
		cfg.Args = parsed
		return cfg, nil
	}
	if err := json.Unmarshal(pre.Args, &cfg.Args); err != nil {
		return appspec.AppConfig{}, fmt.Errorf("args must be string or array: %w", err)
	}
	return cfg, nil
}

// SplitArgs is the exported counterpart to splitArgs — same parser,
// available to callers outside the package (e.g. config-gen wants
// to tokenize SKYCOIND_FLAGS using the same rules the on-disk
// config uses).
func SplitArgs(s string) ([]string, error) {
	return splitArgs(s)
}

// splitArgs parses a shell-like argument string into a token slice.
// Handles whitespace separators plus "double" and 'single' quoted
// tokens; backslash escapes only inside double quotes. Doesn't do
// variable expansion or command substitution — those are footguns
// we don't want in the config file anyway. The grammar is the
// minimum needed to round-trip joinArgs output back to the
// original slice.
func splitArgs(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inDouble, inSingle := false, false
	inToken := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inDouble:
			if c == '\\' && i+1 < len(s) {
				cur.WriteByte(s[i+1])
				i++
				continue
			}
			if c == '"' {
				inDouble = false
				continue
			}
			cur.WriteByte(c)
		case inSingle:
			if c == '\'' {
				inSingle = false
				continue
			}
			cur.WriteByte(c)
		default:
			switch c {
			case ' ', '\t', '\n':
				if inToken {
					out = append(out, cur.String())
					cur.Reset()
					inToken = false
				}
			case '"':
				inDouble = true
				inToken = true
			case '\'':
				inSingle = true
				inToken = true
			default:
				cur.WriteByte(c)
				inToken = true
			}
		}
	}
	if inDouble || inSingle {
		return nil, fmt.Errorf("unclosed quote in args")
	}
	if inToken {
		out = append(out, cur.String())
	}
	return out, nil
}

// joinArgs renders tokens as a shell-like string. Tokens containing
// whitespace or quotes are double-quoted with internal " and \
// escaped.
func joinArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, len(args))
	for i, a := range args {
		switch {
		case a == "":
			parts[i] = `""`
		case strings.ContainsAny(a, " \t\n\"'\\"):
			esc := strings.ReplaceAll(a, `\`, `\\`)
			esc = strings.ReplaceAll(esc, `"`, `\"`)
			parts[i] = `"` + esc + `"`
		default:
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}
