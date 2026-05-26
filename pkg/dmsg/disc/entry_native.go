//go:build !js

// Package disc pkg/dmsg/disc/entry_native.go
//
// Sign + VerifySignature use json.Marshal on the Entry to produce
// the canonical signed payload. encoding/json drags reflect runtime
// helpers TinyGo's stdlib doesn't ship, so the methods are split
// off the WASM build. The install-page WASM never signs or verifies
// discovery entries — those operations are visor-runtime only.
package disc

import (
	"github.com/skycoin/skywire/pkg/cipher"
)

// VerifySignature checks if the entry's signature matches its
// PubKey. Build-tag-gated — see this file's package doc.
func (e *Entry) VerifySignature() error {
	entry := *e

	// Get and parse signature.
	signature := cipher.Sig{}
	err := signature.UnmarshalText([]byte(e.Signature))
	if err != nil {
		return err
	}

	// Set signature field to zero-value before computing the hash.
	entry.Signature = ""

	entryJSON, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	return cipher.VerifyPubKeySignedPayload(e.Static, signature, entryJSON)
}

// Sign signs the Entry with the provided SecKey. Build-tag-gated —
// see this file's package doc.
func (e *Entry) Sign(sk cipher.SecKey) error {
	// Clear previous signature, in case there was any.
	e.Signature = ""

	entryJSON, err := json.Marshal(e)
	if err != nil {
		return err
	}

	sig, err := cipher.SignPayload(entryJSON, sk)
	if err != nil {
		return err
	}
	e.Signature = sig.Hex()
	return nil
}
