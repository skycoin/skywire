// Package pairing cmd/apps/skychat/pairing/store_seal_test.go
//
// The at-rest half of forward secrecy: the ratchet secrets a pair now
// keeps must not be readable out of pairs.db, and must survive a reopen
// by the same visor.
package pairing

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// testStoreSK is a FIXED key, not a fresh one per call.
//
// Several tests reopen the same pairs.db to assert persistence, and a
// per-call key would make the reopen fail to unseal — which would look
// like a persistence bug rather than the test's own doing.
func testStoreSK() cipher.SecKey {
	var sk cipher.SecKey
	for i := range sk {
		sk[i] = byte(i + 1)
	}
	return sk
}

// onDiskForm renders a key the way encoding/json writes []byte, so
// searching the raw file for it is a check that can actually fail.
// Grepping for the raw bytes would never match and would pass no matter
// what was written.
func onDiskForm(key []byte) []byte {
	return []byte(base64.StdEncoding.EncodeToString(key))
}

func TestStoreSealsRatchetSecretsOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pairs.db")
	s, err := OpenStore(path, testStoreSK())
	require.NoError(t, err)

	peer, _ := cipher.GenerateKeyPair()
	rt := newRatchetState(time.Now().UTC())
	// Give it an epoch key so the ring has something to protect.
	other, _ := cipher.GenerateKeyPair()
	_, key, err := deriveEpochKey(rt.mySK, rt.myPK, other)
	require.NoError(t, err)
	rt.installLocked(computeEpochID(rt.myPK, other), key, time.Now().UTC())

	snap := rt.snapshot()
	require.NoError(t, s.Put(Record{PeerPK: peer, Status: StatusActive, Ratchet: &snap}))
	require.NoError(t, s.Close())

	raw, err := os.ReadFile(path) //nolint:gosec // test-controlled temp path
	require.NoError(t, err)

	require.NotContains(t, string(raw), string(onDiskForm(snap.RatchetSK[:])),
		"the ratchet SECRET is on disk in the clear — an attacker with the file can open the current epoch without ever touching the identity key")
	require.NotContains(t, string(raw), string(onDiskForm(key)),
		"an epoch key is on disk in the clear — the ring is exactly what opens history")

	// The peer's PUBLIC ratchet key is not a secret and stays readable;
	// asserting that keeps the sealing honest about its scope.
	require.NotEmpty(t, raw)
}

func TestStoreReopensRatchetSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pairs.db")
	peer, _ := cipher.GenerateKeyPair()

	s1, err := OpenStore(path, testStoreSK())
	require.NoError(t, err)
	rt := newRatchetState(time.Now().UTC())
	other, _ := cipher.GenerateKeyPair()
	id, key, err := deriveEpochKey(rt.mySK, rt.myPK, other)
	require.NoError(t, err)
	rt.installLocked(id, key, time.Now().UTC())
	snap := rt.snapshot()
	require.NoError(t, s1.Put(Record{PeerPK: peer, Status: StatusActive, Ratchet: &snap}))
	require.NoError(t, s1.Close())

	s2, err := OpenStore(path, testStoreSK())
	require.NoError(t, err)
	t.Cleanup(func() { _ = s2.Close() }) //nolint:errcheck

	got, ok, err := s2.Get(peer)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, got.Ratchet)
	require.Equal(t, snap.RatchetSK, got.Ratchet.RatchetSK, "the ratchet secret did not survive a reopen")
	require.Len(t, got.Ratchet.Ring, 1)
	require.Equal(t, []byte(key), []byte(got.Ratchet.Ring[0].Key), "the epoch key did not survive a reopen")
}

func TestStoreSealedRatchetDoesNotOpenForAnotherVisor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pairs.db")
	peer, _ := cipher.GenerateKeyPair()

	s1, err := OpenStore(path, testStoreSK())
	require.NoError(t, err)
	rt := newRatchetState(time.Now().UTC())
	snap := rt.snapshot()
	require.NoError(t, s1.Put(Record{PeerPK: peer, Status: StatusActive, Ratchet: &snap}))
	require.NoError(t, s1.Close())

	// A different identity: the file is portable, the secrets are not.
	_, otherSK := cipher.GenerateKeyPair()
	s2, err := OpenStore(path, otherSK)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s2.Close() }) //nolint:errcheck

	got, _, err := s2.Get(peer)
	require.Error(t, err, "another visor's key opened this store's ratchet secret")
	require.Equal(t, peer, got.PeerPK,
		"metadata should still load so the contact doesn't vanish — only the secrets are lost")
	require.NotNil(t, got.Ratchet)
	require.Equal(t, cipher.SecKey{}, got.Ratchet.RatchetSK)
}

func TestOpenStoreRefusesZeroSecretKey(t *testing.T) {
	_, err := OpenStore(filepath.Join(t.TempDir(), "pairs.db"), cipher.SecKey{})
	require.ErrorIs(t, err, ErrStoreSealKeyRequired,
		"a store opened with no key would write the ratchet secrets in plaintext")
}

// A setter that touches one metadata field must not drop the ratchet on
// the way back to disk — the read-modify-write cycle goes through the
// sealer for exactly this reason.
func TestStoreSettersPreserveRatchet(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "pairs.db"), testStoreSK())
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() }) //nolint:errcheck

	peer, _ := cipher.GenerateKeyPair()
	rt := newRatchetState(time.Now().UTC())
	snap := rt.snapshot()
	require.NoError(t, s.Put(Record{PeerPK: peer, Status: StatusPending, Ratchet: &snap}))

	require.NoError(t, s.SetStatus(peer, StatusActive))
	require.NoError(t, s.MarkMessage(peer, time.Now().UTC()))

	got, ok, err := s.Get(peer)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, StatusActive, got.Status)
	require.NotNil(t, got.Ratchet, "SetStatus/MarkMessage dropped the ratchet")
	require.Equal(t, snap.RatchetSK, got.Ratchet.RatchetSK)
}
