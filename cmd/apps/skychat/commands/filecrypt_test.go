// Package commands cmd/apps/skychat/commands/filecrypt_test.go c4-app-chat
//
// The attachment container: that it round-trips at every size boundary,
// that the plaintext is genuinely gone from the bytes on disk, that a
// tampered or truncated file fails loudly instead of decoding to garbage,
// and that the decrypted view still seeks — the property HTTP Range
// requests (video scrubbing) depend on.
package commands

import (
	"bytes"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testFileKey(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

// sealBytes writes plaintext through the container and returns the sealed
// path.
func sealBytes(t *testing.T, plaintext []byte, key []byte) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	if err := os.WriteFile(src, plaintext, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	dst := filepath.Join(dir, "sealed.bin")
	if err := sealFileTo(src, dst, "gid-1", "fid-1", "holiday.png", key); err != nil {
		t.Fatalf("sealFileTo: %v", err)
	}
	return dst
}

func readAllSealed(t *testing.T, path string, keys [][]byte) []byte {
	t.Helper()
	rs, err := openSealedFile(path, keys)
	if err != nil {
		t.Fatalf("openSealedFile: %v", err)
	}
	defer func() { _ = rs.Close() }() //nolint:errcheck
	out, err := io.ReadAll(rs)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return out
}

// Sizes chosen around the chunk boundary, where an off-by-one in the
// final-chunk bookkeeping hides: empty, short, exactly one chunk, one byte
// over, and several chunks with a partial tail.
func TestSealedAttachmentRoundTripsAtChunkBoundaries(t *testing.T) {
	key := testFileKey(0x11)
	for _, size := range []int{0, 1, 100, chunkPlainSize - 1, chunkPlainSize, chunkPlainSize + 1, 3*chunkPlainSize + 7} {
		plain := make([]byte, size)
		if _, err := rand.Read(plain); err != nil {
			t.Fatalf("rand: %v", err)
		}
		path := sealBytes(t, plain, key)
		got := readAllSealed(t, path, [][]byte{key})
		if !bytes.Equal(got, plain) {
			t.Fatalf("size %d: round trip mismatch (got %d bytes)", size, len(got))
		}
	}
}

// The point of the whole exercise: what lands on disk is not the file.
func TestSealedAttachmentDoesNotContainThePlaintext(t *testing.T) {
	key := testFileKey(0x22)
	plain := []byte("the quick brown fox jumps over the lazy dog, repeatedly and identifiably")
	path := sealBytes(t, plain, key)

	onDisk, err := os.ReadFile(path) //nolint:gosec // test-local path
	if err != nil {
		t.Fatalf("read sealed: %v", err)
	}
	if bytes.Contains(onDisk, plain) {
		t.Error("the sealed file contains its plaintext verbatim")
	}
	if bytes.Contains(onDisk, []byte("quick brown fox")) {
		t.Error("the sealed file leaks a plaintext fragment")
	}
	// The header is deliberately readable — that is what makes the file
	// self-describing — so the ids and the display name are there.
	hdr, err := sealedFileHeader(path)
	if err != nil {
		t.Fatalf("sealedFileHeader: %v", err)
	}
	if hdr.GroupID != "gid-1" || hdr.FileID != "fid-1" || hdr.Name != "holiday.png" {
		t.Errorf("header = %+v", hdr)
	}
	if hdr.PlainSize != int64(len(plain)) {
		t.Errorf("declared size = %d, want %d", hdr.PlainSize, len(plain))
	}
}

func TestSealedAttachmentRejectsTheWrongKey(t *testing.T) {
	path := sealBytes(t, []byte("secret"), testFileKey(0x33))
	if _, err := openSealedFile(path, [][]byte{testFileKey(0x44)}); err == nil {
		t.Error("a wrong key opened the attachment")
	}
	// The ring case: the right key anywhere in the list is enough, which is
	// what keeps an attachment readable across a key rotation.
	rs, err := openSealedFile(path, [][]byte{testFileKey(0x44), testFileKey(0x33)})
	if err != nil {
		t.Fatalf("a key later in the ring should still open it: %v", err)
	}
	_ = rs.Close() //nolint:errcheck
}

// Each of these is a way a file could be altered on disk or in flight; all
// of them have to fail the tag rather than produce plausible bytes.
func TestSealedAttachmentDetectsTampering(t *testing.T) {
	key := testFileKey(0x55)
	plain := bytes.Repeat([]byte("A"), 3*chunkPlainSize)

	for name, mangle := range map[string]func(b []byte) []byte{
		"body byte flipped": func(b []byte) []byte {
			b[len(b)-20] ^= 0xff
			return b
		},
		"header size edited": func(b []byte) []byte {
			// The declared size sits in the last 8 bytes of the header,
			// which ends where the first nonce begins.
			hdrLen := len(sealedFileMagic) + 1 + len("gid-1") + 1 + len("fid-1") + 2 + len("holiday.png") + 8
			b[hdrLen-1] ^= 0x01
			return b
		},
		"final chunk truncated": func(b []byte) []byte {
			return b[:len(b)-chunkWireSize/2]
		},
		"chunks swapped": func(b []byte) []byte {
			hdrLen := len(sealedFileMagic) + 1 + len("gid-1") + 1 + len("fid-1") + 2 + len("holiday.png") + 8
			c0 := append([]byte(nil), b[hdrLen:hdrLen+chunkWireSize]...)
			c1 := append([]byte(nil), b[hdrLen+chunkWireSize:hdrLen+2*chunkWireSize]...)
			copy(b[hdrLen:], c1)
			copy(b[hdrLen+chunkWireSize:], c0)
			return b
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := sealBytes(t, plain, key)
			raw, err := os.ReadFile(path) //nolint:gosec // test-local path
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if err := os.WriteFile(path, mangle(raw), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			rs, oErr := openSealedFile(path, [][]byte{key})
			if oErr != nil {
				return // rejected at open — the usual case
			}
			// Opened (the first chunk survived), so the damage has to
			// surface while reading the rest.
			defer func() { _ = rs.Close() }() //nolint:errcheck
			if got, rErr := io.ReadAll(rs); rErr == nil && bytes.Equal(got, plain) {
				t.Error("a tampered attachment read back as the original")
			}
		})
	}
}

// A plaintext file is not an error — it is a DM attachment, a public-group
// one, or one from before sealing existed, and it must be served as-is.
func TestPlaintextFileIsReportedAsNotSealed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain.txt")
	if err := os.WriteFile(path, []byte("just a file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := sealedFileHeader(path); err != errNotSealed { //nolint:errorlint // sentinel returned directly
		t.Errorf("sealedFileHeader on a plaintext file = %v, want errNotSealed", err)
	}
	// A short file (fewer bytes than the magic) is also just a file.
	short := filepath.Join(t.TempDir(), "tiny")
	if err := os.WriteFile(short, []byte("ab"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := sealedFileHeader(short); err != errNotSealed { //nolint:errorlint
		t.Errorf("sealedFileHeader on a 2-byte file = %v, want errNotSealed", err)
	}
}

// Seeking is what makes <video> scrubbing work: the browser asks for a
// byte range and http.ServeContent seeks into the reader. Whole-file
// decryption would have quietly broken this.
func TestSealedAttachmentServesRanges(t *testing.T) {
	key := testFileKey(0x66)
	plain := make([]byte, 5*chunkPlainSize+1234)
	if _, err := rand.Read(plain); err != nil {
		t.Fatalf("rand: %v", err)
	}
	path := sealBytes(t, plain, key)

	// Direct seek arithmetic, including a seek that lands mid-chunk and one
	// past the last full chunk.
	rs, err := openSealedFile(path, [][]byte{key})
	if err != nil {
		t.Fatalf("openSealedFile: %v", err)
	}
	defer func() { _ = rs.Close() }() //nolint:errcheck
	if rs.Size() != int64(len(plain)) {
		t.Fatalf("Size() = %d, want %d", rs.Size(), len(plain))
	}
	for _, off := range []int64{0, 1, chunkPlainSize - 5, chunkPlainSize, 4*chunkPlainSize + 77, int64(len(plain)) - 3} {
		if _, err := rs.Seek(off, io.SeekStart); err != nil {
			t.Fatalf("seek %d: %v", off, err)
		}
		buf := make([]byte, 3)
		n, rErr := io.ReadFull(rs, buf)
		if rErr != nil && rErr != io.ErrUnexpectedEOF && rErr != io.EOF { //nolint:errorlint
			t.Fatalf("read at %d: %v", off, rErr)
		}
		if !bytes.Equal(buf[:n], plain[off:off+int64(n)]) {
			t.Errorf("bytes at offset %d differ", off)
		}
	}

	// And through ServeContent, the way the browser actually asks.
	if _, err := rs.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/files/clip.mp4", nil)
	req.Header.Set("Range", "bytes=1000-1999")
	rec := httptest.NewRecorder()
	http.ServeContent(rec, req, "clip.mp4", time.Now(), rs)
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", rec.Code)
	}
	if got := rec.Body.Bytes(); !bytes.Equal(got, plain[1000:2000]) {
		t.Errorf("range body differs (got %d bytes)", len(got))
	}
}
