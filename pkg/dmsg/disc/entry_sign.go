// Package disc pkg/dmsg/disc/entry_sign.go c1-net-dmsg
package disc

import (
	"github.com/skycoin/skywire/pkg/cipher"
)

// VerifySignature checks if the entry's signature matches its
// PubKey.
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

	entryJSON, err := signedPayload(entry)
	if err != nil {
		return err
	}

	return cipher.VerifyPubKeySignedPayload(e.Static, signature, entryJSON)
}

// Sign signs the Entry with the provided SecKey.
func (e *Entry) Sign(sk cipher.SecKey) error {
	// Clear previous signature, in case there was any.
	e.Signature = ""

	entryJSON, err := signedPayload(e)
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
