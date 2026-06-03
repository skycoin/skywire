// Package buildinfo pkg/cipher/cipher_test.go
package cipher

import (
	"log"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

func TestMain(m *testing.M) {
	loggingLevel, ok := os.LookupEnv("TEST_LOGGING_LEVEL")
	if ok {
		lvl, err := logging.LevelFromString(loggingLevel)
		if err != nil {
			log.Fatal(err)
		}
		logging.SetLevel(lvl)
	} else {
		logging.Disable()
	}

	os.Exit(m.Run())
}

func TestPubKeyString(t *testing.T) {
	p, _ := GenerateKeyPair()
	require.Equal(t, p.Hex(), p.String())
}

func TestPubKeyTextMarshaller(t *testing.T) {
	p, _ := GenerateKeyPair()
	h, err := p.MarshalText()
	require.NoError(t, err)

	var p2 PubKey
	err = p2.UnmarshalText(h)
	require.NoError(t, err)
	require.Equal(t, p, p2)
}

func TestPubKeyBinaryMarshaller(t *testing.T) {
	p, _ := GenerateKeyPair()
	b, err := p.MarshalBinary()
	require.NoError(t, err)

	var p2 PubKey
	err = p2.UnmarshalBinary(b)
	require.NoError(t, err)
	require.Equal(t, p, p2)
}

func TestSecKeyString(t *testing.T) {
	_, s := GenerateKeyPair()
	require.Equal(t, s.Hex(), s.String())
}

func TestSecKeyTextMarshaller(t *testing.T) {
	_, s := GenerateKeyPair()
	h, err := s.MarshalText()
	require.NoError(t, err)

	var s2 SecKey
	err = s2.UnmarshalText(h)
	require.NoError(t, err)
	require.Equal(t, s, s2)
}

func TestSecKeyBinaryMarshaller(t *testing.T) {
	_, s := GenerateKeyPair()
	b, err := s.MarshalBinary()
	require.NoError(t, err)

	var s2 SecKey
	err = s2.UnmarshalBinary(b)
	require.NoError(t, err)
	require.Equal(t, s, s2)
}

func TestSigString(t *testing.T) {
	_, sk := GenerateKeyPair()
	sig, err := SignPayload([]byte("foo"), sk)
	require.NoError(t, err)
	assert.Equal(t, sig.Hex(), sig.String())
}

func TestSigTextMarshaller(t *testing.T) {
	_, sk := GenerateKeyPair()
	sig, err := SignPayload([]byte("foo"), sk)
	require.NoError(t, err)
	h, err := sig.MarshalText()
	require.NoError(t, err)

	var sig2 Sig
	err = sig2.UnmarshalText(h)
	require.NoError(t, err)
	assert.Equal(t, sig, sig2)
}

func TestVerifyPubKeySignedPayload(t *testing.T) {
	pk, sk := GenerateKeyPair()
	payload := []byte("test payload")

	sig, err := SignPayload(payload, sk)
	require.NoError(t, err)

	// Should succeed
	err = VerifyPubKeySignedPayload(pk, sig, payload)
	assert.NoError(t, err)

	// Wrong payload should fail
	err = VerifyPubKeySignedPayload(pk, sig, []byte("wrong"))
	assert.Error(t, err)

	// Wrong pubkey should fail
	wrongPK, _ := GenerateKeyPair()
	err = VerifyPubKeySignedPayload(wrongPK, sig, payload)
	assert.Error(t, err)
}

func BenchmarkVerifyPubKeySignedPayload(b *testing.B) {
	pk, sk := GenerateKeyPair()
	payload := []byte("benchmark payload")
	sig, _ := SignPayload(payload, sk) //nolint

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = VerifyPubKeySignedPayload(pk, sig, payload) //nolint
	}
}

// TestVerifyCacheStats validates the hits / misses / evictions
// counters exposed via GetVerifyCacheStats. First verify is a miss
// + populates the cache; repeated verifies are hits. Filling past
// maxVerifyCacheSize triggers a wipe (Evictions++, Size resets to 1).
func TestVerifyCacheStats(t *testing.T) {
	resetVerifyCache()

	pk, sk := GenerateKeyPair()
	payload := []byte("stats test payload")
	sig, err := SignPayload(payload, sk)
	require.NoError(t, err)

	// First verify — miss + populate.
	require.NoError(t, VerifyPubKeySignedPayload(pk, sig, payload))
	// 5 more — all hits.
	for i := 0; i < 5; i++ {
		require.NoError(t, VerifyPubKeySignedPayload(pk, sig, payload))
	}

	s := GetVerifyCacheStats()
	assert.Equal(t, uint64(5), s.Hits, "expected 5 hits after first verify populated the cache")
	assert.Equal(t, uint64(1), s.Misses, "expected 1 miss on first verify")
	assert.Equal(t, 1, s.Size)
	assert.Equal(t, maxVerifyCacheSize, s.Capacity)
	assert.Equal(t, uint64(0), s.Evictions, "expected no evictions yet")

	// Stuff the cache past capacity to trigger a wipe.
	for i := 0; i <= maxVerifyCacheSize; i++ {
		spk, ssk := GenerateKeyPair()
		ssig, _ := SignPayload(payload, ssk)              //nolint
		_ = VerifyPubKeySignedPayload(spk, ssig, payload) //nolint
	}
	s = GetVerifyCacheStats()
	assert.GreaterOrEqual(t, s.Evictions, uint64(1), "expected at least one eviction (wipe) after overflow")
}

// resetVerifyCache wipes the package-level cache + counters for
// test isolation.
func resetVerifyCache() {
	verifyCacheMu.Lock()
	verifyCacheMap = make(map[[130]byte]struct{}, 1024)
	verifyCacheSize = 0
	verifyCacheMu.Unlock()
	verifyCacheHits.Store(0)
	verifyCacheMisses.Store(0)
	verifyCacheEvictions.Store(0)
}
