//go:build !js

// Package group pkg/skychat/group/store_seal_test.go c4-app-chat
//
// Tests for encryption at rest. The load-bearing assertion is the plain
// one: read the bytes bbolt actually wrote and check the group key is not
// in them.
package group

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/skycoin/skywire/pkg/cipher"
)

// testStoreSK is the store-sealing key every test store opens with.
// Deterministic per call is not required — records are only read back by
// the same Store instance — but a real key is, since the sealer refuses
// the zero value.
func testStoreSK() cipher.SecKey {
	_, sk := cipher.GenerateKeyPair()
	return sk
}

// onDiskForm returns the key as it would APPEAR in a record on disk.
//
// Not the raw bytes: encoding/json renders []byte as base64, so searching
// the file for the key's bytes would never match and the assertion would
// pass no matter what was written. Searching for the base64 form is the
// check that actually means something.
func onDiskForm(key []byte) []byte {
	return []byte(base64.StdEncoding.EncodeToString(key))
}

// rawStoreRecord returns the bytes on disk for a group, bypassing the
// store entirely. This is the only honest way to assert what was written.
// The caller must have CLOSED the store first — bolt takes an exclusive
// file lock, so a second open against a live store just times out.
func rawStoreRecord(t *testing.T, path, id string) []byte {
	t.Helper()
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 2 * time.Second, ReadOnly: true})
	if err != nil {
		t.Fatalf("reopen bolt: %v", err)
	}
	defer db.Close() //nolint:errcheck
	var out []byte
	if err := db.View(func(tx *bolt.Tx) error {
		out = append([]byte(nil), tx.Bucket([]byte(groupsBucket)).Get([]byte(id))...)
		return nil
	}); err != nil {
		t.Fatalf("read raw record: %v", err)
	}
	return out
}

// The headline property: a private group's key must not be recoverable
// from the file. Round-tripping through the store must still hand it back.
func TestStoreSealsKeyMaterialOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "groups.db")
	sk := testStoreSK()
	st, err := OpenStore(path, sk)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	id := "11111111-2222-3333-4444-555555555555"
	key := bytes.Repeat([]byte{0xd1}, 32)
	retired := bytes.Repeat([]byte{0xd0}, 32)
	rec := Record{
		ID: id, OwnerPK: pkN(1), Port: 40021,
		Mode: ModePrivate, Kind: KindPrivate, Role: RoleOwner, Status: StatusActive,
		AESKey: key, KeyEpoch: 1,
		KeyRing: []GroupKey{{Epoch: 0, Key: retired, AddedAt: time.Now().UTC()}},
	}
	if err := st.Put(rec); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	raw := rawStoreRecord(t, path, id)
	if len(raw) == 0 {
		t.Fatal("no record was written")
	}
	if bytes.Contains(raw, onDiskForm(key)) {
		t.Error("the current group key is on disk in the clear")
	}
	if bytes.Contains(raw, onDiskForm(retired)) {
		t.Error("a retired group key is on disk in the clear")
	}
	// The whole file, not just this record — a stray copy elsewhere would
	// defeat the point.
	whole, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatalf("read db file: %v", err)
	}
	if bytes.Contains(whole, onDiskForm(key)) || bytes.Contains(whole, onDiskForm(retired)) {
		t.Error("group key bytes appear somewhere in groups.db")
	}
	// And the plaintext JSON field is gone rather than merely overwritten.
	var onDisk map[string]json.RawMessage
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("on-disk record is not JSON: %v", err)
	}
	if _, present := onDisk["aes_key"]; present {
		t.Error("the on-disk record still has an aes_key field")
	}
	if _, present := onDisk["aes_key_sealed"]; !present {
		t.Error("the on-disk record has no aes_key_sealed field")
	}
	// The non-secret half stays readable — that is deliberate, and a
	// regression here would mean the store had become opaque to queries.
	if _, present := onDisk["members"]; !present {
		t.Error("members should NOT be sealed")
	}

	// Reopening with the same key returns the plaintext keys.
	st2, err := OpenStore(path, sk)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close() //nolint:errcheck
	got, ok, err := st2.Get(id)
	if err != nil || !ok {
		t.Fatalf("Get after reopen: err=%v ok=%v", err, ok)
	}
	if !bytes.Equal(got.AESKey, key) {
		t.Error("the current key did not survive the round trip")
	}
	if len(got.KeyRing) != 1 || !bytes.Equal(got.KeyRing[0].Key, retired) {
		t.Error("the retired key did not survive the round trip")
	}
	// List takes the same path as Get.
	all, err := st2.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 || !bytes.Equal(all[0].AESKey, key) {
		t.Error("List did not open the sealed key")
	}
}

