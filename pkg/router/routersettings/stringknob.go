// Package routersettings pkg/router/routersettings/stringknob.go c2-net-routing
//
// The KindString half of the catalog: a knob whose value is TEXT with a
// grammar of its own (mux.shape's "auto | <k>x<n> | n1,n2,…"), refused by the
// knob's own validator rather than by a numeric floor. Its int64 payload is
// unused, so the value lives in its own atomic pointer — the same shape the
// KindList knobs already use — and its per-app overrides in appText, beside
// the int64 appVals.
package routersettings

import (
	"fmt"
	"strings"
)

// appText holds the per-app overrides of the KindString knobs. Guarded by mu,
// exactly like appVals; a string cannot live in appVals' int64 map.
//
//nolint:gochecknoglobals // the catalog IS package state, by design
var appText = map[string]map[string]string{}

// Text reads a KindString knob's live value; an unset knob reads its compiled
// default. Nil-safe. (Not String(), so a *Knob does not become a Stringer and
// start printing its value in place of itself in a log line.)
func (k *Knob) Text() string {
	if k == nil {
		return ""
	}
	if p := k.str.Load(); p != nil {
		return *p
	}
	return k.def.DefaultText
}

// Text reads a KindString knob through a resolved view — the app's override
// when `route settings --app` set one, else the visor-wide value.
func (v *View) Text(k *Knob) string {
	if k == nil {
		return ""
	}
	if v == nil || k.idx >= len(v.texts) || v.texts[k.idx] == "" {
		return k.Text()
	}
	return v.texts[k.idx]
}

// RegisterString adds a KindString knob. validate is the knob's grammar and
// runs on every set, including the compiled default at registration — a knob
// whose own default is invalid is a programming error and panics here.
func RegisterString(name, def, doc string, validate func(string) error) *Knob {
	if validate != nil {
		if err := validate(def); err != nil {
			panic("routersettings: default of " + name + " is invalid: " + err.Error())
		}
	}
	return register(Def{Name: name, Kind: KindString, DefaultText: def, Doc: doc, Validate: validate})
}

// SetText installs a KindString knob's value visor-wide once the knob's own
// validator accepts it. An empty value restores the compiled default. A
// refused value changes nothing.
func SetText(name, raw string) error {
	k := Lookup(name)
	if k == nil {
		return fmt.Errorf("unknown setting %q", name)
	}
	if k.def.Kind != KindString {
		return fmt.Errorf("%s: not a string knob", name)
	}
	v, err := validateText(k, raw)
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	k.str.Store(&v)
	k.set.Store(true)
	bump()
	return nil
}

// setAppText installs a KindString knob's value as app's override.
func setAppText(app, name, raw string) error {
	k := Lookup(name)
	if k == nil {
		return fmt.Errorf("unknown setting %q", name)
	}
	v, err := validateText(k, raw)
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	if appText[app] == nil {
		appText[app] = map[string]string{}
	}
	appText[app][name] = v
	bump()
	return nil
}

// validateText trims raw, reads an empty value as the compiled default, and
// runs the knob's validator over the result.
func validateText(k *Knob, raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		v = k.def.DefaultText
	}
	if k.def.Validate != nil {
		if err := k.def.Validate(v); err != nil {
			return "", fmt.Errorf("%s: %w", k.def.Name, err)
		}
	}
	return v, nil
}
