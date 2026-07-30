// Package group cmd/apps/skychat/group/keyrotate_test.go c4-app-chat
//
// Unit coverage for the key-rotation envelope and the key ring: sealing
// to a recipient, the signature's domain separation and tamper
// resistance, and the ring's promote/retire rules. The wire half — a ban
// that actually cuts the banned member off — needs a transport and lives
// in key_rotation_integration_test.go.
package group

import (
	"bytes"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
)

// A wrap must open for its recipient and for nobody else. This is the
// property the whole feature rests on: the leaf is public, so exclusion
// has to come from the sealing, not from who can see the bytes.
func TestSealGroupKeyOnlyOpensForRecipient(t *testing.T) {
	adminPK, adminSK := cipher.GenerateKeyPair()
	memberPK, memberSK := cipher.GenerateKeyPair()
	_, evictedSK := cipher.GenerateKeyPair()

	key, err := GenerateAESKey()
	if err != nil {
		t.Fatalf("GenerateAESKey: %v", err)
	}
	sealed, err := sealGroupKey(adminSK, memberPK, key)
	if err != nil {
		t.Fatalf("sealGroupKey: %v", err)
	}
	if bytes.Contains(sealed, key) {
		t.Fatal("the sealed blob contains the group key verbatim")
	}

	got, err := openGroupKey(memberSK, adminPK, sealed)
	if err != nil {
		t.Fatalf("openGroupKey as the recipient: %v", err)
	}
	if !bytes.Equal(got, key) {
		t.Error("unwrapped key differs from the sealed one")
	}

	// The evicted member holds a valid key pair and can read the leaf.
	// It still must not be able to open a wrap addressed to someone else.
	if _, err := openGroupKey(evictedSK, adminPK, sealed); err == nil {
		t.Error("a non-recipient opened a wrap addressed to another member")
	}

	// Tampering fails the AEAD tag rather than yielding a wrong key.
	mangled := append([]byte(nil), sealed...)
	mangled[len(mangled)-1] ^= 0xff
	if _, err := openGroupKey(memberSK, adminPK, mangled); err == nil {
		t.Error("a tampered wrap opened")
	}

	// Too short to hold nonce + tag.
	if _, err := openGroupKey(memberSK, adminPK, []byte("short")); err == nil {
		t.Error("a truncated wrap opened")
	}
}

// ECDH symmetry is what lets a recipient derive the sealing key from the
// issuer's PK alone, with no prior exchange.
func TestDeriveWrapKeyIsSymmetric(t *testing.T) {
	aPK, aSK := cipher.GenerateKeyPair()
	bPK, bSK := cipher.GenerateKeyPair()

	fromA, err := deriveWrapKey(aSK, bPK)
	if err != nil {
		t.Fatalf("deriveWrapKey(aSK, bPK): %v", err)
	}
	fromB, err := deriveWrapKey(bSK, aPK)
	if err != nil {
		t.Fatalf("deriveWrapKey(bSK, aPK): %v", err)
	}
	if !bytes.Equal(fromA, fromB) {
		t.Error("ECDH is not symmetric across the pair")
	}
	if len(fromA) != 32 {
		t.Errorf("wrap key is %d bytes, want 32", len(fromA))
	}
}