// Setters go through update(), which reads-modifies-writes. A setter that
// touches something unrelated must neither lose the key nor leave it in
// the clear.
func TestStoreSettersPreserveSealedKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "groups.db")
	sk := testStoreSK()
	st, err := OpenStore(path, sk)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	id := "22222222-3333-4444-5555-666666666666"
	key := bytes.Repeat([]byte{0xe1}, 32)
	if err := st.Put(Record{
		ID: id, OwnerPK: pkN(1), Port: 40022,
		Mode: ModePrivate, Kind: KindPrivate, Role: RoleOwner, Status: StatusActive,
		AESKey: key,
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// An unrelated setter.
	if err := st.SetStatus(id, StatusLeft); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	got, _, err := st.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got.AESKey, key) {
		t.Fatal("a status change dropped the group key")
	}
	if got.Status != StatusLeft {
		t.Errorf("status = %q", got.Status)
	}

	// And the key setter itself, which hands update() plaintext to re-seal.
	rotated := bytes.Repeat([]byte{0xe2}, 32)
	if err := st.SetKeyState(id, KeyState{
		Epoch: 1, Key: rotated,
		KeyRing: []GroupKey{{Epoch: 0, Key: key, AddedAt: time.Now().UTC()}},
	}); err != nil {
		t.Fatalf("SetKeyState: %v", err)
	}
	got, _, err = st.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got.AESKey, rotated) || got.KeyEpoch != 1 {
		t.Error("SetKeyState did not install the rotated key")
	}
	if len(got.KeyRing) != 1 || !bytes.Equal(got.KeyRing[0].Key, key) {
		t.Error("SetKeyState did not retain the previous key")
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	raw := rawStoreRecord(t, path, id)
	if bytes.Contains(raw, onDiskForm(rotated)) || bytes.Contains(raw, onDiskForm(key)) {
		t.Error("SetKeyState wrote key material in the clear")
	}
}

