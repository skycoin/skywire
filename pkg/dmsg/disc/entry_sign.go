// Package disc pkg/dmsg/disc/entry_sign.go
//
// Sign + VerifySignature use the package-wide `json` codec on the Entry to
// produce the canonical signed payload. They build on all targets: the codec is
// jsoniter on native (interface_native.go) and stdlib encoding/json on TinyGo
// (interface_tinygo.go), so a TinyGo dmsg client can sign + register its own
// discovery entry. (Previously //go:build !tinygo, on the now-stale assumption
// that encoding/json couldn't run under TinyGo.)
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
