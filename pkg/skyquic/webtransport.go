// Package skyquic pkg/skyquic/webtransport.go c1-net-transport
package skyquic

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
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

// webTransportCertRotateEvery is how often a RotatingWebTransportCert moves on
// to its next certificate: half the validity, so the one being served is never
// close to expiring, and the next one — made a full period before it is served
// — still has half its validity left when it takes over.
var webTransportCertRotateEvery = webTransportCertValidity / 2

// RotatingWebTransportCert is a WebTransport server certificate that replaces
// itself before it expires, and always has its successor ready.
//
// Both hashes are advertised. A browser pins every hash it is given and
// accepts the server if its certificate matches any of them, so a client that
// learned [current, next] keeps connecting straight through a rotation, with
// no fresh lookup — it only has to learn the new pair within one rotation
// period.
//
// TLSConfig presents whichever certificate is current at each handshake, so a
// rotation reaches new connections at once and leaves established ones alone.
type RotatingWebTransportCert struct {
	mu       sync.RWMutex
	cert     *tls.Certificate
	hash     [32]byte
	next     *tls.Certificate
	nextHash [32]byte
	subs     []func(current, next [32]byte)

	done chan struct{}
	once sync.Once
}

// NewRotatingWebTransportCert makes the first certificate and its successor,
// and starts rotating. Close stops the rotation.
func NewRotatingWebTransportCert() (*RotatingWebTransportCert, error) {
	r := &RotatingWebTransportCert{done: make(chan struct{})}
	// Two rotations: the first fills next, the second promotes it to current
	// and makes a new next.
	for i := 0; i < 2; i++ {
		if err := r.Rotate(); err != nil {
			return nil, err
		}
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

// Rotate serves the next certificate from now on, makes a new next one, and
// tells every OnRotate subscriber both hashes.
func (r *RotatingWebTransportCert) Rotate() error {
	cert, hash, err := NewWebTransportCertificate()
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.cert, r.hash = r.next, r.nextHash
	r.next, r.nextHash = &cert, hash
	cur, next := r.hash, r.nextHash
	subs := append([]func([32]byte, [32]byte){}, r.subs...)
	r.mu.Unlock()
	for _, fn := range subs {
		fn(cur, next)
	}
	return nil
}

// Hash is the SHA-256 of the current certificate.
func (r *RotatingWebTransportCert) Hash() [32]byte {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.hash
}

// NextHash is the SHA-256 of the certificate the next rotation will serve.
func (r *RotatingWebTransportCert) NextHash() [32]byte {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.nextHash
}

// OnRotate calls fn with the current and next hashes after every rotation.
func (r *RotatingWebTransportCert) OnRotate(fn func(current, next [32]byte)) {
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

// PinnedHashes decodes the advertised hex hashes a client pins, skipping
// empty ones (an older server advertises no next hash). A server is accepted
// if its certificate matches any of them.
func PinnedHashes(hexHashes ...string) ([][]byte, error) {
	var out [][]byte
	for _, h := range hexHashes {
		if h == "" {
			continue
		}
		b, err := hex.DecodeString(h)
		if err != nil || len(b) != sha256.Size {
			return nil, fmt.Errorf("skyquic/wt: invalid cert hash %q", h)
		}
		out = append(out, b)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("skyquic/wt: no cert hash to pin")
	}
	return out, nil
}

// MatchesPinned reports whether the DER certificate's SHA-256 is one of pins.
func MatchesPinned(der []byte, pins [][]byte) bool {
	sum := sha256.Sum256(der)
	for _, p := range pins {
		if hmac.Equal(sum[:], p) {
			return true
		}
	}
	return false
}
