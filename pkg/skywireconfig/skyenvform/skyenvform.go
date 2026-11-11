// Package skyenvform pkg/skywireconfig/skyenvform/skyenvform.go c3-vis-core
//
// Package skyenvform is the form side of a skywire.conf editor that works
// through the visor's skyenv API (visorapi.SkyenvState), the same calls
// `skywire cli visor skyenv` makes: one row per variable, and the edits a
// filled-in form amounts to. The desk's settings window and the hypervisor
// UI's settings tab both use it.
package skyenvform

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/skycoin/skywire/pkg/skywireconfig/autoconfigcmd"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// Row is one skywire.conf variable as a settings form shows it.
// autoconfig often has two flags for one variable (--ishv / --no-ishv);
// the row is the variable, and edits go through one of its flags.
type Row struct {
	Key     string // the variable, ISHYPERVISOR
	Flag    string // the flag edits go through, preferably the positive one
	Negate  bool   // Flag is a --no-X flag, so a value is sent inverted
	Format  string // "bool", "int", "string", "bashArray"
	Help    string
	Default string // what config gen uses while the variable is unset
	Note    string
	Check   string // the rule the visor applies on save, from the flag's check

	Set   bool   // the file assigns it
	Value string // the file's value as the form shows it; arrays comma-separated
	// Secret: the visor reports only that it is set. The form keeps the
	// placeholder, and an unchanged placeholder is never sent back.
	Secret bool
	// Locked: the caller owns this variable, so Edits leaves it alone.
	Locked bool
}

// Rows turns the visor's skyenv state into one row per variable, in the
// order autoconfig registers the flags. The secret key is left out: the
// visor refuses to change it from here.
func Rows(st visorapi.SkyenvState) []Row {
	var rows []Row
	at := map[string]int{}
	for _, f := range st.Flags {
		if f.EnvKey == "" || f.EnvKey == "SK" {
			continue
		}
		i, seen := at[f.EnvKey]
		if !seen {
			at[f.EnvKey] = len(rows)
			rows = append(rows, rowFor(f, st.Values))
			continue
		}
		// A positive flag takes over from a --no-X flag seen first.
		if rows[i].Negate && !f.EnvNegate {
			rows[i] = rowFor(f, st.Values)
		}
	}
	return rows
}

func rowFor(f autoconfigcmd.Flag, vals map[string]string) Row {
	r := Row{
		Key: f.EnvKey, Flag: f.Name, Negate: f.EnvNegate, Format: f.EnvFormat,
		Help: flagHelp(f.Description), Default: f.EnvDefault, Note: f.EnvNote, Check: f.Check,
	}
	if v, ok := vals[f.EnvKey]; ok {
		r.Set = true
		r.Secret = v == visorapi.SkyenvRedacted
		r.Value = v
		if r.Format == "bashArray" {
			r.Value = strings.Join(strings.Fields(v), ",")
		}
	}
	return r
}

// flagHelp drops the "— writes X in skywire.conf" tail every description
// carries: the form already shows the variable.
func flagHelp(desc string) string {
	if before, _, ok := strings.Cut(desc, " — writes "); ok {
		return strings.TrimSpace(before)
	}
	return desc
}

// Input is one row of the filled-in form. An empty value outside a bool is
// the same as Unset: a cleared field means "back to the default".
type Input struct {
	Unset bool   // back to the default: the line is commented out
	Value string // for a bool, "true" or "false"
}

// Change is one line of the preview.
type Change struct {
	Key, From, To string
}

