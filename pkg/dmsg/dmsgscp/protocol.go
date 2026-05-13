// Package dmsgscp pkg/dmsg/dmsgscp/protocol.go: wire-protocol
// header parsing + serialization, plus the tiny ack helpers that
// drive the framing. The protocol mirrors OpenSSH's scp on-wire
// format — a single header line per record, a single ack byte after
// every header and every payload, and an in-band error prefix
// (\x01 warning, \x02 fatal) for human-readable failures.
//
// We only implement records the dmsgscp Host/Client actually use:
//
//	C<octal-mode> <size> <name>\n  - file record (followed by `size`
//	                                 bytes of payload + ack)
//	D<octal-mode> 0 <dirname>\n    - start of recursion (v1 rejects)
//	E\n                            - end of recursion (v1 rejects)
//
// Path safety: the parser rejects any `..` component, absolute
// paths, embedded NUL/newline, and names beyond MaxNameLen — these
// are all explicit traversal/DoS guards rather than passive checks.
// Callers still resolve the final path against the host's rootDir
// via ResolveSafePath before touching the filesystem.
package dmsgscp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// RecordType identifies a parsed protocol record.
type RecordType byte

// Supported record types.
const (
	// RecordFile is a `C` (file) record.
	RecordFile RecordType = 'C'
	// RecordDirStart is a `D` (start-of-directory) record. v1 rejects.
	RecordDirStart RecordType = 'D'
	// RecordDirEnd is an `E` (end-of-directory) record. v1 rejects.
	RecordDirEnd RecordType = 'E'
)

// Header is one parsed protocol record. For RecordDirEnd, only Type
// is meaningful; Mode/Size/Name are zero.
type Header struct {
	Type RecordType
	Mode os.FileMode // permission bits parsed from the octal field
	Size int64       // payload size in bytes (0 for D/E)
	Name string      // basename, validated against traversal
}

// Sentinel parser errors. Callers can use errors.Is to react to a
// specific failure mode (e.g. send a fatal-prefix line on the wire
// after ErrPathTraversal).
var (
	// ErrShortHeader signals a header line shorter than the minimum
	// valid form (`C0 0 x`).
	ErrShortHeader = errors.New("dmsgscp: header too short")
	// ErrHeaderTooLong signals a header line above MaxHeaderLen.
	ErrHeaderTooLong = errors.New("dmsgscp: header line exceeds max length")
	// ErrUnknownRecord signals a leading byte we don't recognize.
	ErrUnknownRecord = errors.New("dmsgscp: unknown record type")
	// ErrBadMode signals an unparseable octal mode field.
	ErrBadMode = errors.New("dmsgscp: bad octal mode")
	// ErrBadSize signals an unparseable or out-of-range size field.
	ErrBadSize = errors.New("dmsgscp: bad size")
	// ErrEmptyName signals a missing or whitespace-only name.
	ErrEmptyName = errors.New("dmsgscp: empty name")
	// ErrNameTooLong signals a name above MaxNameLen.
	ErrNameTooLong = errors.New("dmsgscp: name exceeds max length")
	// ErrPathTraversal signals a name containing `..` or absolute paths.
	ErrPathTraversal = errors.New("dmsgscp: name contains path traversal")
	// ErrInvalidName signals a name with NUL or newline bytes.
	ErrInvalidName = errors.New("dmsgscp: name contains invalid characters")
	// ErrDirNotSupported is returned when a D/E record arrives — v1
	// is files-only.
	ErrDirNotSupported = errors.New("dmsgscp: directory transfer not implemented in v1")
)

