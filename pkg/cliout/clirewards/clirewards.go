// Package clirewards is the output shape of the `skywire cli rewards`
// commands, which collect the data the reward system is computed from.
package clirewards

import (
	"fmt"
	"io"
)

// QualifyingVisors lists the public keys that met the transport threshold,
// and — unless only listing — what was written to the history file.
type QualifyingVisors struct {
	MinTransports int      `json:"min_transports"`
	PubKeys       []string `json:"public_keys"`
	// Added and Path describe the history file update. Absent when the
	// command was only asked to list (--all), which writes nothing.
	Added int    `json:"added,omitempty"`
	Path  string `json:"path,omitempty"`
}

// Human writes either the bare key list or the completion line, matching what
// the command printed for each mode.
func (q QualifyingVisors) Human(w io.Writer) error {
	if q.Path == "" {
		if _, err := fmt.Fprintln(w, "Public Keys with sufficient transports:"); err != nil {
			return err
		}
		for _, pk := range q.PubKeys {
			if _, err := fmt.Fprintln(w, pk); err != nil {
				return err
			}
		}
		return nil
	}
	_, err := fmt.Fprintf(w, "Transport collection complete: %d qualifying visors, %d new entries added to %s\n",
		len(q.PubKeys), q.Added, q.Path)
	return err
}
