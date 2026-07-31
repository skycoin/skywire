// Package group pkg/skychat/group/filekey_test.go c4-app-chat
//
// Attachment keys: that they are scoped to one file in one group, that the
// group key never has to leave to produce one, and that the ring is offered
// in the order that keeps an attachment readable across a rotation.
package group

import (
	"bytes"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestDeriveFileKeyIsScopedAndDeterministic(t *testing.T) {
	groupKey := bytes.Repeat([]byte{0x01}, 32)

	k1, err := deriveFileKey(groupKey, "gid-a", "file-1")
	require.NoError(t, err)
	require.Len(t, k1, 32)

	again, err := deriveFileKey(groupKey, "gid-a", "file-1")
	require.NoError(t, err)
	require.Equal(t, k1, again, "the derivation is not deterministic — the same file would seal and open under different keys")

	// A different file, a different group, or a different group key all
	// produce a different key. The first is what bounds a leaked file key
	// to one attachment; the second is what stops an id collision across
	// groups from mattering.
	other, err := deriveFileKey(groupKey, "gid-a", "file-2")
	require.NoError(t, err)
	require.NotEqual(t, k1, other, "two files in one group share a key")

	otherGroup, err := deriveFileKey(groupKey, "gid-b", "file-1")
	require.NoError(t, err)
	require.NotEqual(t, k1, otherGroup, "the same file id in two groups shares a key")

	otherEpoch, err := deriveFileKey(bytes.Repeat([]byte{0x02}, 32), "gid-a", "file-1")
	require.NoError(t, err)
	require.NotEqual(t, k1, otherEpoch, "a rotation did not change the file key")

	// And the group key itself is not recoverable by inspection — the
	// derived key must not simply BE it.
	require.NotEqual(t, groupKey, k1)
}

func TestDeriveFileKeyRejectsBadInput(t *testing.T) {
	_, err := deriveFileKey(bytes.Repeat([]byte{1}, 16), "g", "f")
	require.Error(t, err, "a short group key was accepted")
	_, err = deriveFileKey(bytes.Repeat([]byte{1}, 32), "g", "")
	require.Error(t, err, "an empty file id was accepted")
}

// A public group hands its key to whoever asks, so there is nothing to
// seal with and nothing gained by pretending otherwise.
func TestFileKeysAreAbsentForAPlaintextGroup(t *testing.T) {
	r := Record{ID: uuid.NewString(), Mode: ModePublic, Kind: KindPublic}
	seal, open, err := r.FileKeys("file-1")
	require.NoError(t, err)
	require.Nil(t, seal)
	require.Nil(t, open)
}

// The ordering is the whole contract: seal with the current key, try the
// current key first when opening, and keep the retired ones available so a
// rotation does not make yesterday's attachment unreadable.
func TestFileKeysCoverTheRingCurrentFirst(t *testing.T) {
	cur := bytes.Repeat([]byte{0xa1}, 32)
	old := bytes.Repeat([]byte{0xa0}, 32)
	r := Record{
		ID:       uuid.NewString(),
		Mode:     ModePrivate,
		Kind:     KindPrivate,
		AESKey:   cur,
		KeyEpoch: 1,
		KeyRing:  []GroupKey{{Epoch: 0, Key: old, AddedAt: time.Now().UTC()}},
	}

	seal, open, err := r.FileKeys("file-7")
	require.NoError(t, err)
	require.Len(t, open, 2, "the ring key is missing from the open set")

	wantCur, err := deriveFileKey(cur, r.ID, "file-7")
	require.NoError(t, err)
	wantOld, err := deriveFileKey(old, r.ID, "file-7")
	require.NoError(t, err)

	require.Equal(t, wantCur, seal, "sealing does not use the current key")
	require.Equal(t, wantCur, open[0], "the current key is not tried first")
	require.Equal(t, wantOld, open[1], "the retired key is not offered for older attachments")
}

// An encrypted group with no key at all is an error, not a silent
// plaintext fallback — that distinction is what keeps a joiner whose
// admission response is still in flight from publishing a readable file.
func TestFileKeysFailWhenAnEncryptedGroupHoldsNoKey(t *testing.T) {
	r := Record{ID: uuid.NewString(), Mode: ModePrivate, Kind: KindPrivate}
	_, _, err := r.FileKeys("file-1")
	require.Error(t, err)
}
