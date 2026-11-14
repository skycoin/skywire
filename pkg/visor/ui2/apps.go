// Package ui2 pkg/visor/ui2/apps.go c3-vis-core
package ui2

import (
	"sort"
	"strconv"
	"strings"

	"github.com/a-h/templ"

	"github.com/skycoin/skywire/pkg/app/appserver"
)

// App is one row of the apps tab.
type App struct {
	Name         string
	Status       int
	Detail       string
	Port         string
	AutoStart    bool
	OTP          string
	Args         string
	Env          string
	LauncherMode string
}

// Running reports whether the app is up or coming up, as the Angular list
// tells start from stop.
func (a App) Running() bool { return a.Status == 1 || a.Status == 3 }

// Apps is the apps tab: the official apps and the rest, as the Angular tab
// splits them.
type Apps struct {
	PK       string
	Official []App
	Other    []App
	Err      string
	// Open is the app whose settings panel is shown.
	Open string
	// Filtered is set when a filter hid some apps.
	Filtered bool
}

// officialApps follows node-apps-list.component.ts. Multi-instance names such
// as skysocks-client-2 belong to their family.
var officialApps = []string{
	"skychat", "skysocks", "skysocks-client", "vpn-client", "vpn-server",
	"skycoin-daemon", "skycoin-web", "skydex-market", "skydex-client",
}

func isOfficial(name string) bool {
	for _, o := range officialApps {
		if name == o || strings.HasPrefix(name, o+"-") {
			return true
		}
	}
	return false
}

// AppsOf sorts the visor's apps into the two lists, by name.
func AppsOf(pk string, states []*appserver.AppState) Apps {
	a := Apps{PK: pk}
	for _, s := range states {
		if s == nil {
			continue
		}
		row := App{
			Name:         s.Name,
			Status:       int(s.Status),
			Detail:       s.DetailedStatus,
			AutoStart:    s.AutoStart,
			OTP:          s.OTP,
			Args:         ArgsString(s.Args),
			Env:          strings.Join(s.Env, "\n"),
			LauncherMode: s.LauncherMode,
		}
		if s.Port != 0 {
			row.Port = strconv.Itoa(int(s.Port))
		}
		if isOfficial(s.Name) {
			a.Official = append(a.Official, row)
		} else {
			a.Other = append(a.Other, row)
		}
	}
	for _, l := range [][]App{a.Official, a.Other} {
		sort.Slice(l, func(i, j int) bool { return l[i].Name < l[j].Name })
	}
	return a
}

// Find is the app named name.
func (a Apps) Find(name string) (App, bool) {
	for _, l := range [][]App{a.Official, a.Other} {
		for _, x := range l {
			if x.Name == name {
				return x, true
			}
		}
	}
	return App{}, false
}

// ArgsString joins args into one shell-like line, quoting the ones with
// spaces or quotes, as app-settings.component.ts does.
func ArgsString(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t\"'") {
			a = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		}
		out[i] = a
	}
	return strings.Join(out, " ")
}

// EnvLines is the non-blank lines of the environment textarea.
func EnvLines(s string) []string {
	out := []string{}
	for l := range strings.SplitSeq(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func (a Apps) url(rest string) string { return VisorURL(a.PK, "apps") + rest }

// oobAttrs marks an element as an out of band swap when oob is set.
func oobAttrs(oob bool) templ.Attributes {
	if oob {
		return templ.Attributes{"hx-swap-oob": "true"}
	}
	return templ.Attributes{}
}

// AppFilter is the apps list filter, as the Angular filter dialog: name,
// port, state ("running" or "stopped") and autostart ("on" or "off").
type AppFilter struct {
	Name, Port, State, Auto string
}

// AppFilterOf reads the filter from a form.
func AppFilterOf(get func(string) string) AppFilter {
	return AppFilter{Name: strings.TrimSpace(get("f_name")), Port: strings.TrimSpace(get("f_port")), State: get("f_state"), Auto: get("f_auto")}
}

func (f AppFilter) keep(x App) bool {
	switch {
	case f.Name != "" && !strings.Contains(strings.ToLower(x.Name), strings.ToLower(f.Name)):
		return false
	case f.Port != "" && !strings.Contains(x.Port, f.Port):
		return false
	case f.State == "running" && !x.Running():
		return false
	case f.State == "stopped" && x.Running():
		return false
	case f.Auto == "on" && !x.AutoStart:
		return false
	case f.Auto == "off" && x.AutoStart:
		return false
	}
	return true
}

// Filter keeps the apps f matches.
func (a Apps) Filter(f AppFilter) Apps {
	pick := func(l []App) []App {
		var out []App
		for _, x := range l {
			if f.keep(x) {
				out = append(out, x)
			} else {
				a.Filtered = true
			}
		}
		return out
	}
	a.Official, a.Other = pick(a.Official), pick(a.Other)
	return a
}
