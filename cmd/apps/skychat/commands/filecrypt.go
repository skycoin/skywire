// Package commands cmd/apps/skychat/commands/filecrypt.go c4-app-chat
//
// The on-disk and on-the-wire container for a group attachment, sealed
// under a per-file key derived from the group key (see the group package's
// filekey.go for the derivation and why the group key itself never leaves
// the visor).
//
// # Why a container and not "encrypt the bytes"
//
// A group attachment has to survive three things a plain AES blob does
// not:
//
//   - It is SERVED to the local browser at /files/<name>, and the UI plays
//     video and audio out of it. That means HTTP Range requests, which
//     means the decrypted view has to be seekable — an all-at-once
//     decrypt would either break seeking or pull a 35 MB clip into memory
//     on every scrub.
//   - It is RE-SENT verbatim by the backfill path. The holder ships the
//     bytes it stored without opening them, so the container must be
//     self-describing: whoever ends up with it has to know which group's
//     key opens it without being told out of band.
//   - It must fail loudly on tampering rather than yield plausible
//     garbage into an image decoder.
//
// So: fixed-size plaintext chunks, each its own AES-256-GCM message.
//
//	"SGF1" | u8 len | group id | u8 len | file id | u16 len | name | u64 plaintext size | chunk*
//	chunk = 12-byte nonce | GCM(chunk plaintext) + 16-byte tag
//
// The group id and file id in the header are what "self-describing"
// means concretely: the key is derived from exactly that pair, so a file
// found on disk — or re-sent to a member who has never seen its feed
// message — carries everything needed to ask for the right key. Deriving
// them from the FILENAME instead was the obvious shortcut and does not
// work: a received copy keeps the sender's original name, which contains
// no id at all.
//
// Every chunk is sealed with the whole header as additional data, plus its
// index and a final-chunk flag. That binds three things at once: the
// declared size and group cannot be edited (the tags stop verifying),
// chunks cannot be reordered or swapped between files, and the file cannot
// be truncated silently — the last chunk says it is the last one, so a cut
// tail surfaces as a missing final chunk rather than as a short read.
//
// Plaintext size in the header is what makes the seek arithmetic direct:
// plaintext offset N lives in chunk N/chunkPlainSize at a fixed ciphertext
// offset, so a Range request decrypts one chunk instead of the file.
//
// # What it does not hide
//
// The attachment's NAME and SIZE. Both ride the xfer offer in the clear so
// the receiver can name the file on disk, and both are in the group
// message that references the file anyway — sealed there, but a member who
// asks for the bytes has already read it. The size is also inherent: the
// container is a constant factor larger than the plaintext. Hiding either
// means padding and an anonymous-id save path, which is a bigger change
// than this and buys little while the reference itself is already sealed.
package commands

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// sealedFileMagic marks a sealed attachment. Ends in a format digit: a
// future layout takes SGF2, and this one is rejected rather than
// misparsed.
var sealedFileMagic = []byte("SGF1")

const (
	// chunkPlainSize is the plaintext bytes per AEAD chunk. 64 KiB keeps
	// the per-chunk overhead (28 bytes of nonce+tag, 0.04%) negligible
	// while bounding a Range request's decrypt work to one chunk — a seek
	// into a video decrypts 64 KiB, not the file.
	chunkPlainSize = 64 << 10

	// sealedNonceLen / sealedTagLen are AES-GCM's fixed widths.
	sealedNonceLen = 12
	sealedTagLen   = 16

	// chunkWireSize is one full chunk on disk at chunkPlainSize.
	chunkWireSize = sealedNonceLen + chunkPlainSize + sealedTagLen

	// maxSealedIDLen bounds each id in the header so a corrupt or hostile
	// header cannot make the parser allocate. A group id is a UUID string
	// (36 bytes) and a file id is 16 hex chars; the cap leaves room
	// without being open-ended.
	maxSealedIDLen = 64

	// maxSealedNameLen bounds the stored display name.
	maxSealedNameLen = 512
)

// errNotSealed reports that a file does not carry the container framing —
// a plaintext attachment (a DM file, a public-group file, or one sent by a
// build that predates sealing). Callers treat it as "serve as-is", not as
// an error.
var errNotSealed = errors.New("skychat: not a sealed attachment")

// sealedHeader is the parsed container header.
type sealedHeader struct {
	GroupID   string
	FileID    string
	Name      string // original display name, "" when the sender stored none
	PlainSize int64
	HeaderLen int    // bytes of header, i.e. where chunk 0 starts
	RawHeader []byte // exact header bytes, used as AEAD additional data
}

