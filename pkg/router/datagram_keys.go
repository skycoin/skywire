// Package router pkg/router/datagram_keys.go c2-net-routing
// faithful-UDP per-datagram AEAD master key from a route's Noise
// session. Stage 4b of #2607.
//
// The datagram sibling of a route has no handshake of its own. Both
// peers export a shared secret from the reliable route's finished Noise
// session (noise.Noise.ExportKey, keyed by the transport keys) under
// the label below. NewDatagramCipher then expands that master, per
// direction, into the ChaCha20-Poly1305 subkeys.
package router

import "github.com/skycoin/skywire/pkg/dmsg/noise"

// datagramMasterKeyLabel names the datagram master key export. v1 keyed
// off the public handshake hash, so v2 does not interoperate with it.
const datagramMasterKeyLabel = "skywire-datagram-master-v2"

// sessionKeyExporter is satisfied by the Noise-encrypted conn returned
// by network.EncryptConn (concrete *noise.Conn).
type sessionKeyExporter interface {
	ExportKey(label string) ([32]byte, error)
}

// deriveDatagramMasterKey returns the 32-byte master key consumed by
// NewDatagramCipher. Both peers of a route derive the same key.
func deriveDatagramMasterKey(session sessionKeyExporter) ([32]byte, error) {
	return session.ExportKey(datagramMasterKeyLabel)
}

var _ sessionKeyExporter = (*noise.Conn)(nil)
