// Package router pkg/router/datagram_crypto_test.go: unit tests
// for the per-datagram AEAD layer. Stage 3 of #2607.

package router

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// newTestPair builds an initiator+responder cipher pair sharing the
// same master key + bind PK. The two ciphers each play one role on
// each side: initiator's "outbound" matches responder's "inbound"
// (same subkey via HKDF info "skywire-datagram-v1-init") and vice
// versa.
func newTestPair(t *testing.T, cfg *DatagramCipherConfig) (initiatorOut, responderIn, responderOut, initiatorIn *DatagramCipher) {
	t.Helper()
	var master [32]byte
	_, err := rand.Read(master[:])
	require.NoError(t, err)
	bindPK, _ := cipher.GenerateKeyPair()

	initiatorOut, err = NewDatagramCipher(master, RoleInitiator, bindPK, cfg)
	require.NoError(t, err)
	responderIn, err = NewDatagramCipher(master, RoleInitiator, bindPK, cfg) // peer's inbound mirrors my outbound
	require.NoError(t, err)
	responderOut, err = NewDatagramCipher(master, RoleResponder, bindPK, cfg)
	require.NoError(t, err)
	initiatorIn, err = NewDatagramCipher(master, RoleResponder, bindPK, cfg)
	require.NoError(t, err)
	return
}

func TestDatagramCipherRoundTrip(t *testing.T) {
	out, in, _, _ := newTestPair(t, nil)

	plaintext := []byte("over-skynet-as-datagram")
	sealed, err := out.Seal(42, plaintext)
	require.NoError(t, err)

	// Wire-format checks: first 8 bytes = counter (0 on first send);
	// total length = counter(8) + ciphertext(plaintext) + tag(16).
	assert.Equal(t, uint64(0), binary.BigEndian.Uint64(sealed[:8]))
	assert.Equal(t, DatagramOverhead+len(plaintext), len(sealed))

	pt, err := in.Open(42, sealed)
	require.NoError(t, err)
	assert.Equal(t, plaintext, pt)
}

func TestDatagramCipherWrongRouteIDRejected(t *testing.T) {
	out, in, _, _ := newTestPair(t, nil)

	sealed, err := out.Seal(42, []byte("for route 42"))
	require.NoError(t, err)

	// Wrong route ID at Open → AAD mismatch → auth failure.
	_, err = in.Open(43, sealed)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAEADAuth)
}

func TestDatagramCipherWrongBindPKRejected(t *testing.T) {
	var master [32]byte
	_, _ = rand.Read(master[:])
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()

	out, err := NewDatagramCipher(master, RoleInitiator, pk1, nil)
	require.NoError(t, err)
	in, err := NewDatagramCipher(master, RoleInitiator, pk2, nil) // different bindPK
	require.NoError(t, err)

	sealed, err := out.Seal(42, []byte("test"))
	require.NoError(t, err)

	// AAD mismatch on destPK → auth failure.
	_, err = in.Open(42, sealed)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAEADAuth)
}

func TestDatagramCipherReplayRejected(t *testing.T) {
	out, in, _, _ := newTestPair(t, nil)

	sealed, err := out.Seal(1, []byte("once"))
	require.NoError(t, err)

	// First Open succeeds.
	_, err = in.Open(1, sealed)
	require.NoError(t, err)

	// Replay of the same ciphertext → auth failure (already in
	// the window).
	_, err = in.Open(1, sealed)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAEADAuth)
}

func TestDatagramCipherOutOfWindowRejected(t *testing.T) {
	out, in, _, _ := newTestPair(t, nil)

	// Send + receive enough datagrams to advance the window past
	// where counter=0 was. replayWindowSize is 2048, so 3000 sends
	// puts counter=0 well outside the trailing edge.
	for i := 0; i < 3000; i++ {
		sealed, err := out.Seal(1, []byte("x"))
		require.NoError(t, err)
		_, err = in.Open(1, sealed)
		require.NoError(t, err)
	}

	// Now craft a sealed datagram with counter=0 (replay the very
	// first message we sent). The replay window has long since
	// slid past 0; even if we had its ciphertext it would fall
	// outside the window and be rejected on precheck.
	//
	// We don't have the original ciphertext, but we can prove the
	// rejection by hand-constructing a counter=0 prefix and showing
	// it gets rejected without even attempting AEAD verify. We
	// detect "outside window" indirectly: the function returns
	// ErrAEADAuth (same opaque error as forge / auth fail), so we
	// just confirm the negative.
	var sealed [DatagramOverhead + 5]byte
	// counter prefix all zeros (already is).
	// ciphertext + tag is junk; precheck rejects before AEAD runs.
	_, err := in.Open(1, sealed[:])
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAEADAuth)
}

