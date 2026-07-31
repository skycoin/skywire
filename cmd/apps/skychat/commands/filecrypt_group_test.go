// Package commands cmd/apps/skychat/commands/filecrypt_group_test.go c4-app-chat
//
// The end-to-end shape of a group attachment: published sealed, stored
// sealed, served decrypted to the local UI, and refused rather than served
// as ciphertext when this visor has no key for it.
package commands

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/cmd/apps/skychat/group"
)

// THE property. Before this, the served copy on the sender's disk — and
// therefore the copy every member pulled through file-backfill — was the
// file itself, in the clear, next to a chat history whose text was sealed.
func TestGroupAttachmentIsSealedOnDiskAndServedDecrypted(t *testing.T) {
	key := testFileKey(0x77)
	fake := &groupAPI{fileKey: key}
	withFakePairRPC(t, fake)

	plain := []byte("PNGDATA-recognizable-plaintext-bytes")
	src := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(src, plain, 0o600); err != nil {
		t.Fatal(err)
	}

	fileID, url, err := sendFileToVisorGroup(context.Background(), "gid-9", src, "photo.png")
	if err != nil {
		t.Fatalf("sendFileToVisorGroup: %v", err)
	}
	dir, err := downloadsDir()
	if err != nil {
		t.Fatal(err)
	}
	served := filepath.Join(dir, fileID+".png")
	t.Cleanup(func() { _ = os.Remove(served) }) //nolint:errcheck

	// On disk: a sealed container, not the file.
	raw, err := os.ReadFile(served) //nolint:gosec // test-local path
	if err != nil {
		t.Fatalf("served copy missing: %v", err)
	}
	if bytes.Contains(raw, plain) {
		t.Fatal("the group attachment is stored in the clear")
	}
	hdr, err := sealedFileHeader(served)
	if err != nil {
		t.Fatalf("served copy is not a sealed container: %v", err)
	}
	if hdr.GroupID != "gid-9" || hdr.FileID != fileID || hdr.Name != "photo.png" {
		t.Errorf("container header = %+v", hdr)
	}
	if hdr.PlainSize != int64(len(plain)) {
		t.Errorf("declared size = %d, want %d", hdr.PlainSize, len(plain))
	}
	// The key was asked for by group + file id, which is what scopes it.
	if len(fake.fileKeyCalls) == 0 || fake.fileKeyCalls[0].ID != "gid-9" || fake.fileKeyCalls[0].FileID != fileID {
		t.Errorf("GroupFileKey calls = %+v", fake.fileKeyCalls)
	}

	// Served to the local UI: the file, decrypted, at the URL the sender
	// was handed for its own optimistic render.
	rec := httptest.NewRecorder()
	downloadFileHandler(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", url, rec.Code)
	}
	if got := rec.Body.Bytes(); !bytes.Equal(got, plain) {
		t.Errorf("served body = %q, want the plaintext", got)
	}
}

// A visor that holds no key for the group — one that left, or that never
// received the epoch the file was sealed under — must be told so, not
// handed ciphertext that renders as a broken image.
func TestGroupAttachmentWithoutAKeyIsRefusedNotServed(t *testing.T) {
	key := testFileKey(0x88)
	fake := &groupAPI{fileKey: key}
	withFakePairRPC(t, fake)

	src := filepath.Join(t.TempDir(), "doc.pdf")
	if err := os.WriteFile(src, []byte("sensitive"), 0o600); err != nil {
		t.Fatal(err)
	}
	fileID, url, err := sendFileToVisorGroup(context.Background(), "gid-x", src, "doc.pdf")
	if err != nil {
		t.Fatalf("sendFileToVisorGroup: %v", err)
	}
	dir, _ := downloadsDir()                                               //nolint:errcheck
	t.Cleanup(func() { _ = os.Remove(filepath.Join(dir, fileID+".pdf")) }) //nolint:errcheck

	// The group's key is gone from this visor's point of view.
	fake.fileKey = nil

	rec := httptest.NewRecorder()
	downloadFileHandler(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET %s = %d, want 403", url, rec.Code)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("sensitive")) {
		t.Error("the refusal leaked the file")
	}
}

// A public group has no key at all: its attachments are stored and served
// exactly as before, which is the same call the group makes about message
// bodies.
func TestPublicGroupAttachmentStaysPlaintext(t *testing.T) {
	fake := &groupAPI{} // no key → plaintext group
	withFakePairRPC(t, fake)

	plain := []byte("open group, open bytes")
	src := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(src, plain, 0o600); err != nil {
		t.Fatal(err)
	}
	fileID, url, err := sendFileToVisorGroup(context.Background(), "gid-pub", src, "note.txt")
	if err != nil {
		t.Fatalf("sendFileToVisorGroup: %v", err)
	}
	dir, _ := downloadsDir() //nolint:errcheck
	served := filepath.Join(dir, fileID+".txt")
	t.Cleanup(func() { _ = os.Remove(served) }) //nolint:errcheck

	raw, err := os.ReadFile(served) //nolint:gosec // test-local path
	if err != nil {
		t.Fatalf("served copy missing: %v", err)
	}
	if !bytes.Equal(raw, plain) {
		t.Errorf("public-group copy = %q, want the file unchanged", raw)
	}
	rec := httptest.NewRecorder()
	downloadFileHandler(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), plain) {
		t.Errorf("GET %s = %d %q", url, rec.Code, rec.Body.Bytes())
	}
}

