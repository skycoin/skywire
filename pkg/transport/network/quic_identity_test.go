// Package network pkg/transport/network/quic_identity_test.go: tests the
// skywire-PK-bound QUIC TLS identity (option A).
package network

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestQUICCertBindsSkywirePK verifies a generated cert carries the skywire
// PK and that verification recovers exactly that PK.
func TestQUICCertBindsSkywirePK(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	cert, err := newQUICCertificate(pk, sk)
	require.NoError(t, err)
	require.Len(t, cert.Certificate, 1)

	got, err := verifySkywireCert(cert.Certificate)
	require.NoError(t, err)
	assert.Equal(t, pk, got, "verification must recover the binding skywire PK")
}

// TestQUICVerifyPeerPinning verifies the dialer-side pinning: the correct
// expected PK passes, a wrong one fails, and the listener-side onPeerPK
// callback learns the verified PK.
func TestQUICVerifyPeerPinning(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()
	cert, err := newQUICCertificate(pk, sk)
	require.NoError(t, err)

	// Dialer expecting the correct PK: passes.
	verifyCorrect := makeVerifyPeerCertificate(&pk, nil)
	require.NoError(t, verifyCorrect(cert.Certificate, nil))

	// Dialer expecting a different PK: rejected.
	verifyWrong := makeVerifyPeerCertificate(&other, nil)
	require.Error(t, verifyWrong(cert.Certificate, nil))

	// Listener (no expected PK) learns who connected.
	var learned cipher.PubKey
	verifyListen := makeVerifyPeerCertificate(nil, func(p cipher.PubKey) { learned = p })
	require.NoError(t, verifyListen(cert.Certificate, nil))
	assert.Equal(t, pk, learned)
}

// TestQUICVerifyRejectsTamperedSignature ensures a cert whose binding
// signature was forged/corrupted fails verification — i.e. you can't
// present someone else's skywire PK without their SK.
func TestQUICVerifyRejectsTamperedSignature(t *testing.T) {
	pkVictim, _ := cipher.GenerateKeyPair()
	_, skAttacker := cipher.GenerateKeyPair()
	// Attacker signs their own TLS key but claims the victim's PK — the
	// binding signature won't verify against the victim's PK.
	cert, err := newQUICCertificate(pkVictim, skAttacker)
	require.NoError(t, err)

	_, err = verifySkywireCert(cert.Certificate)
	require.Error(t, err, "a PK not matching the signing SK must fail verification")
}

// TestQUICVerifyRejectsMissingExtension ensures a vanilla cert without the
// skywire extension is rejected.
func TestQUICVerifyRejectsMissingExtension(t *testing.T) {
	_, err := verifySkywireCert([][]byte{[]byte("not-a-cert")})
	require.Error(t, err)
	_, err = verifySkywireCert(nil)
	require.Error(t, err)
}
