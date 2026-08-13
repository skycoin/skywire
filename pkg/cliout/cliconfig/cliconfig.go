// Package cliconfig is the output shape of the `skywire cli config` commands.
package cliconfig

import (
	"fmt"
	"io"
)

// Keypair is a generated or derived key pair.
//
// SecretKey is omitempty because `pubkey` derives only the public half, and a
// caller must be able to tell "no secret here" from an empty string it might
// otherwise write somewhere.
type Keypair struct {
	PublicKey string `json:"public_key"`
	SecretKey string `json:"secret_key,omitempty"`
}

// Human writes the keys on their own lines, as before — public first, which is
// the order every script that parses this already expects.
func (k Keypair) Human(w io.Writer) error {
	if k.SecretKey == "" {
		_, err := fmt.Fprintln(w, k.PublicKey)
		return err
	}
	_, err := fmt.Fprintf(w, "%s\n%s\n", k.PublicKey, k.SecretKey)
	return err
}