// Edits compares the form with the rows it was built from and returns the
// edits for SetSkyenv, with a preview line per change. Rows the form did not
// include, and locked rows, are left alone.
func Edits(rows []Row, form map[string]Input) (visorapi.SkyenvEdits, []Change, error) {
	edits := visorapi.SkyenvEdits{Set: map[string]string{}}
	var changes []Change
	for _, r := range rows {
		in, ok := form[r.Key]
		if !ok || r.Locked {
			continue
		}
		from := r.shown()
		if r.Format != "bool" && strings.TrimSpace(in.Value) == "" {
			in.Unset = true
		}
		switch {
		case in.Unset:
			// A variable set to nothing (WSPEERS=('')) shows as an empty
			// field; leaving it empty is not a change.
			if !r.Set || r.Value == "" {
				continue
			}
			edits.Unset = append(edits.Unset, r.Flag)
			changes = append(changes, Change{r.Key, from, DefaultShown(r)})
		default:
			v := strings.TrimSpace(in.Value)
			if r.Format == "bashArray" {
				v = normalizeList(v)
			}
			if r.Set && v == r.Value || r.Secret && v == visorapi.SkyenvRedacted {
				continue
			}
			send := v
			if r.Format == "bool" {
				b, err := parseBool(v)
				if err != nil {
					return visorapi.SkyenvEdits{}, nil, fmt.Errorf("%s: %w", r.Key, err)
				}
				if r.Set && b == (r.Value == "true") {
					continue
				}
				v = fmt.Sprint(b)
				send = fmt.Sprint(b != r.Negate)
			}
			edits.Set[r.Flag] = send
			to := v
			if r.Secret || visorapi.SkyenvSecret(r.Key) {
				to = "(changed)"
			}
			changes = append(changes, Change{r.Key, from, to})
		}
	}
	return edits, changes, nil
}

// shown is the row's current value for the preview.
func (r Row) shown() string {
	if !r.Set {
		return DefaultShown(r)
	}
	return r.Value
}

// DefaultShown is how a form names the default of a row.
func DefaultShown(r Row) string {
	if r.Default == "" {
		return "(default)"
	}
	return "(default " + r.Default + ")"
}

func normalizeList(v string) string {
	parts := strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })
	return strings.Join(parts, ",")
}

func parseBool(v string) (bool, error) {
	switch strings.ToLower(v) {
	case "true", "on", "yes", "1":
		return true, nil
	case "false", "off", "no", "0":
		return false, nil
	}
	return false, fmt.Errorf("%q is not on or off", v)
}

// Check is what is wrong with a form value for a row, or "" when the visor
// would accept it. The visor makes the same checks on save.
func Check(r Row, value string) string {
	v := strings.TrimSpace(value)
	if v == "" || r.Secret && v == visorapi.SkyenvRedacted {
		return ""
	}
	if r.Format == "int" {
		if _, err := strconv.Atoi(v); err != nil {
			return fmt.Sprintf("%q is not a whole number", v)
		}
	}
	if r.Format == "bool" {
		if _, err := parseBool(v); err != nil {
			return err.Error()
		}
	}
	if err := autoconfigcmd.CheckValue(r.Check, v); err != nil {
		return err.Error()
	}
	return ""
}

// Command is the `sudo skywire autoconfig` line that makes the same edits, for
// a visor that cannot write its own conf file. autoconfig has no unset, so an
// unset sets the default instead. It also regenerates skywire.json and
// restarts the visor, which applies the change.
func Command(rows []Row, edits visorapi.SkyenvEdits) string {
	byFlag := map[string]Row{}
	for _, r := range rows {
		byFlag[r.Flag] = r
	}
	flags := make([]string, 0, len(edits.Set))
	for f := range edits.Set {
		flags = append(flags, f)
	}
	sort.Strings(flags)
	args := []string{"sudo", "skywire", "autoconfig"}
	for _, f := range flags {
		args = append(args, shellWord("--"+f+"="+edits.Set[f]))
	}
	for _, f := range edits.Unset {
		r, ok := byFlag[f]
		v := ""
		if ok {
			v = r.Default
			if r.Format == "bool" && r.Negate {
				v = strconv.FormatBool(v != "true")
			}
		}
		args = append(args, shellWord("--"+f+"="+v))
	}
	return strings.Join(args, " ")
}

var plainWord = regexp.MustCompile(`^[A-Za-z0-9_.,:/@=+-]+$`)

// shellWord single-quotes s unless it needs no quoting.
func shellWord(s string) string {
	if plainWord.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
