//go:build !tinygo

// Package network pkg/transport/network/quic_identity.go: thin wrappers over
// pkg/skyquic, which holds the skywire-PK-bound QUIC TLS identity (#2607,
// option A). The implementation moved to its own package so dmsg-over-QUIC can
// reuse it without an import cycle (pkg/transport/network imports pkg/dmsg/dmsg).
package network

import (
	"crypto/tls"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skyquic"
)

func newQUICCertificate(lPK cipher.PubKey, lSK cipher.SecKey) (tls.Certificate, error) {
	return skyquic.NewCertificate(lPK, lSK)
}

func quicTLSConfig(cert tls.Certificate, expectedRemotePK *cipher.PubKey, onPeerPK func(cipher.PubKey)) *tls.Config {
	return skyquic.TLSConfig(cert, expectedRemotePK, onPeerPK)
}
