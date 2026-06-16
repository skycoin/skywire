// Package noise pkg/noise/noise.go
package noise

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/flynn/noise"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

var noiseLogger = logging.MustGetLogger("noise")

// ErrInvalidCipherText occurs when a ciphertext is received which is too short in size.
var ErrInvalidCipherText = errors.New("noise decrypt unsafe: ciphertext cannot be less than 8 bytes")

// nonceSize is the noise cipher state's nonce size in bytes.
const nonceSize = 8

// Config hold noise parameters.
type Config struct {
	LocalPK   cipher.PubKey // Local instance static public key.
	LocalSK   cipher.SecKey // Local instance static secret key.
	RemotePK  cipher.PubKey // Remote instance static public key.
	Initiator bool          // Whether the local instance initiates the connection.
}

// Noise handles the handshake and the frame's cryptography.
// All operations on Noise are not guaranteed to be thread-safe.
type Noise struct {
	pk   cipher.PubKey
	sk   cipher.SecKey
	init bool

	pattern noise.HandshakePattern
	hs      *noise.HandshakeState
	enc     *noise.CipherState
	dec     *noise.CipherState

	encNonce uint64 // increment after encryption
	decNonce uint64 // expect increment with each subsequent packet

	encBuf [nonceSize]byte // reusable nonce buffer for encryption

	// Post-quantum hybrid (ML-KEM-768). PQ is offered UNCONDITIONALLY and
	// composes with the classical handshake; if the peer doesn't reciprocate
	// (empty payload — an older visor), we transparently fall back to classical.
	// No flag, reverse-compatible by construction. See pqhybrid.go +
	// docs/design/pq-hybrid-noise-handshake.md.
	suite      noise.CipherSuite // retained to rebuild CipherStates for the hybrid re-key
	pqInit     *pqInitiator      // initiator's ephemeral ML-KEM keys (lazily generated)
	pqSendData []byte            // PQ payload queued for the next handshake message
	pqSecret   []byte            // derived ML-KEM shared secret, once available
	pqActive   bool              // true once the hybrid key has been mixed in
}

// PQActive reports whether the session negotiated the post-quantum hybrid (both
// peers exchanged ML-KEM material and the shared secret was mixed into the
// transport keys). False means a classical-only handshake (e.g. an older peer).
func (ns *Noise) PQActive() bool { return ns.pqActive }

// New creates a new Noise with:
//   - provided pattern for handshake.
//   - Secp256k1 for the curve.
func New(pattern noise.HandshakePattern, config Config) (*Noise, error) {
	suite := noise.NewCipherSuite(Secp256k1{}, noise.CipherChaChaPoly, noise.HashSHA256)
	nc := noise.Config{
		CipherSuite: suite,
		Random:      rand.Reader,
		Pattern:     pattern,
		Initiator:   config.Initiator,
		StaticKeypair: noise.DHKey{
			Public:  config.LocalPK[:],
			Private: config.LocalSK[:],
		},
	}
	if !config.RemotePK.Null() {
		nc.PeerStatic = config.RemotePK[:]
	}

	hs, err := noise.NewHandshakeState(nc)
	if err != nil {
		return nil, err
	}
	return &Noise{
		pk:      config.LocalPK,
		sk:      config.LocalSK,
		init:    config.Initiator,
		pattern: pattern,
		hs:      hs,
		suite:   suite,
	}, nil
}

// KKAndSecp256k1 creates a new Noise with:
//   - KK pattern for handshake.
//   - Secp256k1 for the curve.
func KKAndSecp256k1(config Config) (*Noise, error) {
	return New(noise.HandshakeKK, config)
}

// XKAndSecp256k1 creates a new Noise with:
//   - XK pattern for handshake.
//   - Secp256 for the curve.
func XKAndSecp256k1(config Config) (*Noise, error) {
	return New(noise.HandshakeXK, config)
}

// GetEncNonce returns underlying encNonce.
func (ns *Noise) GetEncNonce() uint64 {
	return ns.encNonce
}

// GetDecNonce returns underlying decNonce.
func (ns *Noise) GetDecNonce() uint64 {
	return ns.decNonce
}

