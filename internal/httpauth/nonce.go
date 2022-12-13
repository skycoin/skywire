// Package httpauth internal/httpauth/nonce.go
package httpauth

import (
	"fmt"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

// Nonce is used to sign requests in order to avoid replay attack
type Nonce uint64

func (n Nonce) String() string { return fmt.Sprintf("%d", n) }

// PayloadWithNonce returns the concatenation of payload and nonce.
func PayloadWithNonce(payload []byte, nonce Nonce) []byte {
	return []byte(fmt.Sprintf("%s%d", string(payload), nonce))
}

// Sign signs the Hash of payload and nonce
func Sign(payload []byte, nonce Nonce, sec cipher.SecKey) (cipher.Sig, error) {
	return cipher.SignPayload(PayloadWithNonce(payload, nonce), sec)
}