// buildSealedHeader renders the header for one group attachment.
func buildSealedHeader(groupID, fileID, name string, plainSize int64) ([]byte, error) {
	if len(groupID) > maxSealedIDLen || len(fileID) > maxSealedIDLen {
		return nil, fmt.Errorf("skychat: seal: ids exceed %d bytes", maxSealedIDLen)
	}
	if fileID == "" {
		return nil, fmt.Errorf("skychat: seal: file id required")
	}
	if len(name) > maxSealedNameLen {
		name = name[:maxSealedNameLen]
	}
	if plainSize < 0 {
		return nil, fmt.Errorf("skychat: seal: negative size %d", plainSize)
	}
	out := make([]byte, 0, len(sealedFileMagic)+1+len(groupID)+1+len(fileID)+2+len(name)+8)
	out = append(out, sealedFileMagic...)
	out = append(out, byte(len(groupID))) //nolint:gosec // both ids are length-checked against maxSealedIDLen above
	out = append(out, groupID...)
	out = append(out, byte(len(fileID))) //nolint:gosec // ditto
	out = append(out, fileID...)
	var nl [2]byte
	binary.BigEndian.PutUint16(nl[:], uint16(len(name))) //nolint:gosec // clamped above
	out = append(out, nl[:]...)
	out = append(out, name...)
	var sz [8]byte
	binary.BigEndian.PutUint64(sz[:], uint64(plainSize)) //nolint:gosec // guarded non-negative above
	return append(out, sz[:]...), nil
}

// parseSealedHeader reads the header from r. Returns errNotSealed when the
// magic is absent, which is the ordinary "this is a plaintext file" path.
func parseSealedHeader(r io.Reader) (sealedHeader, error) {
	magic := make([]byte, len(sealedFileMagic))
	if _, err := io.ReadFull(r, magic); err != nil {
		return sealedHeader{}, errNotSealed
	}
	for i := range magic {
		if magic[i] != sealedFileMagic[i] {
			return sealedHeader{}, errNotSealed
		}
	}
	// Everything past the magic is a real parse: a file that claims the
	// format and then fails to hold it is corrupt, not plaintext.
	raw := append([]byte(nil), magic...)
	readStr := func(maxLen int, what string) (string, error) {
		var lb [1]byte
		if _, err := io.ReadFull(r, lb[:]); err != nil {
			return "", fmt.Errorf("skychat: sealed attachment: truncated %s length: %w", what, err)
		}
		n := int(lb[0])
		if n > maxLen {
			return "", fmt.Errorf("skychat: sealed attachment: %s length %d exceeds %d", what, n, maxLen)
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", fmt.Errorf("skychat: sealed attachment: truncated %s: %w", what, err)
		}
		raw = append(raw, lb[0])
		raw = append(raw, buf...)
		return string(buf), nil
	}
	gid, err := readStr(maxSealedIDLen, "group id")
	if err != nil {
		return sealedHeader{}, err
	}
	fid, err := readStr(maxSealedIDLen, "file id")
	if err != nil {
		return sealedHeader{}, err
	}
	var nl [2]byte
	if _, err := io.ReadFull(r, nl[:]); err != nil {
		return sealedHeader{}, fmt.Errorf("skychat: sealed attachment: truncated name length: %w", err)
	}
	nameLen := int(binary.BigEndian.Uint16(nl[:]))
	if nameLen > maxSealedNameLen {
		return sealedHeader{}, fmt.Errorf("skychat: sealed attachment: name length %d exceeds %d", nameLen, maxSealedNameLen)
	}
	name := make([]byte, nameLen)
	if _, err := io.ReadFull(r, name); err != nil {
		return sealedHeader{}, fmt.Errorf("skychat: sealed attachment: truncated name: %w", err)
	}
	raw = append(raw, nl[:]...)
	raw = append(raw, name...)

	var sz [8]byte
	if _, err := io.ReadFull(r, sz[:]); err != nil {
		return sealedHeader{}, fmt.Errorf("skychat: sealed attachment: truncated size: %w", err)
	}
	size := binary.BigEndian.Uint64(sz[:])
	if size > 1<<62 {
		return sealedHeader{}, fmt.Errorf("skychat: sealed attachment: implausible size %d", size)
	}
	raw = append(raw, sz[:]...)
	return sealedHeader{
		GroupID:   gid,
		FileID:    fid,
		Name:      string(name),
		PlainSize: int64(size), //nolint:gosec // bounded above
		HeaderLen: len(raw),
		RawHeader: raw,
	}, nil
}

// chunkAAD is the additional data every chunk is sealed with: the whole
// header, the chunk index, and whether this is the last chunk. Binding all
// three is what makes reorder, cross-file splice, header edits and silent
// truncation all fail the tag check.
func chunkAAD(header []byte, index uint64, final bool) []byte {
	aad := make([]byte, 0, len(header)+9)
	aad = append(aad, header...)
	var idx [8]byte
	binary.BigEndian.PutUint64(idx[:], index)
	aad = append(aad, idx[:]...)
	if final {
		aad = append(aad, 1)
	} else {
		aad = append(aad, 0)
	}
	return aad
}

