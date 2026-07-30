// Package group cmd/apps/skychat/group/key_rotation_integration_test.go
//
// The dmsg-backed lane for key rotation: the property that a ban stops
// being "loses network access" and becomes "actually cut off".
//
// These cannot be reached without a real transport — the rotation travels
// as a signed leaf on the admin's feed and the remaining member has to
// receive it, unwrap its own copy, and converge on the new epoch.
//
// Reuses the harness in manager_integration_test.go; skipped under -short
// like every other dmsg-backed test in this package.
package group

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The headline property. B is banned from an encrypted group; the group
// re-keys, and the key B still holds no longer opens what A publishes
// afterwards — while everything published before the ban stays readable
// to the members who remain.
func TestKeyRotation_BanCutsOffTheBannedMember(t *testing.T) {
	nodes := newGroupEnvN(t, 3)
	a, b, c := nodes[0], nodes[1], nodes[2]

	rec, err := a.mgr.Create("sealed room", KindPrivate, nil)
	require.NoError(t, err, "Create")
	require.Len(t, rec.AESKey, 32)
	require.Zero(t, rec.KeyEpoch, "a fresh group starts at epoch 0")

	// Pre-admit both so the joins are answered "admitted" immediately.
	_, err = a.mgr.AddMember(rec.ID, b.pk)
	require.NoError(t, err)
	_, err = a.mgr.AddMember(rec.ID, c.pk)
	require.NoError(t, err)

	inv := inviteFor(t, a, rec.ID)
	joinedB, err := b.mgr.Join(inv)
	require.NoError(t, err, "B joins")
	joinedC, err := c.mgr.Join(inv)
	require.NoError(t, err, "C joins")

	// Both hold the founding key — this is what a ban used to leave
	// behind.
	keyBeforeBan := append([]byte(nil), rec.AESKey...)
	require.Equal(t, keyBeforeBan, joinedB.AESKey)
	require.Equal(t, keyBeforeBan, joinedC.AESKey)

	// Ban B. The moderation command rotates as its last step.
	banned, err := a.mgr.BanMember(rec.ID, b.pk)
	require.NoError(t, err, "BanMember")
	require.True(t, banned.IsBanned(b.pk))
	require.NotContains(t, banned.Members, b.pk)

	afterBan, ok, err := a.mgr.Get(rec.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(1), afterBan.KeyEpoch, "the ban must have rotated the key")
	require.NotEqual(t, keyBeforeBan, afterBan.AESKey, "the group is still on the key the banned peer holds")
	require.Len(t, afterBan.AESKey, 32)

	// The key B holds cannot open what A publishes now. Assert against
	// the crypto rather than the transport: B's subscription is also gone,
	// but that is the OLD protection — the point here is that even a copy
	// of the feed is useless to B.
	ct, nonce, err := Encrypt(afterBan.AESKey, []byte("after the ban"))
	require.NoError(t, err, "Encrypt under the rotated key")
	_, err = Decrypt(keyBeforeBan, ct, nonce)
	require.Error(t, err, "the banned peer's key still opens post-ban messages")

	// C, who stayed, converges onto the new epoch by unwrapping its own
	// sealed copy off A's feed.
	waitMember(t, c, rec.ID, func(r Record) bool { return r.KeyEpoch == 1 },
		"C never received the rotated key")
	onC, _, err := c.mgr.Get(rec.ID)
	require.NoError(t, err)
	require.Equal(t, afterBan.AESKey, onC.AESKey, "C converged on a different key than the admin issued")

	// And C can still read history from before the rotation: the retired
	// key is kept, so a re-key does not erase the room.
	histCT, histNonce, err := Encrypt(keyBeforeBan, []byte("before the ban"))
	require.NoError(t, err)
	plain, err := decryptWithRing(onC.DecryptionKeys(), histCT, histNonce)
	require.NoError(t, err, "pre-rotation history stopped opening for a remaining member")
	require.Equal(t, "before the ban", string(plain))
}

