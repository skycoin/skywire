// Package skymail pkg/skymail/sealed.go c4-app-mail
package skymail

import (
	"bytes"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/mail"
	"strings"

	skycipher "github.com/skycoin/skycoin/src/cipher"
	"golang.org/x/crypto/chacha20poly1305"

	"github.com/skycoin/skywire/pkg/cipher"
)

// A sealed envelope carries one message from its sender to its recipient
// through any visor. Only the recipient can open it and it names the
// sender by signature, so whoever delivers it can neither read it nor
// pass it off as someone else's. The recipient files it under the signer
// and notes the visor that relayed it.
//
// On the wire it is an RFC 5322 message whose base64 body is
//
//	"SKM1" | ephemeral PK (33) | recipient PK (33) | nonce (12) | sealed
//
// sealed is ChaCha20-Poly1305 (RFC 8439) over signer PK (33) | sig (65) |
// message, with the first 70 bytes as additional data. The key is HKDF
// (RFC 5869, SHA-256) of ECDH(ephemeral, recipient), salted with both
// PKs. sig signs sealedDomain | recipient PK | SHA-256(message), so the
// recipient cannot reseal a signed message to someone else as if the
// signer had sent it to them.

// SealedContentType marks a sealed envelope.
const SealedContentType = "application/vnd.skywire.skymail-sealed"

// SealedExtension is the EHLO keyword of a mailbox that opens envelopes.
const SealedExtension = "SKYMAIL-SEALED"

const (
	sealedMagic  = "SKM1"
	sealedDomain = "skymail sealed envelope v1"
	pkLen        = 33
	sigLen       = 65
	sealedHead   = len(sealedMagic) + 2*pkLen + chacha20poly1305.NonceSize
	sealedInner  = pkLen + sigLen
	sealedHeader = "MIME-Version: 1.0\r\nContent-Type: " + SealedContentType + "\r\nContent-Transfer-Encoding: base64\r\n\r\n"
	b64Line      = 76
)

// Errors an envelope can fail to open with.
var (
	ErrNotSealed   = errors.New("skymail: not a sealed envelope")
	ErrNotForUs    = errors.New("skymail: sealed for another visor")
	ErrSealBroken  = errors.New("skymail: sealed envelope does not open")
	ErrBadSealSign = errors.New("skymail: sealed envelope signature does not verify")
)

// SealedSize is the size of the envelope Seal makes for an n byte
// message, for a size limit that has to admit it.
func SealedSize(n int64) int64 {
	raw := int64(sealedHead+sealedInner+chacha20poly1305.Overhead) + n
	enc := (raw + 2) / 3 * 4
	return int64(len(sealedHeader)) + enc + (enc+b64Line-1)/b64Line*2
}

func sealedKey(shared []byte, eph, rcpt cipher.PubKey) ([]byte, error) {
	salt := append(append([]byte{}, eph[:]...), rcpt[:]...)
	return hkdf.Key(sha256.New, shared, salt, sealedDomain, chacha20poly1305.KeySize)
}

func sealedSigned(rcpt cipher.PubKey, msg []byte) []byte {
	h := sha256.Sum256(msg)
	return append(append([]byte(sealedDomain), rcpt[:]...), h[:]...)
}

// Seal signs msg with sk and seals it so only rcpt can open it.
func Seal(msg []byte, sk cipher.SecKey, rcpt cipher.PubKey) ([]byte, error) {
	signer, err := sk.PubKey()
	if err != nil {
		return nil, fmt.Errorf("skymail: seal: %w", err)
	}
	sig, err := cipher.SignPayload(sealedSigned(rcpt, msg), sk)
	if err != nil {
		return nil, fmt.Errorf("skymail: seal: sign: %w", err)
	}
	return sealRaw(signer, sig, msg, rcpt)
}

