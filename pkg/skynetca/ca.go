// Package skynetca implements a locally-installed certificate
// authority used by the skywire resolver to terminate TLS for
// `*.skynet` and `*.dmsg` hostnames in the visitor's browser.
//
// The CA is generated on the visitor's machine, name-constrained
// via the X.509 nameConstraints extension to the resolver TLDs, and
// installed into the OS trust store. Cross-wire authentication is
// already provided by the underlying skywire transport (Noise +
// secp256k1 visor pubkey); the locally-issued leaf cert exists only
// to satisfy the browser's secure-context requirements.
//
// The package exports three primitives:
//
//   - CA lifecycle on disk: GenerateCA / SaveCA / LoadCA / Fingerprint.
//   - LeafMinter: on-demand leaf cert minting and caching, signed by
//     the CA.
//   - MITMTerminate: TLS-termination splice that wraps an upstream
//     net.Conn and returns a net.Conn the resolver's SOCKS5 server
//     can splice against.
//
// pkg/skynetweb and pkg/dmsgweb both consume this package; neither
// imports the other.
package skynetca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// DefaultPermittedDomains is the set of TLDs the resolver CA is
// allowed to issue leaf certs for. The leading dot means "any
// subdomain of"; bare TLDs themselves are not matched.
var DefaultPermittedDomains = []string{".skynet", ".dmsg"}

// CAOptions configures GenerateCA.
type CAOptions struct {
	CommonName       string
	Validity         time.Duration
	PermittedDomains []string
}

func (o CAOptions) withDefaults() CAOptions {
	if o.CommonName == "" {
		o.CommonName = "Skynet Resolver Local CA"
	}
	if o.Validity == 0 {
		o.Validity = 10 * 365 * 24 * time.Hour
	}
	if len(o.PermittedDomains) == 0 {
		o.PermittedDomains = append([]string{}, DefaultPermittedDomains...)
	}
	return o
}

// GenerateCA returns a fresh ECDSA P-256 CA suitable for signing
// leaf certs. Name constraints are applied per opts.PermittedDomains.
func GenerateCA(opts CAOptions) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	o := opts.withDefaults()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate ca key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 159))
	if err != nil {
		return nil, nil, fmt.Errorf("generate serial: %w", err)
	}

	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: o.CommonName},
		NotBefore:             now.Add(-1 * time.Hour),
		NotAfter:              now.Add(o.Validity),
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		// Name constraints are the load-bearing security property:
		// a leaf signed by this CA can only have a dnsName whose
		// labels end in one of these suffixes.
		PermittedDNSDomains:         o.PermittedDomains,
		PermittedDNSDomainsCritical: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("self-sign ca: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, fmt.Errorf("parse ca: %w", err)
	}
	return cert, key, nil
}

// SaveCA writes cert and key to the given paths in PEM format.
// The key file is written 0600; the cert file 0644. The parent
// directory is created with 0700 if missing.
func SaveCA(cert *x509.Certificate, key *ecdsa.PrivateKey, certPath, keyPath string) error {
	if err := os.MkdirAll(filepath.Dir(certPath), 0700); err != nil {
		return fmt.Errorf("mkdir cert dir: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		return fmt.Errorf("write ca.crt: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal ca key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return fmt.Errorf("write ca.key: %w", err)
	}
	return nil
}

// LoadCA reads a CA cert + key pair previously written by SaveCA.
func LoadCA(certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certBytes, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read ca.crt: %w", err)
	}
	block, _ := pem.Decode(certBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, nil, errors.New("ca.crt: no CERTIFICATE PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse ca cert: %w", err)
	}
	if !cert.IsCA {
		return nil, nil, errors.New("ca.crt: not a CA certificate")
	}

	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read ca.key: %w", err)
	}
	keyBlock, _ := pem.Decode(keyBytes)
	if keyBlock == nil || keyBlock.Type != "EC PRIVATE KEY" {
		return nil, nil, errors.New("ca.key: no EC PRIVATE KEY PEM block")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse ca key: %w", err)
	}
	return cert, key, nil
}

// Fingerprint returns the SHA-256 fingerprint of cert in the
// conventional colon-separated lowercase hex format.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	h := hex.EncodeToString(sum[:])
	out := make([]byte, 0, len(h)+len(h)/2-1)
	for i := 0; i < len(h); i += 2 {
		if i > 0 {
			out = append(out, ':')
		}
		out = append(out, h[i], h[i+1])
	}
	return string(out)
}
