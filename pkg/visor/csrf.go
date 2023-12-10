// Package visor pkg/visor/hypervisor.go
package visor

import (
	"time"

	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/skycoin/skycoin/src/cipher"
)

const (
	// CSRFHeaderName is the name of the CSRF header
	CSRFHeaderName = "X-CSRF-Token"

	// CSRFMaxAge is the lifetime of a CSRF token in seconds
	CSRFMaxAge = time.Second * 30

	csrfSecretLength = 64

	csrfNonceLength = 64
)

var (
	// ErrCSRFInvalid is returned when the the CSRF token is in invalid format
	ErrCSRFInvalid = errors.New("invalid CSRF token")
	// ErrCSRFExpired is returned when the csrf token has expired
	ErrCSRFExpired = errors.New("csrf token expired")
)

var csrfSecretKey []byte

func init() {
	csrfSecretKey = cipher.RandByte(csrfSecretLength)
}

// CSRFToken csrf token
type CSRFToken struct {
	Nonce     []byte
	ExpiresAt time.Time
}

// newCSRFToken generates a new CSRF Token
func newCSRFToken() (string, error) {
	token := &CSRFToken{
		Nonce:     cipher.RandByte(csrfNonceLength),
		ExpiresAt: time.Now().Add(CSRFMaxAge),
	}

	tokenJSON, err := json.Marshal(token)
	if err != nil {
		return "", err
	}

	h := hmac.New(sha256.New, csrfSecretKey)
	_, err = h.Write([]byte(tokenJSON))
	if err != nil {
		return "", err
	}

	sig := base64.RawURLEncoding.EncodeToString(h.Sum(nil))

	signingString := base64.RawURLEncoding.EncodeToString(tokenJSON)

	return strings.Join([]string{signingString, sig}, "."), nil
}

// verifyCSRFToken checks validity of the given token
func verifyCSRFToken(headerToken string) error {
	tokenParts := strings.Split(headerToken, ".")
	if len(tokenParts) != 2 {
		return ErrCSRFInvalid
	}

	signingString, err := base64.RawURLEncoding.DecodeString(tokenParts[0])
	if err != nil {
		return err
	}

	h := hmac.New(sha256.New, csrfSecretKey)
	_, err = h.Write([]byte(signingString))
	if err != nil {
		return err
	}

	sig := base64.RawURLEncoding.EncodeToString(h.Sum(nil))

	if sig != tokenParts[1] {
		return ErrCSRFInvalid
	}

	var csrfToken CSRFToken
	err = json.Unmarshal(signingString, &csrfToken)
	if err != nil {
		return err
	}

	if time.Now().After(csrfToken.ExpiresAt) {
		return ErrCSRFExpired
	}

	return nil
}