// MakeHandshakeMessage generates handshake message for a current handshake state.
// The handshake payload carries the post-quantum offer/response (ML-KEM public
// key from the initiator, ciphertext from the responder); see pqOfferPayload.
func (ns *Noise) MakeHandshakeMessage() (res []byte, err error) {
	payload := ns.pqOfferPayload()
	if ns.hs.MessageIndex() < len(ns.pattern.Messages)-1 {
		res, _, _, err = ns.hs.WriteMessage(nil, payload)
		return
	}

	res, ns.dec, ns.enc, err = ns.hs.WriteMessage(nil, payload)
	if err == nil {
		err = ns.applyHybrid()
	}
	return res, err
}

// ProcessHandshakeMessage processes a received handshake message and appends the payload.
func (ns *Noise) ProcessHandshakeMessage(msg []byte) (err error) {
	if ns.hs.MessageIndex() < len(ns.pattern.Messages)-1 {
		var payload []byte
		payload, _, _, err = ns.hs.ReadMessage(nil, msg)
		if err == nil {
			ns.pqHandlePeerPayload(payload)
		}
		return
	}

	var payload []byte
	payload, ns.enc, ns.dec, err = ns.hs.ReadMessage(nil, msg)
	if err == nil {
		ns.pqHandlePeerPayload(payload)
		err = ns.applyHybrid()
	}
	return err
}

// pqOfferPayload returns the post-quantum data to embed in the next handshake
// message: the initiator's ephemeral ML-KEM public key on its first message, or
// a responder's ML-KEM ciphertext once it has encapsulated. Returns nil when
// there's nothing to offer — which produces an empty payload, the natural "I'm
// not doing PQ / I'm an older visor" signal that yields a classical handshake.
// Best-effort: an ML-KEM keygen failure silently proceeds classical.
func (ns *Noise) pqOfferPayload() []byte {
	if ns.init && ns.pqInit == nil && ns.pqSecret == nil {
		k, err := newPQInitiator()
		if err != nil {
			return nil
		}
		ns.pqInit = k
		return k.PublicKey
	}
	if ns.pqSendData != nil {
		out := ns.pqSendData
		ns.pqSendData = nil
		return out
	}
	return nil
}

// pqHandlePeerPayload reacts to a peer's handshake payload: a responder
// encapsulates against the initiator's ML-KEM public key; an initiator
// decapsulates the responder's ciphertext. A malformed, empty, or unexpected
// payload simply leaves PQ off (classical fallback) — it NEVER fails the
// handshake, which is what makes this reverse-compatible with older visors.
func (ns *Noise) pqHandlePeerPayload(payload []byte) {
	if len(payload) == 0 || ns.pqSecret != nil {
		return
	}
	switch {
	case !ns.init && len(payload) == pqPublicKeySize:
		ct, ss, err := pqEncapsulate(payload)
		if err == nil {
			ns.pqSecret = ss
			ns.pqSendData = ct
		}
	case ns.init && ns.pqInit != nil && len(payload) == pqCiphertextSize:
		if ss, err := ns.pqInit.decapsulate(payload); err == nil {
			ns.pqSecret = ss
		}
	}
}

// applyHybrid mixes the ML-KEM shared secret into the post-Split transport
// CipherStates, binding it to the handshake transcript (ChannelBinding). Both
// ends derive matching per-direction keys (same ss_pq, same transcript, and the
// classical enc-key on one side equals the dec-key on the other). No-op when PQ
// wasn't negotiated. After this, EncryptUnsafe/DecryptUnsafe transparently use
// the hybrid key via the new CipherState (skywire's external nonce is unchanged).
func (ns *Noise) applyHybrid() error {
	if ns.pqSecret == nil || ns.enc == nil || ns.dec == nil {
		return nil
	}
	cb := ns.hs.ChannelBinding()
	encKey := ns.enc.UnsafeKey()
	decKey := ns.dec.UnsafeKey()
	newEnc, err := combineHybridKey(encKey[:], ns.pqSecret, cb, 32)
	if err != nil {
		return err
	}
	newDec, err := combineHybridKey(decKey[:], ns.pqSecret, cb, 32)
	if err != nil {
		return err
	}
	var ek, dk [32]byte
	copy(ek[:], newEnc)
	copy(dk[:], newDec)
	ns.enc = noise.UnsafeNewCipherState(ns.suite, ek, 0)
	ns.dec = noise.UnsafeNewCipherState(ns.suite, dk, 0)
	ns.pqActive = true
	// Best-effort wipe of the shared secret; it's no longer needed.
	for i := range ns.pqSecret {
		ns.pqSecret[i] = 0
	}
	ns.pqSecret = nil
	return nil
}

