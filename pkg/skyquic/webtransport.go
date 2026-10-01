// Package skyquic pkg/skyquic/webtransport.go c1-net-transport
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
	"sync"
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

// webTransportCertRotateEvery is how often a RotatingWebTransportCert makes a
// new certificate: half its validity, so the one being served is never close
// to expiring. A server runs for weeks; a single certificate made at start
// stops being accepted by browsers after webTransportCertValidity.
var webTransportCertRotateEvery = webTransportCertValidity / 2

// RotatingWebTransportCert is a WebTransport server certificate that replaces
// itself before it expires. Its TLSConfig presents whichever certificate is
// current at each handshake, so a rotation reaches new connections at once
// and leaves established ones alone; OnRotate subscribers re-advertise the
// new hash, which is what a browser pins.
type RotatingWebTransportCert struct {
	mu   sync.RWMutex
	cert *tls.Certificate
	hash [32]byte
	subs []func([32]byte)

	done chan struct{}
	once sync.Once
}

// NewRotatingWebTransportCert makes the first certificate and starts rotating.
// Close stops the rotation.
func NewRotatingWebTransportCert() (*RotatingWebTransportCert, error) {
	r := &RotatingWebTransportCert{done: make(chan struct{})}
	if err := r.Rotate(); err != nil {
		return nil, err
	}
	go r.run()
	return r, nil
}

func (r *RotatingWebTransportCert) run() {
	t := time.NewTicker(webTransportCertRotateEvery)
	defer t.Stop()
	for {
		select {
		case <-r.done:
			return
		case <-t.C:
			// A failed rotation keeps the current certificate; the next tick
			// tries again, well before it expires.
			_ = r.Rotate() //nolint:errcheck
		}
	}
}

// Rotate replaces the certificate now and tells every OnRotate subscriber.
func (r *RotatingWebTransportCert) Rotate() error {
	cert, hash, err := NewWebTransportCertificate()
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.cert, r.hash = &cert, hash
	subs := append([]func([32]byte){}, r.subs...)
	r.mu.Unlock()
	for _, fn := range subs {
		fn(hash)
	}
	return nil
}

// Hash is the SHA-256 of the current certificate.
func (r *RotatingWebTransportCert) Hash() [32]byte {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.hash
}

// OnRotate calls fn with the new hash after every rotation.
func (r *RotatingWebTransportCert) OnRotate(fn func([32]byte)) {
	r.mu.Lock()
	r.subs = append(r.subs, fn)
	r.mu.Unlock()
}

// TLSConfig is a server *tls.Config, as WebTransportTLSConfig builds, that
// presents the current certificate at each handshake.
func (r *RotatingWebTransportCert) TLSConfig() *tls.Config {
	return &tls.Config{
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			r.mu.RLock()
			defer r.mu.RUnlock()
			return r.cert, nil
		},
		MinVersion: tls.VersionTLS13,
		NextProtos: []string{WebTransportNextProto},
	}
}

// Close stops the rotation. The current certificate keeps being served.
func (r *RotatingWebTransportCert) Close() {
	r.once.Do(func() { close(r.done) })
}