// ReadHeader reads one header record from r. It consumes up to (and
// including) the trailing newline. ErrHeaderTooLong is returned if
// the line would exceed MaxHeaderLen — the caller should treat that
// as fatal and abort the stream.
//
// io.EOF is returned verbatim when the peer cleanly closes before
// sending a header — callers distinguish "no more records" from a
// malformed record by checking for io.EOF.
func ReadHeader(r *bufio.Reader) (Header, error) {
	// ReadSlice rather than ReadString so we can enforce the cap
	// before allocating a string. bufio.Reader.ReadSlice returns
	// bufio.ErrBufferFull if the line exceeds the buffer; we size
	// the buffer at the call site (NewReader uses 4 KiB default,
	// well above MaxHeaderLen) so the cap path triggers via our
	// own length check first.
	line, err := r.ReadSlice('\n')
	if err != nil {
		// A line longer than the buffer surfaces as
		// bufio.ErrBufferFull — translate to our cap error so
		// callers get a consistent classification.
		if errors.Is(err, bufio.ErrBufferFull) {
			return Header{}, ErrHeaderTooLong
		}
		// EOF / unexpected EOF / closed pipe propagate verbatim.
		return Header{}, err
	}
	if len(line) > MaxHeaderLen {
		return Header{}, ErrHeaderTooLong
	}
	// Strip trailing \n (and optional \r for robustness against
	// peers that CRLF their wire).
	line = trimTrailingNewline(line)
	if len(line) == 0 {
		return Header{}, ErrShortHeader
	}

	switch RecordType(line[0]) {
	case RecordDirEnd:
		// `E` is the entire line; anything after is malformed.
		if len(line) != 1 {
			return Header{}, ErrUnknownRecord
		}
		return Header{Type: RecordDirEnd}, nil

	case RecordFile, RecordDirStart:
		return parseFileOrDir(RecordType(line[0]), line[1:])

	default:
		return Header{}, fmt.Errorf("%w: %q", ErrUnknownRecord, string(line[0]))
	}
}

// parseFileOrDir parses the body of a C or D record — the part
// AFTER the leading type byte. Format: `<octal-mode> <size> <name>`.
func parseFileOrDir(typ RecordType, body []byte) (Header, error) {
	// SplitN(3) so a name containing spaces — which scp wire format
	// does allow — survives intact. Real scp uses a single space
	// between the fixed fields and the name; the name itself can
	// contain spaces.
	parts := strings.SplitN(string(body), " ", 3)
	if len(parts) != 3 {
		return Header{}, ErrShortHeader
	}

	// Mode: octal, leading zero by convention. ParseUint handles
	// both "0644" and "644" interchangeably with base 8.
	mode64, err := strconv.ParseUint(parts[0], 8, 32)
	if err != nil {
		return Header{}, fmt.Errorf("%w: %q", ErrBadMode, parts[0])
	}

	// Size: decimal, non-negative.
	size, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return Header{}, fmt.Errorf("%w: %q", ErrBadSize, parts[1])
	}
	if size < 0 {
		return Header{}, fmt.Errorf("%w: negative", ErrBadSize)
	}

	name := parts[2]
	if err := validateName(name); err != nil {
		return Header{}, err
	}

	// `D` records always carry size 0 in real scp — enforce that so
	// a malicious peer can't smuggle a non-zero size field through.
	if typ == RecordDirStart && size != 0 {
		return Header{}, fmt.Errorf("%w: D-record size must be 0", ErrBadSize)
	}

	return Header{
		Type: typ,
		// FileMode masks to the permission bits — we deliberately
		// drop setuid/setgid/sticky from incoming records; the host
		// applies an additional umask when creating the file.
		Mode: os.FileMode(mode64) & os.ModePerm,
		Size: size,
		Name: name,
	}, nil
}

