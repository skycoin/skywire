// Package releasesig pkg/releasesig/releasesig_test.go c4-vis-cli
package releasesig

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSignVerify(t *testing.T) {
	key, err := KeyGenerate("test")
	require.NoError(t, err)
	message := []byte("abc  skywire-linux-amd64.zst\n")
	signature, err := key.Sign(message)
	require.NoError(t, err)

	name, err := Verify(message, signature, []*PublicKey{key.Public()})
	require.NoError(t, err)
	require.Equal(t, "test", name)

	_, err = Verify([]byte("abd  skywire-linux-amd64.zst\n"), signature, []*PublicKey{key.Public()})
	require.ErrorIs(t, err, ErrBadSignature)

	other, err := KeyGenerate("other")
	require.NoError(t, err)
	_, err = Verify(message, signature, []*PublicKey{other.Public()})
	require.ErrorIs(t, err, ErrNoTrustedKey)
}

// TestVerifyNeedsBothHalves swaps in one valid half from another key of the
// same name: the hybrid must still fail.
func TestVerifyNeedsBothHalves(t *testing.T) {
	key, err := KeyGenerate("k")
	require.NoError(t, err)
	impostor, err := KeyGenerate("k")
	require.NoError(t, err)
	message := []byte("payload")

	for _, swapEd := range []bool{true, false} {
		real, err := key.Sign(message)
		require.NoError(t, err)
		fake, err := impostor.Sign(message)
		require.NoError(t, err)
		_, realEd, realML, err := decodeLines(real, signatureHeader)
		require.NoError(t, err)
		_, fakeEd, fakeML, err := decodeLines(fake, signatureHeader)
		require.NoError(t, err)
		mixed := encodeLines(signatureHeader, "k", realEd, fakeML)
		if swapEd {
			mixed = encodeLines(signatureHeader, "k", fakeEd, realML)
		}
		_, err = Verify(message, mixed, []*PublicKey{key.Public()})
		require.ErrorIs(t, err, ErrBadSignature)
	}
}

func TestKeyTextRoundTrip(t *testing.T) {
	key, err := KeyGenerate("round")
	require.NoError(t, err)
	privateText, err := key.MarshalText()
	require.NoError(t, err)
	parsed, err := PrivateKeyParse(privateText)
	require.NoError(t, err)
	publicText, err := key.Public().MarshalText()
	require.NoError(t, err)
	public, err := PublicKeyParse(publicText)
	require.NoError(t, err)

	signature, err := parsed.Sign([]byte("x"))
	require.NoError(t, err)
	name, err := Verify([]byte("x"), signature, []*PublicKey{public})
	require.NoError(t, err)
	require.Equal(t, "round", name)

	_, err = PublicKeyParse(privateText)
	require.Error(t, err)
	_, err = KeyGenerate("two words")
	require.Error(t, err)
}

func TestTrustedKeysParse(t *testing.T) {
	keys, err := TrustedKeys()
	require.NoError(t, err)
	require.NotEmpty(t, keys)
}