// sealRaw seals a message and the signature that goes with it.
func sealRaw(signer cipher.PubKey, sig cipher.Sig, msg []byte, rcpt cipher.PubKey) ([]byte, error) {
	ephPK, ephSK := cipher.GenerateKeyPair()
	shared, err := skycipher.ECDH(skycipher.PubKey(rcpt), skycipher.SecKey(ephSK))
	if err != nil {
		return nil, fmt.Errorf("skymail: seal: %w", err)
	}
	key, err := sealedKey(shared, ephPK, rcpt)
	if err != nil {
		return nil, err
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	raw := make([]byte, sealedHead, sealedHead+sealedInner+len(msg)+aead.Overhead())
	copy(raw, sealedMagic)
	copy(raw[len(sealedMagic):], ephPK[:])
	copy(raw[len(sealedMagic)+pkLen:], rcpt[:])
	nonce := raw[sealedHead-chacha20poly1305.NonceSize : sealedHead]
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	plain := make([]byte, 0, sealedInner+len(msg))
	plain = append(append(append(plain, signer[:]...), sig[:]...), msg...)
	raw = aead.Seal(raw, nonce, plain, raw[:sealedHead-chacha20poly1305.NonceSize])

	enc := base64.StdEncoding.EncodeToString(raw)
	var b bytes.Buffer
	b.Grow(int(SealedSize(int64(len(msg)))))
	b.WriteString(sealedHeader)
	for len(enc) > b64Line {
		b.WriteString(enc[:b64Line])
		b.WriteString("\r\n")
		enc = enc[b64Line:]
	}
	b.WriteString(enc)
	b.WriteString("\r\n")
	return b.Bytes(), nil
}

// IsSealed reports whether body is a sealed envelope.
func IsSealed(body []byte) bool {
	m, err := mail.ReadMessage(bytes.NewReader(body))
	if err != nil {
		return false
	}
	ct, _, err := mime.ParseMediaType(m.Header.Get("Content-Type"))
	return err == nil && ct == SealedContentType
}

// OpenSealed opens an envelope sealed to the holder of sk and returns
// the message and the PK that signed it.
func OpenSealed(body []byte, sk cipher.SecKey) ([]byte, cipher.PubKey, error) {
	var none cipher.PubKey
	m, err := mail.ReadMessage(bytes.NewReader(body))
	if err != nil {
		return nil, none, ErrNotSealed
	}
	if ct, _, err := mime.ParseMediaType(m.Header.Get("Content-Type")); err != nil || ct != SealedContentType {
		return nil, none, ErrNotSealed
	}
	b64, err := io.ReadAll(m.Body)
	if err != nil {
		return nil, none, ErrNotSealed
	}
	raw, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(string(b64)), ""))
	if err != nil || len(raw) < sealedHead+sealedInner+chacha20poly1305.Overhead || string(raw[:len(sealedMagic)]) != sealedMagic {
		return nil, none, ErrNotSealed
	}
	var eph, rcpt cipher.PubKey
	copy(eph[:], raw[len(sealedMagic):])
	copy(rcpt[:], raw[len(sealedMagic)+pkLen:])
	own, err := sk.PubKey()
	if err != nil {
		return nil, none, err
	}
	if rcpt != own {
		return nil, none, ErrNotForUs
	}
	shared, err := skycipher.ECDH(skycipher.PubKey(eph), skycipher.SecKey(sk))
	if err != nil {
		return nil, none, ErrSealBroken
	}
	key, err := sealedKey(shared, eph, rcpt)
	if err != nil {
		return nil, none, err
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, none, err
	}
	nonce := raw[sealedHead-chacha20poly1305.NonceSize : sealedHead]
	plain, err := aead.Open(nil, nonce, raw[sealedHead:], raw[:sealedHead-chacha20poly1305.NonceSize])
	if err != nil {
		return nil, none, ErrSealBroken
	}
	var signer cipher.PubKey
	var sig cipher.Sig
	copy(signer[:], plain)
	copy(sig[:], plain[pkLen:])
	msg := plain[sealedInner:]
	if err := cipher.VerifyPubKeySignedPayload(signer, sig, sealedSigned(rcpt, msg)); err != nil {
		return nil, none, ErrBadSealSign
	}
	return msg, signer, nil
}