func TestKeyMutationSignVerify(t *testing.T) {
	adminPK, adminSK := cipher.GenerateKeyPair()
	m1PK, _ := cipher.GenerateKeyPair()
	m2PK, _ := cipher.GenerateKeyPair()
	gid := uuid.New()
	key, _ := GenerateAESKey() //nolint:errcheck

	m, skipped, err := buildKeyMutation(gid, 3, key, []cipher.PubKey{m1PK, m2PK}, adminSK, time.Now().UTC())
	if err != nil {
		t.Fatalf("buildKeyMutation: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("skipped recipients: %v", skipped)
	}
	if m.IssuerPK != adminPK {
		t.Errorf("issuer = %s, want %s", m.IssuerPK, adminPK)
	}
	if len(m.Wraps) != 2 {
		t.Fatalf("got %d wraps, want 2", len(m.Wraps))
	}
	if err := VerifyKey(m); err != nil {
		t.Fatalf("VerifyKey: %v", err)
	}

	// Round trip through the wire codec.
	body, err := MarshalKey(m)
	if err != nil {
		t.Fatalf("MarshalKey: %v", err)
	}
	back, err := UnmarshalKey(body)
	if err != nil {
		t.Fatalf("UnmarshalKey: %v", err)
	}
	if back.Epoch != 3 || len(back.Wraps) != 2 {
		t.Errorf("round trip: %+v", back)
	}

	// Reordering the wraps must NOT break the signature — the canonical
	// bytes sort them, so a relaying admin can re-serialize freely.
	reordered := m
	reordered.Wraps = []KeyWrap{m.Wraps[1], m.Wraps[0]}
	if err := VerifyKey(reordered); err != nil {
		t.Errorf("reordered wraps should still verify: %v", err)
	}
}

// Every field inside the signed bytes has to be tamper-evident, and a
// signature over one envelope family must not verify as another.
func TestKeyMutationTamperAndDomainSeparation(t *testing.T) {
	_, adminSK := cipher.GenerateKeyPair()
	victimPK, _ := cipher.GenerateKeyPair()
	otherPK, _ := cipher.GenerateKeyPair()
	gid := uuid.New()
	key, _ := GenerateAESKey() //nolint:errcheck

	base, _, err := buildKeyMutation(gid, 2, key, []cipher.PubKey{victimPK}, adminSK, time.Now().UTC())
	if err != nil {
		t.Fatalf("buildKeyMutation: %v", err)
	}

	for name, mangle := range map[string]func(*KeyMutation){
		"epoch bump":        func(m *KeyMutation) { m.Epoch++ },
		"group id":          func(m *KeyMutation) { m.GroupID = uuid.New() },
		"issued at":         func(m *KeyMutation) { m.IssuedAt = m.IssuedAt.Add(time.Hour) },
		"parent seq":        func(m *KeyMutation) { m.ParentSeq++ },
		"recipient swapped": func(m *KeyMutation) { m.Wraps[0].RecipientPK = otherPK },
		"sealed bytes":      func(m *KeyMutation) { m.Wraps[0].Sealed[0] ^= 0xff },
		"wrap dropped":      func(m *KeyMutation) { m.Wraps = nil },
	} {
		t.Run(name, func(t *testing.T) {
			tampered := base
			tampered.Wraps = append([]KeyWrap(nil), base.Wraps...)
			if len(tampered.Wraps) > 0 {
				tampered.Wraps[0].Sealed = append([]byte(nil), base.Wraps[0].Sealed...)
			}
			mangle(&tampered)
			if err := VerifyKey(tampered); err == nil {
				t.Error("tampered envelope verified")
			}
		})
	}

	// A moderation signature must not verify as a key rotation. The two
	// families carry different domain tags for exactly this reason.
	if canonicalBytesKey(base) == nil {
		t.Fatal("canonical bytes are nil")
	}
	mod := ModerationMutation{GroupID: gid, Op: ModOpBan, PeerPK: victimPK, IssuedAt: base.IssuedAt}
	if err := SignMod(&mod, adminSK); err != nil {
		t.Fatalf("SignMod: %v", err)
	}
	crossed := base
	crossed.Signature = mod.Signature
	if err := VerifyKey(crossed); err == nil {
		t.Error("a moderation signature verified as a key rotation")
	}
}

// A rotation nobody can open is refused at signing time: it would be
// indistinguishable from destroying the group.
func TestSignKeyRefusesEmptyOrMalformed(t *testing.T) {
	_, sk := cipher.GenerateKeyPair()
	pk, _ := cipher.GenerateKeyPair()
	gid := uuid.New()
	now := time.Now().UTC()

	for name, m := range map[string]KeyMutation{
		"no wraps":       {GroupID: gid, Epoch: 1, IssuedAt: now},
		"no group id":    {Epoch: 1, IssuedAt: now, Wraps: []KeyWrap{{RecipientPK: pk, Sealed: []byte("x")}}},
		"no issued at":   {GroupID: gid, Epoch: 1, Wraps: []KeyWrap{{RecipientPK: pk, Sealed: []byte("x")}}},
		"zero recipient": {GroupID: gid, Epoch: 1, IssuedAt: now, Wraps: []KeyWrap{{Sealed: []byte("x")}}},
		"empty sealed":   {GroupID: gid, Epoch: 1, IssuedAt: now, Wraps: []KeyWrap{{RecipientPK: pk}}},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := m
			if err := SignKey(&candidate, sk); err == nil {
				t.Error("SignKey accepted a malformed mutation")
			}
		})
	}
}

