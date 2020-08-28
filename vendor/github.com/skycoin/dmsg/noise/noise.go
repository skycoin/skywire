package noise

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/flynn/noise"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/dmsg/cipher"
)

var noiseLogger = logging.MustGetLogger("noise") // TODO: initialize properly or remove

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
}

// New creates a new Noise with:
//	- provided pattern for handshake.
//	- Secp256k1 for the curve.
func New(pattern noise.HandshakePattern, config Config) (*Noise, error) {
	nc := noise.Config{
		CipherSuite: noise.NewCipherSuite(Secp256k1{}, noise.CipherChaChaPoly, noise.HashSHA256),
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
	}, nil
}

// KKAndSecp256k1 creates a new Noise with:
//	- KK pattern for handshake.
//	- Secp256k1 for the curve.
func KKAndSecp256k1(config Config) (*Noise, error) {
	return New(noise.HandshakeKK, config)
}

// XKAndSecp256k1 creates a new Noise with:
//  - XK pattern for handshake.
//	- Secp256 for the curve.
func XKAndSecp256k1(config Config) (*Noise, error) {
	return New(noise.HandshakeXK, config)
}

// MakeHandshakeMessage generates handshake message for a current handshake state.
func (ns *Noise) MakeHandshakeMessage() (res []byte, err error) {
	if ns.hs.MessageIndex() < len(ns.pattern.Messages)-1 {
		res, _, _, err = ns.hs.WriteMessage(nil, nil)
		return
	}

	res, ns.dec, ns.enc, err = ns.hs.WriteMessage(nil, nil)
	return res, err
}

// ProcessHandshakeMessage processes a received handshake message and appends the payload.
func (ns *Noise) ProcessHandshakeMessage(msg []byte) (err error) {
	if ns.hs.MessageIndex() < len(ns.pattern.Messages)-1 {
		_, _, _, err = ns.hs.ReadMessage(nil, msg)
		return
	}

	_, ns.enc, ns.dec, err = ns.hs.ReadMessage(nil, msg)
	return err
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
func (ns *Noise) RemoteStatic() cipher.PubKey {
	pk, err := cipher.NewPubKey(ns.hs.PeerStatic())
	if err != nil {
		panic(err)
	}
	return pk
}

// EncryptUnsafe encrypts plaintext without interlocking, should only
// be used with external lock.
func (ns *Noise) EncryptUnsafe(plaintext []byte) []byte {
	ns.encNonce++
	buf := make([]byte, nonceSize)
	binary.BigEndian.PutUint64(buf, ns.encNonce)
	return append(buf, ns.enc.Cipher().Encrypt(nil, ns.encNonce, nil, plaintext)...)
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
type NonceMap map[uint64]struct{}

// DecryptWithNonceMap is equivalent to DecryptNonce, instead it uses NonceMap to track nonces instead of a counter.
func (ns *Noise) DecryptWithNonceMap(nm NonceMap, ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < nonceSize {
		return nil, ErrInvalidCipherText
	}
	recvSeq := binary.BigEndian.Uint64(ciphertext[:nonceSize])
	if _, ok := nm[recvSeq]; ok {
		return nil, fmt.Errorf("received decryption nonce (%d) is repeated", recvSeq)
	}
	return ns.dec.Cipher().Decrypt(nil, recvSeq, nil, ciphertext[nonceSize:])
}
