// Package skyquic pkg/skyquic/identity_test.go: tests the skywire-PK-bound
// QUIC TLS identity (option A).
package skyquic

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestCertBindsSkywirePK verifies a generated cert carries the skywire PK and
// that verification recovers exactly that PK.
func TestCertBindsSkywirePK(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	cert, err := NewCertificate(pk, sk)
	require.NoError(t, err)
	require.Len(t, cert.Certificate, 1)

	got, err := VerifyCert(cert.Certificate)
	require.NoError(t, err)
	assert.Equal(t, pk, got, "verification must recover the binding skywire PK")
}

// TestVerifyPeerPinning verifies dialer-side pinning + the listener-side
// onPeerPK callback.
func TestVerifyPeerPinning(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()
	cert, err := NewCertificate(pk, sk)
	require.NoError(t, err)

	require.NoError(t, makeVerifyPeerCertificate(&pk, nil)(cert.Certificate, nil))
	require.Error(t, makeVerifyPeerCertificate(&other, nil)(cert.Certificate, nil))

	var learned cipher.PubKey
	require.NoError(t, makeVerifyPeerCertificate(nil, func(p cipher.PubKey) { learned = p })(cert.Certificate, nil))
	assert.Equal(t, pk, learned)
}

// TestVerifyRejectsTamperedSignature ensures a cert claiming someone else's PK
// (signed by the wrong SK) fails verification.
func TestVerifyRejectsTamperedSignature(t *testing.T) {
	pkVictim, _ := cipher.GenerateKeyPair()
	_, skAttacker := cipher.GenerateKeyPair()
	cert, err := NewCertificate(pkVictim, skAttacker)
	require.NoError(t, err)

	_, err = VerifyCert(cert.Certificate)
	require.Error(t, err, "a PK not matching the signing SK must fail verification")
}

// TestVerifyRejectsMissingExtension ensures a non-skywire cert is rejected.
func TestVerifyRejectsMissingExtension(t *testing.T) {
	_, err := VerifyCert([][]byte{[]byte("not-a-cert")})
	require.Error(t, err)
	_, err = VerifyCert(nil)
	require.Error(t, err)
}