// The ring is what keeps rotation forward-only: a new key takes over for
// new messages, the old one stays available for old ones.
func TestInstallKeyPromotesAndRetains(t *testing.T) {
	k0 := bytes.Repeat([]byte{0xa0}, 32)
	k1 := bytes.Repeat([]byte{0xa1}, 32)
	k2 := bytes.Repeat([]byte{0xa2}, 32)
	t0 := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)

	r := Record{Mode: ModePrivate, AESKey: k0}
	if !r.installKey(1, k1, t0, t0) {
		t.Fatal("installing a newer epoch should change the record")
	}
	if !bytes.Equal(r.AESKey, k1) || r.KeyEpoch != 1 {
		t.Fatalf("current key not promoted: epoch=%d", r.KeyEpoch)
	}
	if len(r.KeyRing) != 1 || !bytes.Equal(r.KeyRing[0].Key, k0) {
		t.Fatal("the superseded key was not retained")
	}

	// Idempotent: the same leaf arriving twice (re-broadcast, or seen
	// through two feeds) changes nothing.
	if r.installKey(1, k1, t0, t0) {
		t.Error("re-installing the same key reported a change")
	}

	// Decryption order is current-first, then newest-retired.
	keys := r.DecryptionKeys()
	if len(keys) != 2 || !bytes.Equal(keys[0], k1) || !bytes.Equal(keys[1], k0) {
		t.Errorf("DecryptionKeys order wrong: %d keys", len(keys))
	}

	// A same-epoch key from another admin issued LATER wins as current,
	// and the loser is still kept — the admin that minted it may already
	// have published under it.
	later := t0.Add(time.Second)
	if !r.installKey(1, k2, later, later) {
		t.Fatal("a later same-epoch key should be installed")
	}
	if !bytes.Equal(r.AESKey, k2) {
		t.Error("the later same-epoch key did not win")
	}
	if len(r.DecryptionKeys()) != 3 {
		t.Errorf("expected to hold all three keys, got %d", len(r.DecryptionKeys()))
	}

	// An older epoch never takes over, but is still retained so its
	// messages open.
	older := bytes.Repeat([]byte{0xa3}, 32)
	if !r.installKey(0, older, t0.Add(-time.Hour), t0) {
		t.Fatal("an older-epoch key should still be retained")
	}
	if !bytes.Equal(r.AESKey, k2) {
		t.Error("an older-epoch key took over as current")
	}

	// A malformed key is refused outright.
	if r.installKey(9, []byte("too short"), t0, t0) {
		t.Error("a short key was installed")
	}
}

// The ring is bounded, and it must drop from the OLD end so recent
// history keeps working.
func TestKeyRingIsBounded(t *testing.T) {
	r := Record{Mode: ModePrivate}
	at := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	for i := 0; i <= maxKeyRing+5; i++ {
		key := bytes.Repeat([]byte{byte(i)}, 32)
		if !r.installKey(uint64(i), key, at.Add(time.Duration(i)*time.Second), at) {
			t.Fatalf("install %d failed", i)
		}
	}
	if len(r.KeyRing) != maxKeyRing {
		t.Errorf("ring holds %d keys, want the %d cap", len(r.KeyRing), maxKeyRing)
	}
	// Newest retired first: the key one epoch behind current.
	if r.KeyRing[0].Epoch != r.KeyEpoch-1 {
		t.Errorf("ring head is epoch %d, want %d", r.KeyRing[0].Epoch, r.KeyEpoch-1)
	}
}

// decryptWithRing is the read path: a body sealed under any key we still
// hold must open, and one sealed under a key we never had must not.
func TestDecryptWithRing(t *testing.T) {
	k0 := bytes.Repeat([]byte{0xb0}, 32)
	k1 := bytes.Repeat([]byte{0xb1}, 32)
	stranger := bytes.Repeat([]byte{0xb2}, 32)
	at := time.Now().UTC()

	r := Record{Mode: ModePrivate, AESKey: k0}
	r.installKey(1, k1, at, at) //nolint:errcheck

	// Sealed under the retired key — the pre-rotation history case.
	ct, nonce, err := Encrypt(k0, []byte("published before the rotation"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	pt, err := decryptWithRing(r.DecryptionKeys(), ct, nonce)
	if err != nil {
		t.Fatalf("history did not open after rotation: %v", err)
	}
	if string(pt) != "published before the rotation" {
		t.Errorf("got %q", pt)
	}

	// Sealed under the current key.
	ct, nonce, _ = Encrypt(k1, []byte("after")) //nolint:errcheck
	if _, err := decryptWithRing(r.DecryptionKeys(), ct, nonce); err != nil {
		t.Errorf("current-key body did not open: %v", err)
	}

	// Sealed under a key we never held — the evicted member's view of
	// everything published after their rotation.
	ct, nonce, _ = Encrypt(stranger, []byte("not for us")) //nolint:errcheck
	if _, err := decryptWithRing(r.DecryptionKeys(), ct, nonce); err == nil {
		t.Error("a body under an unknown key opened")
	}

	// No keys at all.
	if _, err := decryptWithRing(nil, ct, nonce); err == nil {
		t.Error("decrypting with no keys succeeded")
	}
}

func TestKeyStateRoundTrip(t *testing.T) {
	at := time.Now().UTC()
	r := Record{Mode: ModePrivate, AESKey: bytes.Repeat([]byte{1}, 32)}
	r.installKey(1, bytes.Repeat([]byte{2}, 32), at, at) //nolint:errcheck

	st := keyStateOf(r)
	var dst Record
	st.applyTo(&dst)
	if dst.KeyEpoch != r.KeyEpoch || !bytes.Equal(dst.AESKey, r.AESKey) {
		t.Error("key state did not round trip")
	}
	if len(dst.KeyRing) != len(r.KeyRing) {
		t.Errorf("ring length %d, want %d", len(dst.KeyRing), len(r.KeyRing))
	}
	// Deep copy, not an alias: a later mutation of the snapshot must not
	// reach through into the record it was taken from.
	st.Key[0] ^= 0xff
	if bytes.Equal(st.Key, r.AESKey) {
		t.Error("keyStateOf aliased the record's key")
	}
}
