// Package clitps is the output shape of the `skywire cli tps` commands, which
// manage the transport-setup-node key.
package clitps

import (
	"fmt"
	"io"
)

// Status reports whether transport setup is enabled and under which key.
type Status struct {
	Enabled bool   `json:"enabled"`
	PubKey  string `json:"public_key,omitempty"`
}

// Human writes the lines the command printed, including the hint that only
// makes sense to a person and so has no place in the JSON.
func (s Status) Human(w io.Writer) error {
	if !s.Enabled {
		_, err := fmt.Fprint(w, "TPS Status: disabled\nTo enable, set 'tps_sk' in the visor config\n")
		return err
	}
	_, err := fmt.Fprintf(w, "TPS Status: enabled\nTPS Public Key: %s\n", s.PubKey)
	return err
}
