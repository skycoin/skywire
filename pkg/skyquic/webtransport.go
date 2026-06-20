package skyquic

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"time"
)

// webTransportCertValidity is how long a WebTransport server certificate is
// valid. The browser WebTransport API only accepts a serverCertificateHashes
// (hash-pinned, CA-free) certificate when its validity is at most 14 days —
// Chrome enforces this to bound the damage of a pinned self-signed cert. We use
// 13 days and rotate before expiry.
const webTransportCertValidity = 13 * 24 * time.Hour

// WebTransportNextProto is the ALPN for WebTransport over HTTP/3.
const WebTransportNextProto = "h3"

// NewWebTransportCertificate returns a fresh self-signed ECDSA P-256 certificate
// suitable for the browser WebTransport serverCertificateHashes path, plus its
// SHA-256 fingerprint (the value to publish in discovery and pin in the browser
// constructor). Unlike the QUIC identity cert (skyquic.NewCertificate), this
// cert carries NO skywire-PK binding extension: WebTransport's trust comes from
// the browser pinning this exact cert hash, and the binding "server PK X has WT
// cert hash H" lives in X's signed discovery entry. The client→server skywire-PK
// authentication happens in the dmsg Noise handshake over the WT stream (a
// browser cannot present a client certificate, so it can't be done in TLS).
//
// The cert must be ECDSA P-256 with <=14-day validity for the browser to accept
// it via serverCertificateHashes.
func NewWebTransportCertificate() (tls.Certificate, [32]byte, error) {
	var zeroHash [32]byte
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, zeroHash, fmt.Errorf("skyquic/wt: generate ecdsa key: %w", err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "skywire-webtransport"},
		NotBefore:             now.Add(-1 * time.Hour),
		NotAfter:              now.Add(webTransportCertValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, zeroHash, fmt.Errorf("skyquic/wt: create certificate: %w", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}, sha256.Sum256(der), nil
}

// WebTransportTLSConfig builds a server *tls.Config for the WebTransport (HTTP/3)
// listener presenting the given cert. No client certificate is requested — the
// browser cannot present one; client PK auth is done in the dmsg Noise handshake.
func WebTransportTLSConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		NextProtos:   []string{WebTransportNextProto},
	}
}