// A kick is weaker than a ban — the peer may come back — but it still
// ends read access now, so it rotates too. And when it does come back,
// admission hands it the current key rather than the one it used to hold.
func TestKeyRotation_KickRotatesAndRejoinGetsTheNewKey(t *testing.T) {
	a, b := newGroupEnv(t)

	rec, err := a.mgr.Create("sealed room", KindPrivate, nil)
	require.NoError(t, err)
	_, err = a.mgr.AddMember(rec.ID, b.pk)
	require.NoError(t, err)

	inv := inviteFor(t, a, rec.ID)
	joined, err := b.mgr.Join(inv)
	require.NoError(t, err)
	oldKey := append([]byte(nil), joined.AESKey...)

	_, err = a.mgr.RemoveMember(rec.ID, b.pk)
	require.NoError(t, err, "RemoveMember")

	afterKick, _, err := a.mgr.Get(rec.ID)
	require.NoError(t, err)
	require.Equal(t, uint64(1), afterKick.KeyEpoch, "a kick must rotate the key too")
	require.NotEqual(t, oldKey, afterKick.AESKey)

	// B asks to rejoin. A kick is not a ban, so it is re-admitted — and
	// the admission response carries the CURRENT key and epoch, not the
	// one it held before.
	require.NoError(t, b.mgr.Leave(rec.ID))
	require.NoError(t, b.store.Delete(rec.ID))
	_, err = a.mgr.AddMember(rec.ID, b.pk)
	require.NoError(t, err)

	rejoined, err := b.mgr.RequestJoin(inviteFor(t, a, rec.ID), "")
	require.NoError(t, err, "a kicked peer should be able to rejoin")
	require.Equal(t, afterKick.AESKey, rejoined.AESKey, "the rejoiner did not get the current key")
	require.Equal(t, afterKick.KeyEpoch, rejoined.KeyEpoch, "the rejoiner's epoch disagrees with the group")
}

// Manual rotation is the operator's lever for a key believed leaked
// without anyone being evicted. It must reach the other members and be
// refused on a plaintext group, where there is no key and pretending
// otherwise would imply a protection the group does not have.
func TestKeyRotation_ManualRotateAndPlaintextRefusal(t *testing.T) {
	a, b := newGroupEnv(t)

	priv, err := a.mgr.Create("sealed room", KindPrivate, nil)
	require.NoError(t, err)
	_, err = a.mgr.AddMember(priv.ID, b.pk)
	require.NoError(t, err)
	_, err = b.mgr.Join(inviteFor(t, a, priv.ID))
	require.NoError(t, err)

	rotated, err := a.mgr.RotateKey(priv.ID)
	require.NoError(t, err, "RotateKey")
	require.Equal(t, uint64(1), rotated.KeyEpoch)
	require.NotEqual(t, priv.AESKey, rotated.AESKey)

	waitMember(t, b, priv.ID, func(r Record) bool { return r.KeyEpoch == 1 },
		"B never received the manually rotated key")
	onB, _, err := b.mgr.Get(priv.ID)
	require.NoError(t, err)
	require.Equal(t, rotated.AESKey, onB.AESKey)

	// Rotating again advances the epoch rather than reusing it.
	again, err := a.mgr.RotateKey(priv.ID)
	require.NoError(t, err, "second RotateKey")
	require.Equal(t, uint64(2), again.KeyEpoch)
	require.NotEqual(t, rotated.AESKey, again.AESKey)

	// A public group has no key to rotate.
	pub, err := a.mgr.Create("open room", KindPublic, nil)
	require.NoError(t, err)
	_, err = a.mgr.RotateKey(pub.ID)
	require.ErrorIs(t, err, ErrKeyRotationNotEncrypted)

	// And a non-admin cannot rotate.
	_, err = b.mgr.RotateKey(priv.ID)
	require.Error(t, err, "a plain member rotated the group key")
	require.Contains(t, err.Error(), "admin")
}

// Messages have to keep flowing across a rotation. A sender that hasn't
// applied the new key yet is still encrypting under the old one, and the
// receiver must open it from the ring rather than dropping it — otherwise
// every re-key would punch a hole in the conversation.
func TestKeyRotation_MessagesSurviveTheRotationWindow(t *testing.T) {
	a, b := newGroupEnv(t)

	rec, err := a.mgr.Create("sealed room", KindPrivate, nil)
	require.NoError(t, err)
	_, err = a.mgr.AddMember(rec.ID, b.pk)
	require.NoError(t, err)
	_, err = b.mgr.Join(inviteFor(t, a, rec.ID))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const beforeText = "sent before the re-key"
	require.NoError(t, a.mgr.SendToGroup(ctx, rec.ID, beforeText))
	waitInbox(t, b.inbox, "the pre-rotation message", hasText(beforeText))

	rotated, err := a.mgr.RotateKey(rec.ID)
	require.NoError(t, err, "RotateKey")
	require.Equal(t, uint64(1), rotated.KeyEpoch)

	// A publishes under the new key; B has to have applied the rotation to
	// read it. Re-send on a timer because convergence is asynchronous.
	const afterText = "sent after the re-key"
	go func() {
		for i := 0; i < 30; i++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
				_ = a.mgr.SendToGroup(ctx, rec.ID, afterText) //nolint:errcheck
			}
		}
	}()
	require.NoError(t, a.mgr.SendToGroup(ctx, rec.ID, afterText))
	waitInbox(t, b.inbox, "the post-rotation message", hasText(afterText))

	// The earlier message is still in B's inbox — reading forward did not
	// invalidate what it had already decrypted.
	require.True(t, hasText(beforeText)(b.inbox.snapshot()),
		"the pre-rotation message vanished from the inbox")
}