// Rotation behavior, which is where a per-file key could have gone wrong
// in two opposite directions: an attachment shared before a re-key must
// stay readable for the members who lived through it, and must NOT become
// readable to a joiner that was handed only the current key.
func TestGroupAttachmentAcrossAKeyRotation(t *testing.T) {
	gid := uuid.NewString()
	fileID := "file-rot-1"
	epoch0 := bytes.Repeat([]byte{0xc0}, 32)
	epoch1 := bytes.Repeat([]byte{0xc1}, 32)

	// Sealed while the group was on epoch 0.
	before := group.Record{ID: gid, Mode: group.ModePrivate, Kind: group.KindPrivate, AESKey: epoch0}
	seal, _, err := before.FileKeys(fileID)
	if err != nil {
		t.Fatalf("FileKeys: %v", err)
	}
	plain := []byte("shared before the re-key")
	dir := t.TempDir()
	src := filepath.Join(dir, "before.txt")
	if err := os.WriteFile(src, plain, 0o600); err != nil {
		t.Fatal(err)
	}
	sealedPath := filepath.Join(dir, "before.sealed")
	if err := sealFileTo(src, sealedPath, gid, fileID, "before.txt", seal); err != nil {
		t.Fatalf("sealFileTo: %v", err)
	}

	// A member who lived through the rotation keeps the retired key in its
	// ring, so the attachment still opens.
	stayed := group.Record{
		ID: gid, Mode: group.ModePrivate, Kind: group.KindPrivate,
		AESKey: epoch1, KeyEpoch: 1,
		KeyRing: []group.GroupKey{{Epoch: 0, Key: epoch0}},
	}
	_, open, err := stayed.FileKeys(fileID)
	if err != nil {
		t.Fatalf("FileKeys after rotation: %v", err)
	}
	got := readAllSealed(t, sealedPath, open)
	if !bytes.Equal(got, plain) {
		t.Error("an attachment stopped opening after the group re-keyed")
	}

	// A joiner admitted after the rotation holds only the current key — the
	// same boundary admission already draws for message history.
	joiner := group.Record{ID: gid, Mode: group.ModePrivate, Kind: group.KindPrivate, AESKey: epoch1, KeyEpoch: 1}
	_, joinerKeys, err := joiner.FileKeys(fileID)
	if err != nil {
		t.Fatalf("FileKeys for joiner: %v", err)
	}
	if _, err := openSealedFile(sealedPath, joinerKeys); err == nil {
		t.Error("a post-rotation joiner opened an attachment from before it arrived")
	}
}

// The receiver's copy keeps the SENDER's original filename — there is no id
// in it anywhere. Serving it has to work anyway, which is the whole reason
// the container carries its own group + file id.
func TestReceivedGroupAttachmentIsServedByHeaderNotFilename(t *testing.T) {
	key := testFileKey(0x99)
	fake := &groupAPI{fileKey: key}
	withFakePairRPC(t, fake)

	plain := []byte("bytes that arrived over xfer")
	src := filepath.Join(t.TempDir(), "src.bin")
	if err := os.WriteFile(src, plain, 0o600); err != nil {
		t.Fatal(err)
	}
	dir, err := downloadsDir()
	if err != nil {
		t.Fatal(err)
	}
	// What acceptInbound would have written: the sealed bytes off the wire,
	// under safeFileName(offer.Name, offer.ID).
	received := filepath.Join(dir, "holiday snap.png")
	if err := sealFileTo(src, received, "gid-recv", "fid-recv", "holiday snap.png", key); err != nil {
		t.Fatalf("sealFileTo: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(received) }) //nolint:errcheck

	rec := httptest.NewRecorder()
	downloadFileHandler(rec, httptest.NewRequest(http.MethodGet, "/files/holiday%20snap.png", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d, want 200", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), plain) {
		t.Errorf("served body = %q", rec.Body.Bytes())
	}
	// The key was requested for the ids in the HEADER, not anything guessed
	// from the path.
	if len(fake.fileKeyCalls) == 0 || fake.fileKeyCalls[0].ID != "gid-recv" || fake.fileKeyCalls[0].FileID != "fid-recv" {
		t.Errorf("GroupFileKey calls = %+v", fake.fileKeyCalls)
	}
}
