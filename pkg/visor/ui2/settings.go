// Package ui2 pkg/visor/ui2/settings.go c3-vis-core
package ui2

import (
	"strings"

	"github.com/skycoin/skywire/pkg/skywireconfig/skyenvform"
)

// Settings is the settings tab: the visor's skywire.conf as a form, and the
// review of what a filled-in form changes.
type Settings struct {
	PK       string
	Path     string
	Exists   bool
	Writable bool
	Fields   []Field
	Err      string

	Reviewing bool
	Changes   []skyenvform.Change
	Command   string
	Saved     bool
}

// Field is one row of the form with what the user typed.
type Field struct {
	skyenvform.Row
	Draft   string
	Problem string
}

// FieldsOf pairs the rows with the drafts, the file's values where the form
// gave none, and the problem each draft has.
func FieldsOf(rows []skyenvform.Row, drafts map[string]string) ([]Field, int) {
	out := make([]Field, len(rows))
	bad := 0
	for i, r := range rows {
		d, ok := drafts[r.Key]
		if !ok {
			d = r.Value
		}
		out[i] = Field{Row: r, Draft: d, Problem: skyenvform.Check(r, d)}
		if out[i].Problem != "" {
			bad++
		}
	}
	return out, bad
}

// FilterText is what the filter box matches a field against.
func (f Field) FilterText() string {
	return strings.ToLower(f.Key + " " + f.Flag + " " + f.Help)
}

// DefaultLabel is the bool select's default option.
func (f Field) DefaultLabel() string {
	switch f.Default {
	case "true":
		return "default (on)"
	case "false":
		return "default (off)"
	}
	return "default"
}

// Placeholder is a text field's hint while it is empty.
func (f Field) Placeholder() string { return skyenvform.DefaultShown(f.Row) }

// InputType hides a secret's value.
func (f Field) InputType() string {
	if f.Secret {
		return "password"
	}
	return "text"
}

func setFlag(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
