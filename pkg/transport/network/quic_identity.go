// Package network pkg/transport/network/quic_identity.go: binds a QUIC
// transport's TLS session to the skywire static identity (the QUIC
// transport, option A). QUIC mandates TLS 1.3, but skywire authenticates
// with secp256k1 keys, not a CA/PKI. So — following the libp2p-tls
// pattern — each side presents a self-signed certificate with an ephemeral
// TLS key, and a custom X.509 extension carries the skywire public key plus
// a signature, by the matching skywire secret key, over the TLS key. The
// peer's VerifyPeerCertificate extracts the extension, checks the signature
// (proving the TLS key — hence the QUIC session — is controlled by the
// holder of the skywire SK), and pins it to the expected remote PK. A
// connection from the wrong PK is rejected at the TLS layer, before any
// route setup.
package network

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// skywireTLSExtensionOID identifies the X.509 extension that binds a QUIC
// TLS certificate to a skywire static public key. Arc under skywire's
// private-enterprise-style OID (53594 is arbitrary-but-fixed for skywire).
var skywireTLSExtensionOID = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 53594, 1, 1}

// quicTLSNextProto is the ALPN protocol id for skywire QUIC transports.
const quicTLSNextProto = "skywire-quic-1"

// skywireSignedKey is the payload carried in the skywire X.509 extension:
// the skywire PK and a signature, by the matching skywire SK, over the
// certificate's ephemeral TLS public key.
type skywireSignedKey struct {
	PubKey []byte // skywire cipher.PubKey (33-byte compressed secp256k1)
	Sig    []byte // cipher.Sig over the cert's ed25519 public key
}

// newQUICCertificate creates a self-signed TLS certificate whose ephemeral
// ed25519 key is bound to the skywire static identity (lPK/lSK).
func newQUICCertificate(lPK cipher.PubKey, lSK cipher.SecKey) (tls.Certificate, error) {
	tlsPub, tlsPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("quic: generate tls key: %w", err)
	}

	// Bind the TLS key to the skywire identity: sign the TLS public key
	// with the skywire SK. A verifier trusting the skywire PK then trusts
	// this TLS key (and the QUIC session it secures).
	sig, err := cipher.SignPayload(tlsPub, lSK)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("quic: sign tls key: %w", err)
	}
	extVal, err := asn1.Marshal(skywireSignedKey{PubKey: lPK[:], Sig: sig[:]})
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("quic: marshal identity extension: %w", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "skywire-quic"},
		// Validity is irrelevant — verification is the identity binding,
		// not a CA chain or expiry (we set InsecureSkipVerify + a custom
		// VerifyPeerCertificate). Use a wide, fixed window so clock skew
		// across visors never rejects a peer.
		NotBefore:             time.Unix(0, 0),
		NotAfter:              time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		ExtraExtensions:       []pkix.Extension{{Id: skywireTLSExtensionOID, Value: extVal}},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, tlsPub, tlsPriv)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("quic: create certificate: %w", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: tlsPriv}, nil
}

// quicTLSConfig builds a *tls.Config that presents the local skywire-bound
// certificate and verifies the peer's certificate binds to a skywire PK.
//
//   - expectedRemotePK != nil (dialer): the peer MUST present that exact PK.
//   - onPeerPK != nil (listener): called with the verified remote PK so the
//     accepting side learns who connected.
func quicTLSConfig(cert tls.Certificate, expectedRemotePK *cipher.PubKey, onPeerPK func(cipher.PubKey)) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		// We bypass the standard chain/hostname/expiry verification and run
		// our own skywire-identity check in VerifyPeerCertificate.
		InsecureSkipVerify:    true, //nolint:gosec // custom PK-binding verification below
		ClientAuth:            tls.RequireAnyClientCert,
		MinVersion:            tls.VersionTLS13,
		NextProtos:            []string{quicTLSNextProto},
		VerifyPeerCertificate: makeVerifyPeerCertificate(expectedRemotePK, onPeerPK),
	}
}

func makeVerifyPeerCertificate(expectedRemotePK *cipher.PubKey, onPeerPK func(cipher.PubKey)) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		pk, err := verifySkywireCert(rawCerts)
		if err != nil {
			return err
		}
		if expectedRemotePK != nil && pk != *expectedRemotePK {
			return fmt.Errorf("quic: peer PK %s does not match expected %s", pk, *expectedRemotePK)
		}
		if onPeerPK != nil {
			onPeerPK(pk)
		}
		return nil
	}
}

// verifySkywireCert parses the leaf certificate, extracts the skywire
// identity extension, verifies the binding signature, and returns the
// authenticated skywire public key.
func verifySkywireCert(rawCerts [][]byte) (cipher.PubKey, error) {
	var zero cipher.PubKey
	if len(rawCerts) == 0 {
		return zero, errors.New("quic: peer presented no certificate")
	}
	cert, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return zero, fmt.Errorf("quic: parse peer certificate: %w", err)
	}

	var extVal []byte
	for i := range cert.Extensions {
		if cert.Extensions[i].Id.Equal(skywireTLSExtensionOID) {
			extVal = cert.Extensions[i].Value
			break
		}
	}
	if extVal == nil {
		return zero, errors.New("quic: peer certificate missing skywire identity extension")
	}

	var sk skywireSignedKey
	if _, err := asn1.Unmarshal(extVal, &sk); err != nil {
		return zero, fmt.Errorf("quic: bad skywire identity extension: %w", err)
	}
	if len(sk.PubKey) != len(cipher.PubKey{}) {
		return zero, fmt.Errorf("quic: bad skywire PK length %d in certificate", len(sk.PubKey))
	}
	if len(sk.Sig) != len(cipher.Sig{}) {
		return zero, fmt.Errorf("quic: bad signature length %d in certificate", len(sk.Sig))
	}
	var pk cipher.PubKey
	copy(pk[:], sk.PubKey)
	var sig cipher.Sig
	copy(sig[:], sk.Sig)

	edPub, ok := cert.PublicKey.(ed25519.PublicKey)
	if !ok {
		return zero, errors.New("quic: certificate public key is not ed25519")
	}
	// The signed payload is the certificate's own ed25519 public key.
	if err := cipher.VerifyPubKeySignedPayload(pk, sig, edPub); err != nil {
		return zero, fmt.Errorf("quic: skywire identity binding invalid: %w", err)
	}
	return pk, nil
}
