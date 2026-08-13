// Package cliproxy is the output shape of the `skywire cli proxy` commands.
//
// Most of these mutate something and then say so. A sentence is fine for a
// person and useless to a caller, which had to scrape "added mux leg (2-hop,
// first tp=abc…) on app=skysocks" to learn what happened. The types here carry
// the same facts as fields.
package cliproxy

import (
	"fmt"
	"io"
	"time"
)

// MuxOp is the result of a mux leg operation — add, remove, or a mode change.
//
// One type for the three because a caller asking "what did this do" wants the
// same answer shape each time: what happened, to which app, and to which leg.
// The fields that do not apply are omitted rather than zeroed, so their absence
// is meaningful.
type MuxOp struct {
	// Op is "add", "remove" or "mode".
	Op  string `json:"op"`
	App string `json:"app"`

	// TransportID is the first transport of the leg, for add and remove.
	TransportID string `json:"transport_id,omitempty"`
	// Hops is the leg's length, for add.
	Hops int `json:"hops,omitempty"`
	// Mode is the new setting, for a mode change.
	Mode string `json:"mode,omitempty"`
}

// Human writes the sentence this used to print.
func (m MuxOp) Human(w io.Writer) error {
	switch m.Op {
	case "add":
		_, err := fmt.Fprintf(w, "added mux leg (%d-hop, first tp=%s) on app=%s\n", m.Hops, m.TransportID, m.App)
		return err
	case "remove":
		_, err := fmt.Fprintf(w, "removed mux leg via transport %s on app=%s\n", m.TransportID, m.App)
		return err
	default:
		_, err := fmt.Fprintf(w, "mux mode set to %s\n", m.Mode)
		return err
	}
}

// MuxSet is the result of reconciling an app's legs toward a target count.
type MuxSet struct {
	App    string `json:"app"`
	Target int    `json:"target"`
	// Added and Removed are the transports the run actually changed, so a
	// caller can act on them rather than parse a count out of prose.
	Added    []string `json:"added,omitempty"`
	Removed  []string `json:"removed,omitempty"`
	Existing int      `json:"already_present"`
	// Note carries the trailing remark the human line appends, if any.
	Note string `json:"note,omitempty"`
}

// Human writes the summary line the command printed before.
func (m MuxSet) Human(w io.Writer) error {
	_, err := fmt.Fprintf(w, "mux set app=%s: target=%d, +%d added, -%d removed, %d already present%s\n",
		m.App, m.Target, len(m.Added), len(m.Removed), m.Existing, m.Note)
	return err
}

// MuxAuto is one adaptation pass: what the preset asked for and what the run
// actually did to the session's legs.
//
// DryRun is a field rather than a different shape, because the numbers mean
// the same thing either way — the only difference is whether they happened.
type MuxAuto struct {
	App    string `json:"app"`
	Preset string `json:"preset"`
	Legs   int    `json:"legs"`
	Keep   int    `json:"keep"`
	Pruned int    `json:"pruned"`
	Grown  int    `json:"grown"`
	DryRun bool   `json:"dry_run,omitempty"`
}

// Human writes the timestamped line the command printed. The timestamp is not
// a field: a JSON consumer has its own clock and the line exists to be read as
// it scrolls past.
func (m MuxAuto) Human(w io.Writer) error {
	verb := "pruned"
	if m.DryRun {
		verb = "would prune"
	}
	_, err := fmt.Fprintf(w, "[%s] preset=%s: %d legs, keep<=%d, %s %d, grew %d\n",
		time.Now().Format("15:04:05"), m.Preset, m.Legs, m.Keep, verb, m.Pruned, m.Grown)
	return err
}