// HandshakeFinished indicate whether handshake was completed.
func (ns *Noise) HandshakeFinished() bool {
	return ns.hs.MessageIndex() == len(ns.pattern.Messages)
}

// LocalStatic returns the local static public key.
func (ns *Noise) LocalStatic() cipher.PubKey {
	return ns.pk
}

// RemoteStatic returns the remote static public key.
// Returns an empty public key if the peer static key is invalid.
func (ns *Noise) RemoteStatic() cipher.PubKey {
	raw := ns.hs.PeerStatic()
	if len(raw) != len(cipher.PubKey{}) {
		return cipher.PubKey{}
	}
	var pk cipher.PubKey
	copy(pk[:], raw)
	return pk
}

// EncryptUnsafe encrypts plaintext without interlocking, should only
// be used with external lock.
func (ns *Noise) EncryptUnsafe(plaintext []byte) []byte {
	ns.encNonce++
	binary.BigEndian.PutUint64(ns.encBuf[:], ns.encNonce)
	ciphertext := ns.enc.Cipher().Encrypt(nil, ns.encNonce, nil, plaintext)
	out := make([]byte, nonceSize+len(ciphertext))
	copy(out, ns.encBuf[:])
	copy(out[nonceSize:], ciphertext)
	return out
}

// DecryptUnsafe decrypts ciphertext without interlocking, should only
// be used with external lock.
func (ns *Noise) DecryptUnsafe(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < nonceSize {
		return nil, ErrInvalidCipherText
	}
	recvSeq := binary.BigEndian.Uint64(ciphertext[:nonceSize])
	if recvSeq <= ns.decNonce {
		return nil, fmt.Errorf("received decryption nonce (%d) is not larger than previous (%d)", recvSeq, ns.decNonce)
	}
	ns.decNonce = recvSeq
	return ns.dec.Cipher().Decrypt(nil, recvSeq, nil, ciphertext[nonceSize:])
}

// NonceMap is a map of used nonces.
// Deprecated: Use NonceWindow instead for bounded memory usage.
type NonceMap map[uint64]struct{}

// DecryptWithNonceMap is equivalent to DecryptNonce, instead it uses NonceMap to track nonces instead of a counter.
// Deprecated: Use DecryptWithNonceWindow instead.
func (ns *Noise) DecryptWithNonceMap(nm NonceMap, ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < nonceSize {
		return nil, ErrInvalidCipherText
	}
	recvSeq := binary.BigEndian.Uint64(ciphertext[:nonceSize])
	if _, ok := nm[recvSeq]; ok {
		return nil, fmt.Errorf("received decryption nonce (%d) is repeated", recvSeq)
	}
	plaintext, err := ns.dec.Cipher().Decrypt(nil, recvSeq, nil, ciphertext[nonceSize:])
	if err != nil {
		return nil, err
	}
	nm[recvSeq] = struct{}{}
	return plaintext, nil
}

// DecryptWithNonceWindow decrypts ciphertext using a sliding window for nonce tracking.
// Unlike DecryptWithNonceMap, memory usage is bounded to O(NonceWindowSize) regardless
// of how many messages are decrypted over the session's lifetime.
func (ns *Noise) DecryptWithNonceWindow(nw *NonceWindow, ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < nonceSize {
		return nil, ErrInvalidCipherText
	}
	recvSeq := binary.BigEndian.Uint64(ciphertext[:nonceSize])
	if err := nw.Check(recvSeq); err != nil {
		return nil, err
	}
	plaintext, err := ns.dec.Cipher().Decrypt(nil, recvSeq, nil, ciphertext[nonceSize:])
	if err != nil {
		return nil, err
	}
	return plaintext, nil
}
