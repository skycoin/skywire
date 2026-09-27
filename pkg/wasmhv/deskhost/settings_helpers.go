// Package deskhost pkg/wasmhv/deskhost/settings_helpers.go c4-wasm-desk
//
// The settings app edits the tab visor's skywire.conf through its skyenv
// API (visorapi.SkyenvState), the same calls `skywire cli visor skyenv`
// makes. These are the parts of it that need no DOM: one row per variable,
// and the edits a filled-in form amounts to.
package deskhost

import (
	"fmt"
	"strings"

	"github.com/skycoin/skywire/pkg/skywireconfig/autoconfigcmd"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// settingRow is one skywire.conf variable as the settings window shows it.
// autoconfig often has two flags for one variable (--ishv / --no-ishv);
// the row is the variable, and edits go through one of its flags.
type settingRow struct {
	Key     string // the variable, ISHYPERVISOR
	Flag    string // the flag edits go through, preferably the positive one
	Negate  bool   // Flag is a --no-X flag, so a value is sent inverted
	Format  string // "bool", "int", "string", "bashArray"
	Help    string
	Default string // what config gen uses while the variable is unset
	Note    string

	Set   bool   // the file assigns it
	Value string // the file's value as the form shows it; arrays comma-separated
	// Secret: the visor reports only that it is set. The form keeps the
	// placeholder, and an unchanged placeholder is never sent back.
	Secret bool
	// Desk: the desk passes this on every load, so an edit lasts one boot.
	Desk bool
	// HostOnly: no effect in a browser tab, which cannot listen on a port,
	// open a TUN device or run a separate daemon.
	HostOnly bool
}

// deskOwned are the variables desk-boot.js sets on every load. The hypervisor
// serves the desk's browser; the attach pair is chosen per load.
var deskOwned = map[string]bool{"ISHYPERVISOR": true, "DISABLEPUBLICAUTOCONN": true, "WSPEERS": true}

// hostOnly reports variables that do nothing in a browser tab.
func hostOnly(key string) bool {
	switch key {
	case "BINPATH", "STCPRPORT", "SUDPHPORT", "TRANSPORTPORT", "LANDMSGPORT", "LANDMSGPUBLIC",
		"DMSGSERVER", "DMSGSERVERCONF", "DMSGSERVERPUBLIC", "DMSGSERVERWSTLS",
		"VPNSERVER", "VPNSEVERNETIFC", "VPNSEVERSECURE", "VPNSERVERWL", "SHUTDOWNTIMEOUT":
		return true
	}
	return strings.HasPrefix(key, "VPNROUTER") || strings.HasPrefix(key, "SKYCOIND")
}

// settingRows turns the visor's skyenv state into one row per variable, in
// the order autoconfig registers the flags. The secret key is left out: the
// visor refuses to change it from here.
func settingRows(st visorapi.SkyenvState) []settingRow {
	var rows []settingRow
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

func rowFor(f autoconfigcmd.Flag, vals map[string]string) settingRow {
	r := settingRow{
		Key: f.EnvKey, Flag: f.Name, Negate: f.EnvNegate, Format: f.EnvFormat,
		Help: flagHelp(f.Description), Default: f.EnvDefault, Note: f.EnvNote,
		Desk: deskOwned[f.EnvKey], HostOnly: hostOnly(f.EnvKey),
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
// carries: the window already shows the variable.
func flagHelp(desc string) string {
	if i := strings.Index(desc, " — writes "); i >= 0 {
		return strings.TrimSpace(desc[:i])
	}
	return desc
}

// settingInput is one row of the filled-in form. An empty value outside a
// bool is the same as Unset: a cleared field means "back to the default".
type settingInput struct {
	Unset bool   // back to the default: the line is commented out
	Value string // for a bool, "true" or "false"
}

// settingChange is one line of the preview.
type settingChange struct {
	Key, From, To string
}

// settingsEdits compares the form with the rows it was built from and
// returns the edits for SetSkyenv, with a preview line per change. Rows
// the form did not include, and rows the desk owns, are left alone.
func settingsEdits(rows []settingRow, form map[string]settingInput) (visorapi.SkyenvEdits, []settingChange, error) {
	edits := visorapi.SkyenvEdits{Set: map[string]string{}}
	var changes []settingChange
	for _, r := range rows {
		in, ok := form[r.Key]
		if !ok || r.Desk {
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
			changes = append(changes, settingChange{r.Key, from, defaultShown(r)})
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
			changes = append(changes, settingChange{r.Key, from, to})
		}
	}
	return edits, changes, nil
}

// shown is the row's current value for the preview.
func (r settingRow) shown() string {
	if !r.Set {
		return defaultShown(r)
	}
	return r.Value
}

func defaultShown(r settingRow) string {
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
