// Package httpauth pkg/httpauth/auth.go
package httpauth

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

// Auth holds authentication mandatory values
type Auth struct {
	Key   cipher.PubKey
	Nonce Nonce
	Sig   cipher.Sig
}

// AuthFromHeaders attempts to extract auth from request header
func AuthFromHeaders(hdr http.Header, shouldVerifyAuth bool) (*Auth, error) {
	a := &Auth{}
	v := hdr.Get("SW-Public")

	if v == "" {
		return nil, errors.New("SW-Public missing")
	}

	key := cipher.PubKey{}
	if err := key.UnmarshalText([]byte(v)); err != nil {
		return nil, fmt.Errorf("error parsing SW-Public: %w", err)
	}

	a.Key = key

	if shouldVerifyAuth {
		if v = hdr.Get("SW-Sig"); v == "" {
			return nil, errors.New("SW-Sig missing")
		}

		sig := cipher.Sig{}
		if err := sig.UnmarshalText([]byte(v)); err != nil {
			return nil, fmt.Errorf("error parsing SW-Sig:'%s': %w", v, err)
		}

		a.Sig = sig
	}

	nonceStr := hdr.Get("SW-Nonce")
	if nonceStr == "" {
		return nil, errors.New("SW-Nonce missing")
	}

	nonceUint, err := strconv.ParseUint(nonceStr, 10, 64)
	if err != nil {
		if numErr, ok := err.(*strconv.NumError); ok {
			return nil, fmt.Errorf("error parsing SW-Nonce: %w", numErr.Err)
		}

		return nil, fmt.Errorf("error parsing SW-Nonce: %w", err)
	}

	a.Nonce = Nonce(nonceUint)

	return a, nil
}

// Verify verifies signature of a payload.
func (a *Auth) Verify(in []byte) error {
	return Verify(in, a.Nonce, a.Key, a.Sig)
}

// verifyAuth verifies Request's signature.
func verifyAuth(store NonceStore, r *http.Request, auth *Auth) error {
	cur, err := store.Nonce(r.Context(), auth.Key)
	if err != nil {
		return err
	}

	if auth.Nonce != cur {
		fmt.Printf("SW-Nonce mismatch, want %q, got %q, key=%q, sig=%q\n",
			cur.String(), auth.Nonce.String(), auth.Key.String(), auth.Sig.String())

		return errors.New("SW-Nonce does not match")
	}

	var buf bytes.Buffer
	body := io.TeeReader(r.Body, &buf)

	payload, err := io.ReadAll(body)
	if err != nil {
		return err
	}

	// close the original body cause it will be replaced
	if err := r.Body.Close(); err != nil {
		return err
	}

	r.Body = io.NopCloser(&buf)

	return auth.Verify(payload)
}

// PayloadWithNonce returns the concatenation of payload and nonce.
func PayloadWithNonce(payload []byte, nonce Nonce) []byte {
	return []byte(fmt.Sprintf("%s%d", string(payload), nonce))
}

// Sign signs the Hash of payload and nonce
func Sign(payload []byte, nonce Nonce, sec cipher.SecKey) (cipher.Sig, error) {
	return cipher.SignPayload(PayloadWithNonce(payload, nonce), sec)
}

// Verify verifies the signature of the hash of payload and nonce
func Verify(payload []byte, nonce Nonce, pub cipher.PubKey, sig cipher.Sig) error {
	return cipher.VerifyPubKeySignedPayload(pub, sig, PayloadWithNonce(payload, nonce))
}
