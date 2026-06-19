// Package router pkg/router/datagram_keys.go: derivation of the
// faithful-UDP per-datagram AEAD master key from a route's Noise
// session. Stage 4b of #2607.
//
// The datagram path deliberately has no stream-Noise channel of its
// own (per-datagram AEAD replaces the continuous keystream). Rather
// than run a second handshake, the datagram sibling of a route keys
// itself off the reliable route's *already-completed* Noise session:
// both peers take the session's ChannelBinding (the Noise handshake
// hash — identical on both ends, bound to the full transcript incl.
// any PQ-hybrid payload) and HKDF-expand it into a shared 32-byte
// master key. NewDatagramCipher then expands that master, per
// direction, into the actual ChaCha20-Poly1305 subkeys.
//
// Domain separation: this derivation uses info label
// "skywire-datagram-master-v1", distinct from the per-direction
// labels NewDatagramCipher uses ("skywire-datagram-v1-init/-resp"),
// so the master and the direction subkeys can never collide even
// though both HKDF off related material.
package router

import (
	"crypto/sha256"
	"errors"
	"io"

	"golang.org/x/crypto/hkdf"
)

// datagramMasterKeyInfo domain-separates the master-key derivation
// from the per-direction subkey expansion in datagram_crypto.go.
var datagramMasterKeyInfo = []byte("skywire-datagram-master-v1")

// errEmptyChannelBinding is returned when the caller passes a
// zero-length channel binding — a sign the Noise handshake hadn't
// finished, which must never reach key derivation.
var errEmptyChannelBinding = errors.New("datagram-keys: empty channel binding (handshake not finished?)")

// deriveDatagramMasterKey turns a route's Noise session ChannelBinding
// into the 32-byte master key consumed by NewDatagramCipher. Both
// peers of a route derive an identical key because ChannelBinding is
// identical on both ends.
//
// The caller MUST pass a binding from a *finished* handshake
// (noise.Noise.HandshakeFinished() == true); a partial-transcript
// binding would derive a key the peer can't reproduce.
func deriveDatagramMasterKey(channelBinding []byte) ([32]byte, error) {
	var master [32]byte
	if len(channelBinding) == 0 {
		return master, errEmptyChannelBinding
	}
	h := hkdf.New(sha256.New, channelBinding, nil, datagramMasterKeyInfo)
	if _, err := io.ReadFull(h, master[:]); err != nil {
		return master, err
	}
	return master, nil
}