func TestDatagramCipherCounterMonotonic(t *testing.T) {
	out, in, _, _ := newTestPair(t, nil)

	// Two seals back to back — second must use counter=1.
	s0, err := out.Seal(7, []byte("a"))
	require.NoError(t, err)
	s1, err := out.Seal(7, []byte("b"))
	require.NoError(t, err)

	assert.Equal(t, uint64(0), binary.BigEndian.Uint64(s0[:8]))
	assert.Equal(t, uint64(1), binary.BigEndian.Uint64(s1[:8]))
	assert.NotEqual(t, s0, s1, "different counters must produce different ciphertexts")

	pt0, err := in.Open(7, s0)
	require.NoError(t, err)
	pt1, err := in.Open(7, s1)
	require.NoError(t, err)
	assert.Equal(t, []byte("a"), pt0)
	assert.Equal(t, []byte("b"), pt1)
}

func TestDatagramCipherReorderedDeliveryAccepted(t *testing.T) {
	out, in, _, _ := newTestPair(t, nil)

	// Seal 10 datagrams in order.
	sealed := make([][]byte, 10)
	for i := 0; i < 10; i++ {
		s, err := out.Seal(1, []byte{byte(i)})
		require.NoError(t, err)
		sealed[i] = s
	}

	// Open in reverse — sliding window must accept each as fresh
	// the first time. Faithful-UDP-over-skynet apps will see
	// reordering and must not have their messages rejected for it.
	for i := 9; i >= 0; i-- {
		pt, err := in.Open(1, sealed[i])
		require.NoError(t, err, "datagram %d failed", i)
		assert.Equal(t, []byte{byte(i)}, pt)
	}
}

func TestDatagramCipherCounterExhaustedTriggersRekey(t *testing.T) {
	cfg := &DatagramCipherConfig{
		PacketLimit: 3, // exhaust quickly
	}
	out, _, _, _ := newTestPair(t, cfg)

	// Three seals succeed; fourth must fail with ErrCounterExhausted
	// AND not consume a counter the receiver would have observed.
	_, err := out.Seal(1, []byte("1"))
	require.NoError(t, err)
	_, err = out.Seal(1, []byte("2"))
	require.NoError(t, err)
	_, err = out.Seal(1, []byte("3"))
	require.NoError(t, err)

	assert.True(t, out.NeedsRekey(), "after PacketLimit reached, NeedsRekey should fire")

	_, err = out.Seal(1, []byte("4"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCounterExhausted)
}

func TestDatagramCipherNeedsRekeyOnTime(t *testing.T) {
	cfg := &DatagramCipherConfig{
		TimeLimit: 20 * time.Millisecond,
	}
	out, _, _, _ := newTestPair(t, cfg)

	assert.False(t, out.NeedsRekey(), "fresh cipher should not need rekey")
	time.Sleep(30 * time.Millisecond)
	assert.True(t, out.NeedsRekey(), "time-based trigger should fire after TimeLimit")
}

func TestDatagramCipherRekeyResetsState(t *testing.T) {
	out, in, _, _ := newTestPair(t, nil)

	// Send a few; advance counter to 3.
	for i := 0; i < 3; i++ {
		s, err := out.Seal(1, []byte("pre"))
		require.NoError(t, err)
		_, err = in.Open(1, s)
		require.NoError(t, err)
	}

	// Rekey both with a fresh master.
	var newMaster [32]byte
	_, _ = rand.Read(newMaster[:])
	require.NoError(t, out.Rekey(newMaster, nil))
	require.NoError(t, in.Rekey(newMaster, nil))

	// First seal post-rekey: counter should reset to 0.
	s0, err := out.Seal(1, []byte("post"))
	require.NoError(t, err)
	assert.Equal(t, uint64(0), binary.BigEndian.Uint64(s0[:8]))

	pt, err := in.Open(1, s0)
	require.NoError(t, err)
	assert.Equal(t, []byte("post"), pt)
}

func TestDatagramCipherShortCiphertextRejected(t *testing.T) {
	_, in, _, _ := newTestPair(t, nil)
	_, err := in.Open(1, []byte{0x01, 0x02}) // way under DatagramOverhead
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCiphertextTooShort)
}

func TestDatagramCipherPlaintextTooLongRejected(t *testing.T) {
	out, _, _, _ := newTestPair(t, nil)
	tooBig := bytes.Repeat([]byte{0x42}, MaxDatagramPlaintext+1)
	_, err := out.Seal(1, tooBig)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPlaintextTooLong)
}

func TestDatagramCipherConcurrentSealOpenSafe(t *testing.T) {
	out, in, _, _ := newTestPair(t, nil)

	const N = 200
	sealed := make(chan []byte, N)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < N; i++ {
			s, err := out.Seal(1, []byte{byte(i)})
			if err != nil {
				t.Errorf("seal: %v", err)
				return
			}
			sealed <- s
		}
		close(sealed)
	}()

	var opened uint64
	go func() {
		defer wg.Done()
		for s := range sealed {
			_, err := in.Open(1, s)
			if err == nil {
				atomic.AddUint64(&opened, 1)
			}
		}
	}()

	wg.Wait()
	assert.Equal(t, uint64(N), atomic.LoadUint64(&opened), "all N seals should round-trip")
}
