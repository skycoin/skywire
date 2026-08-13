// Package clisvc is the output shape of the `skywire cli svc` and
// `skywire cli resolver` commands.
package clisvc

import (
	"fmt"
	"io"
)

// ARRegistration answers whether a public key is registered with the address
// resolver, and for which transport types.
//
// Registered is explicit rather than implied by an empty Types, because "not
// registered" and "registered for nothing" are different answers and a caller
// checking `.types | length` could not tell them apart.
type ARRegistration struct {
	PK         string   `json:"pk"`
	Registered bool     `json:"registered"`
	Types      []string `json:"types,omitempty"`
}

// Human writes the sentence the command printed.
func (a ARRegistration) Human(w io.Writer) error {
	if !a.Registered {
		_, err := fmt.Fprintf(w, "%s: not registered in address resolver\n", a.PK)
		return err
	}
	_, err := fmt.Fprintf(w, "%s: registered for %v\n", a.PK, a.Types)
	return err
}

// CA is the certificate authority `resolver ca` generated.
type CA struct {
	Cert        string `json:"cert"`
	Key         string `json:"key"`
	Fingerprint string `json:"fingerprint"`
}

// Human writes the block the command printed, including the follow-up hint —
// which is guidance for a person and has no place in the JSON.
func (c CA) Human(w io.Writer) error {
	_, err := fmt.Fprintf(w,
		"CA generated.\n  cert:        %s\n  key:         %s\n  fingerprint: %s\n\nNext: skywire cli resolver ca install\n",
		c.Cert, c.Key, c.Fingerprint)
	return err
}