// A groups.db is bound to the visor that wrote it. Another visor's key
// must not open the group keys — and the record must still be usable for
// everything that isn't secret, so the failure is diagnosable rather than
// a group that silently disappeared.
func TestStoreSealedKeysDoNotOpenWithAnotherVisorsKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "groups.db")
	st, err := OpenStore(path, testStoreSK())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	id := "33333333-4444-5555-6666-777777777777"
	key := bytes.Repeat([]byte{0xf1}, 32)
	if err := st.Put(Record{
		ID: id, Name: "sealed", OwnerPK: pkN(1), Port: 40023,
		Mode: ModePrivate, Kind: KindPrivate, Role: RoleOwner, Status: StatusActive,
		AESKey: key,
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	other, err := OpenStore(path, testStoreSK())
	if err != nil {
		t.Fatalf("reopen with a different key: %v", err)
	}
	defer other.Close() //nolint:errcheck
	got, ok, err := other.Get(id)
	if err == nil {
		t.Error("a foreign store key opened the sealed group key")
	}
	if !ok {
		t.Fatal("the record itself should still be found")
	}
	if len(got.AESKey) != 0 {
		t.Error("a key was recovered with the wrong store key")
	}
	if got.Name != "sealed" {
		t.Errorf("the unsealed half of the record should still be readable, got name %q", got.Name)
	}
}

// Records written by a build that had no sealing must keep working, and
// must stop being plaintext on open rather than lingering until something
// happens to rewrite them.
func TestStoreMigratesLegacyPlaintextKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "groups.db")
	sk := testStoreSK()
	id := "44444444-5555-6666-7777-888888888888"
	key := bytes.Repeat([]byte{0x9a}, 32)
	ringKey := bytes.Repeat([]byte{0x9b}, 32)

	// Write the pre-sealing shape by hand: plaintext aes_key, no sealed
	// field. This is exactly what an older build left behind.
	legacy := Record{
		ID: id, Name: "old", OwnerPK: pkN(1), Port: 40024,
		Mode: ModePrivate, Kind: KindPrivate, Role: RoleOwner, Status: StatusActive,
		AESKey: key, KeyEpoch: 2,
		KeyRing: []GroupKey{{Epoch: 1, Key: ringKey, AddedAt: time.Now().UTC()}},
	}
	body, err := json.Marshal(&legacy)
	if err != nil {
		t.Fatalf("marshal legacy record: %v", err)
	}
	if !bytes.Contains(body, onDiskForm(key)) {
		t.Fatal("setup: the hand-written record should contain the plaintext key")
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("bolt open: %v", err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		b, e := tx.CreateBucketIfNotExists([]byte(groupsBucket))
		if e != nil {
			return e
		}
		return b.Put([]byte(id), body)
	}); err != nil {
		t.Fatalf("seed legacy record: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}

	st, err := OpenStore(path, sk)
	if err != nil {
		t.Fatalf("OpenStore over a legacy file: %v", err)
	}
	if n := st.MigratedRecords(); n != 1 {
		t.Errorf("MigratedRecords = %d, want 1", n)
	}
	got, ok, err := st.Get(id)
	if err != nil || !ok {
		t.Fatalf("Get: err=%v ok=%v", err, ok)
	}
	if !bytes.Equal(got.AESKey, key) {
		t.Error("the legacy key was not preserved across migration")
	}
	if len(got.KeyRing) != 1 || !bytes.Equal(got.KeyRing[0].Key, ringKey) {
		t.Error("the legacy ring key was not preserved")
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if raw := rawStoreRecord(t, path, id); bytes.Contains(raw, onDiskForm(key)) || bytes.Contains(raw, onDiskForm(ringKey)) {
		t.Error("opening the store left legacy keys in the clear on disk")
	}

	// Second open has nothing left to do.
	st2, err := OpenStore(path, sk)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer st2.Close() //nolint:errcheck
	if n := st2.MigratedRecords(); n != 0 {
		t.Errorf("second open migrated %d records, want 0", n)
	}
}

// A store cannot be opened without an identity to derive from. Falling
// back to plaintext would defeat the whole file.
func TestOpenStoreRefusesZeroSecretKey(t *testing.T) {
	_, err := OpenStore(filepath.Join(t.TempDir(), "groups.db"), cipher.SecKey{})
	if err == nil {
		t.Fatal("OpenStore accepted a zero secret key")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("secret key")) {
		t.Errorf("unhelpful error: %v", err)
	}
}

// A sealed blob is bound to its slot, so someone with write access to the
// file cannot move one group's key into another group's record — or a
// retired key into the current slot.
func TestSealedKeysAreBoundToTheirSlot(t *testing.T) {
	sealer, err := newRecordSealer(testStoreSK())
	if err != nil {
		t.Fatalf("newRecordSealer: %v", err)
	}
	key := bytes.Repeat([]byte{0x77}, 32)

	sealed, err := sealer.seal(key, sealAAD("group-a", 0, true))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := sealer.open(sealed, sealAAD("group-a", 0, true)); err != nil {
		t.Fatalf("open in its own slot: %v", err)
	}
	if _, err := sealer.open(sealed, sealAAD("group-b", 0, true)); err == nil {
		t.Error("a blob opened under a different group's id")
	}
	if _, err := sealer.open(sealed, sealAAD("group-a", 0, false)); err == nil {
		t.Error("a current-key blob opened as a ring entry")
	}

	ringSealed, err := sealer.seal(key, sealAAD("group-a", 3, false))
	if err != nil {
		t.Fatalf("seal ring: %v", err)
	}
	if _, err := sealer.open(ringSealed, sealAAD("group-a", 4, false)); err == nil {
		t.Error("a ring blob opened in a different epoch's slot")
	}

	// Tampering and truncation both fail closed.
	mangled := append([]byte(nil), sealed...)
	mangled[len(mangled)-1] ^= 0xff
	if _, err := sealer.open(mangled, sealAAD("group-a", 0, true)); err == nil {
		t.Error("a tampered blob opened")
	}
	if _, err := sealer.open([]byte("short"), sealAAD("group-a", 0, true)); err == nil {
		t.Error("a truncated blob opened")
	}
}

// The at-rest key must depend on the visor secret key and nothing else,
// so the same visor always reopens its own file.
func TestRecordSealerIsDeterministicPerIdentity(t *testing.T) {
	sk := testStoreSK()
	a, err := newRecordSealer(sk)
	if err != nil {
		t.Fatalf("newRecordSealer: %v", err)
	}
	b, err := newRecordSealer(sk)
	if err != nil {
		t.Fatalf("newRecordSealer: %v", err)
	}
	key := bytes.Repeat([]byte{0x5a}, 32)
	sealed, err := a.seal(key, sealAAD("g", 0, true))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	got, err := b.open(sealed, sealAAD("g", 0, true))
	if err != nil {
		t.Fatalf("a second sealer from the same key could not open it: %v", err)
	}
	if !bytes.Equal(got, key) {
		t.Error("round trip returned different bytes")
	}
}