// newGCM builds the AEAD for a 32-byte file key.
func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("skychat: attachment key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("skychat: attachment aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("skychat: attachment gcm: %w", err)
	}
	return gcm, nil
}

// sealFileTo writes the sealed container for srcPath to dstPath.
//
// A zero-length source still produces one (empty, final) chunk rather than
// a header alone, so "the file ended" is always an authenticated fact
// rather than the absence of one.
func sealFileTo(srcPath, dstPath, groupID, fileID, name string, key []byte) error {
	gcm, err := newGCM(key)
	if err != nil {
		return err
	}
	src, err := os.Open(srcPath) //nolint:gosec // caller-supplied local source
	if err != nil {
		return fmt.Errorf("skychat: seal: open source: %w", err)
	}
	defer func() { _ = src.Close() }() //nolint:errcheck
	fi, err := src.Stat()
	if err != nil {
		return fmt.Errorf("skychat: seal: stat source: %w", err)
	}
	header, err := buildSealedHeader(groupID, fileID, name, fi.Size())
	if err != nil {
		return err
	}
	dst, err := os.Create(dstPath) //nolint:gosec // dst rooted in downloadsDir by callers
	if err != nil {
		return fmt.Errorf("skychat: seal: create destination: %w", err)
	}
	if err := sealStream(src, dst, header, gcm); err != nil {
		_ = dst.Close()        //nolint:errcheck
		_ = os.Remove(dstPath) //nolint:errcheck // never leave a half-sealed file behind
		return err
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(dstPath) //nolint:errcheck
		return fmt.Errorf("skychat: seal: close destination: %w", err)
	}
	return nil
}

// sealStream is sealFileTo's body, split out so tests can drive it without
// touching disk.
func sealStream(src io.Reader, dst io.Writer, header []byte, gcm cipher.AEAD) error {
	if _, err := dst.Write(header); err != nil {
		return fmt.Errorf("skychat: seal: write header: %w", err)
	}
	buf := make([]byte, chunkPlainSize)
	nonce := make([]byte, sealedNonceLen)
	var index uint64
	for {
		n, rErr := io.ReadFull(src, buf)
		// A short read means this is the tail: seal what we have as the
		// final chunk. Anything else is a real read failure.
		final := rErr == io.EOF || rErr == io.ErrUnexpectedEOF
		if rErr != nil && !final {
			return fmt.Errorf("skychat: seal: read source: %w", rErr)
		}
		if _, err := rand.Read(nonce); err != nil {
			return fmt.Errorf("skychat: seal: nonce: %w", err)
		}
		out := make([]byte, sealedNonceLen, sealedNonceLen+n+sealedTagLen)
		copy(out, nonce)
		out = gcm.Seal(out, nonce, buf[:n], chunkAAD(header, index, final))
		if _, err := dst.Write(out); err != nil {
			return fmt.Errorf("skychat: seal: write chunk: %w", err)
		}
		if final {
			return nil
		}
		index++
	}
}

// sealedFileReader is a seekable, decrypting view of a sealed container.
// It implements io.ReadSeeker over the PLAINTEXT, so http.ServeContent
// serves ranges out of it exactly as it would from the original file, and
// an image decoder can rewind it.
//
// Not safe for concurrent use — each request opens its own.
type sealedFileReader struct {
	f      *os.File
	gcm    cipher.AEAD
	hdr    sealedHeader
	pos    int64 // plaintext offset
	chunk  []byte
	chunkI int64 // which chunk `chunk` holds; -1 when empty
}

// openSealedFile opens path and returns a plaintext view of it, trying
// each candidate key in turn (current epoch first, then the ring).
//
// Returns errNotSealed for a plaintext file, so the caller can serve it
// unchanged.
func openSealedFile(path string, keys [][]byte) (*sealedFileReader, error) {
	f, err := os.Open(path) //nolint:gosec // callers pass a sanitized downloads-dir path
	if err != nil {
		return nil, err
	}
	hdr, err := parseSealedHeader(f)
	if err != nil {
		_ = f.Close() //nolint:errcheck
		return nil, err
	}
	// Trial-decrypt the first chunk with each key. AES-GCM authenticates,
	// so a wrong key fails the tag rather than yielding garbage — the same
	// property that lets group message bodies walk the key ring.
	for _, k := range keys {
		gcm, gErr := newGCM(k)
		if gErr != nil {
			continue
		}
		r := &sealedFileReader{f: f, gcm: gcm, hdr: hdr, chunkI: -1}
		if _, rErr := r.loadChunk(0); rErr != nil {
			continue
		}
		// The tail is checked up front, not lazily. A file whose last
		// chunk is missing or damaged still reads back as complete
		// otherwise — every data chunk it does have is authentic, and the
		// final chunk of an exactly-chunk-aligned file carries no
		// plaintext at all, so nothing would ever touch it. Verifying it
		// here is what makes "the attachment is whole" a checked fact
		// rather than an inference. Costs one extra chunk decrypt.
		if last := r.chunkCount() - 1; last > 0 {
			if _, rErr := r.loadChunk(last); rErr != nil {
				continue
			}
			// Restore the front-of-file cache the first Read will want.
			if _, rErr := r.loadChunk(0); rErr != nil {
				continue
			}
		}
		return r, nil
	}
	_ = f.Close() //nolint:errcheck
	return nil, fmt.Errorf("skychat: sealed attachment %s: no group key opens it or it is incomplete", path)
}