// validateName enforces the path-safety rules on a header name.
// Returns one of the sentinel errors so callers can switch on the
// failure mode if needed.
func validateName(name string) error {
	if name == "" {
		return ErrEmptyName
	}
	if len(name) > MaxNameLen {
		return ErrNameTooLong
	}
	// NUL and newline aren't legal in unix filenames anyway and
	// would let a peer smuggle extra header lines or terminate
	// strings early in C-string-based downstream code.
	if strings.ContainsAny(name, "\x00\n\r") {
		return ErrInvalidName
	}
	// Absolute paths and any `..` component must be rejected
	// regardless of OS — the host serves a chroot-style rootDir
	// and the wire format never describes paths above it.
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) {
		return ErrPathTraversal
	}
	// Split on both separators so a Windows-style backslash path
	// is caught even when running on Linux.
	for _, sep := range []string{"/", `\`} {
		for _, p := range strings.Split(name, sep) {
			if p == ".." {
				return ErrPathTraversal
			}
		}
	}
	return nil
}

// WriteFileHeader emits a `C<mode> <size> <name>\n` line.
func WriteFileHeader(w io.Writer, mode os.FileMode, size int64, name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	if size < 0 {
		return fmt.Errorf("%w: negative", ErrBadSize)
	}
	// Mode is masked to the permission bits — same hardening we
	// apply on the read side.
	line := fmt.Sprintf("C%04o %d %s\n", uint32(mode&os.ModePerm), size, name)
	if len(line) > MaxHeaderLen {
		return ErrHeaderTooLong
	}
	_, err := io.WriteString(w, line)
	return err
}

// WriteAck writes a single ack byte (\x00) to the peer.
func WriteAck(w io.Writer) error {
	_, err := w.Write([]byte{AckByte})
	return err
}

// ReadAck reads a single byte from the peer and returns:
//
//	(nil, nil)              on a clean ack (\x00)
//	(warnMsg, nil)          on a warning line (\x01 + msg + \n) — the
//	                        caller may continue but should surface
//	                        the message
//	(fatalMsg, errFatal)    on a fatal line (\x02 + msg + \n) — the
//	                        caller must abort
//
// On I/O error it returns (nil, err).
func ReadAck(r *bufio.Reader) ([]byte, error) {
	b, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	switch b {
	case AckByte:
		return nil, nil
	case WarnPrefix, FatalPrefix:
		// Read the rest of the line (up to and including \n).
		// ReadSlice is bounded by the buffer size; we accept any
		// length here because the peer is reporting an error and
		// truncating its message would obscure debugging.
		line, lerr := r.ReadBytes('\n')
		// Strip trailing newline for cleaner display.
		line = trimTrailingNewline(line)
		if b == FatalPrefix {
			return line, &FatalError{Msg: string(line)}
		}
		// Warning: not an error, just a message.
		return line, lerr
	default:
		return nil, fmt.Errorf("dmsgscp: unexpected byte 0x%02x from peer", b)
	}
}

// FatalError wraps a fatal-prefix message from the peer. ReadAck
// returns one as its error so callers using errors.As can extract
// the original text.
type FatalError struct {
	Msg string
}

// Error implements the error interface.
func (e *FatalError) Error() string { return "dmsgscp: peer reported fatal: " + e.Msg }

// WriteFatal writes a fatal-prefix error line to the peer and
// returns any I/O error. Format: \x02<msg>\n.
func WriteFatal(w io.Writer, msg string) error {
	// Replace any newlines in the message — they'd let a malicious
	// caller inject extra framing on the wire.
	clean := strings.ReplaceAll(msg, "\n", " ")
	clean = strings.ReplaceAll(clean, "\r", " ")
	_, err := fmt.Fprintf(w, "%c%s\n", FatalPrefix, clean)
	return err
}

// WriteWarn writes a warning-prefix error line to the peer.
// Format: \x01<msg>\n. The peer is expected to surface this but
// continue.
func WriteWarn(w io.Writer, msg string) error {
	clean := strings.ReplaceAll(msg, "\n", " ")
	clean = strings.ReplaceAll(clean, "\r", " ")
	_, err := fmt.Fprintf(w, "%c%s\n", WarnPrefix, clean)
	return err
}

// ResolveSafePath joins rootDir with a wire-provided name and
// verifies the result stays under rootDir after symlink-unaware
// cleaning. Callers must NOT pass the result to filepath.EvalSymlinks
// before using it for I/O — the guard is purely structural.
//
// Returns the cleaned absolute path or ErrPathTraversal.
func ResolveSafePath(rootDir, name string) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(absRoot, name)
	cleaned := filepath.Clean(joined)
	// On case-insensitive filesystems this prefix check is still
	// correct because filepath.Clean preserves the original case
	// and absRoot derives from the same source. The trailing
	// separator on absRoot prevents a "/root-evil" path from
	// passing the HasPrefix("/root") check.
	rootWithSep := absRoot
	if !strings.HasSuffix(rootWithSep, string(filepath.Separator)) {
		rootWithSep += string(filepath.Separator)
	}
	if cleaned != absRoot && !strings.HasPrefix(cleaned, rootWithSep) {
		return "", ErrPathTraversal
	}
	return cleaned, nil
}

// trimTrailingNewline strips a single trailing \n and an optional
// preceding \r. Returns a slice into the input (no allocation).
func trimTrailingNewline(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	if len(b) > 0 && b[len(b)-1] == '\r' {
		b = b[:len(b)-1]
	}
	return b
}