// chunkCount is how many chunks the container holds.
//
// Note the +1 and the absence of any rounding: the writer only learns it
// has reached the end by reading short, so a file whose size is an exact
// multiple of the chunk size ends with an EMPTY final chunk. That empty
// chunk is not padding to be optimized away — it is the authenticated
// statement "this is the end", and dropping it from the count made the
// reader treat the last full chunk as final, compute a different AAD, and
// fail the tag on every exactly-aligned file.
func (r *sealedFileReader) chunkCount() int64 {
	return r.hdr.PlainSize/chunkPlainSize + 1
}

// loadChunk decrypts chunk i into r.chunk.
func (r *sealedFileReader) loadChunk(i int64) ([]byte, error) {
	if r.chunkI == i && r.chunk != nil {
		return r.chunk, nil
	}
	count := r.chunkCount()
	if i < 0 || i >= count {
		return nil, io.EOF
	}
	final := i == count-1
	plainLen := int64(chunkPlainSize)
	if final {
		plainLen = r.hdr.PlainSize - i*chunkPlainSize
		if plainLen < 0 {
			plainLen = 0
		}
	}
	wire := make([]byte, sealedNonceLen+plainLen+sealedTagLen)
	off := int64(r.hdr.HeaderLen) + i*chunkWireSize
	if _, err := r.f.ReadAt(wire, off); err != nil {
		return nil, fmt.Errorf("skychat: sealed attachment: read chunk %d: %w", i, err)
	}
	pt, err := r.gcm.Open(nil, wire[:sealedNonceLen], wire[sealedNonceLen:], chunkAAD(r.hdr.RawHeader, uint64(i), final)) //nolint:gosec // i >= 0 checked above
	if err != nil {
		return nil, fmt.Errorf("skychat: sealed attachment: chunk %d failed authentication: %w", i, err)
	}
	r.chunk, r.chunkI = pt, i
	return pt, nil
}

// Read implements io.Reader over the plaintext.
func (r *sealedFileReader) Read(p []byte) (int, error) {
	if r.pos >= r.hdr.PlainSize {
		return 0, io.EOF
	}
	i := r.pos / chunkPlainSize
	chunk, err := r.loadChunk(i)
	if err != nil {
		return 0, err
	}
	within := r.pos - i*chunkPlainSize
	if within >= int64(len(chunk)) {
		return 0, io.EOF
	}
	n := copy(p, chunk[within:])
	r.pos += int64(n)
	return n, nil
}

// Seek implements io.Seeker over the plaintext. Seeking past the end is
// allowed (matching *os.File) and simply reads zero bytes.
func (r *sealedFileReader) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = r.pos + offset
	case io.SeekEnd:
		abs = r.hdr.PlainSize + offset
	default:
		return 0, fmt.Errorf("skychat: sealed attachment: invalid whence %d", whence)
	}
	if abs < 0 {
		return 0, errors.New("skychat: sealed attachment: negative seek")
	}
	r.pos = abs
	return abs, nil
}

// Size is the plaintext length, for Content-Length / ServeContent.
func (r *sealedFileReader) Size() int64 { return r.hdr.PlainSize }

// Header exposes the parsed container header: which group and file this
// is, and the display name the sender stored. What makes an attachment
// found on disk answerable without any other lookup.
func (r *sealedFileReader) Header() sealedHeader { return r.hdr }

// Close releases the underlying file.
func (r *sealedFileReader) Close() error { return r.f.Close() }

// sealedFileHeader peeks at a file's header without decrypting anything.
// Returns errNotSealed for a plaintext file.
func sealedFileHeader(path string) (sealedHeader, error) {
	f, err := os.Open(path) //nolint:gosec // callers pass a sanitized downloads-dir path
	if err != nil {
		return sealedHeader{}, err
	}
	defer func() { _ = f.Close() }() //nolint:errcheck
	return parseSealedHeader(f)
}
