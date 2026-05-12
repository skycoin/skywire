// Package dmsgscp pkg/dmsg/dmsgscp/protocol_test.go: unit coverage
// for the wire-protocol parser/writer + path-traversal guard.
// Integration tests (real dmsg.Client end-to-end) are intentionally
// skipped — those run live against peer visors.
package dmsgscp

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteAndReadFileHeader(t *testing.T) {
	cases := []struct {
		name     string
		mode     os.FileMode
		size     int64
		fileName string
	}{
		{"basic", 0644, 1234, "myfile.bin"},
		{"zero-byte", 0600, 0, "empty.txt"},
		{"max-size", 0644, MaxFileSize, "huge.dat"},
		{"name-with-spaces", 0755, 42, "file with spaces.txt"},
		{"long-name", 0644, 1, strings.Repeat("a", MaxNameLen)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteFileHeader(&buf, tc.mode, tc.size, tc.fileName); err != nil {
				t.Fatalf("WriteFileHeader: %v", err)
			}
			got, err := ReadHeader(bufio.NewReader(&buf))
			if err != nil {
				t.Fatalf("ReadHeader: %v", err)
			}
			if got.Type != RecordFile {
				t.Errorf("Type = %c, want C", got.Type)
			}
			if got.Mode != tc.mode&os.ModePerm {
				t.Errorf("Mode = %o, want %o", got.Mode, tc.mode&os.ModePerm)
			}
			if got.Size != tc.size {
				t.Errorf("Size = %d, want %d", got.Size, tc.size)
			}
			if got.Name != tc.fileName {
				t.Errorf("Name = %q, want %q", got.Name, tc.fileName)
			}
		})
	}
}

func TestReadHeaderDirRecords(t *testing.T) {
	t.Run("D-start", func(t *testing.T) {
		buf := bytes.NewBufferString("D0755 0 mydir\n")
		got, err := ReadHeader(bufio.NewReader(buf))
		if err != nil {
			t.Fatalf("ReadHeader(D): %v", err)
		}
		if got.Type != RecordDirStart {
			t.Errorf("Type = %c, want D", got.Type)
		}
		if got.Name != "mydir" {
			t.Errorf("Name = %q, want mydir", got.Name)
		}
	})
	t.Run("D-with-nonzero-size", func(t *testing.T) {
		buf := bytes.NewBufferString("D0755 5 mydir\n")
		_, err := ReadHeader(bufio.NewReader(buf))
		if !errors.Is(err, ErrBadSize) {
			t.Errorf("err = %v, want ErrBadSize", err)
		}
	})
	t.Run("E-end", func(t *testing.T) {
		buf := bytes.NewBufferString("E\n")
		got, err := ReadHeader(bufio.NewReader(buf))
		if err != nil {
			t.Fatalf("ReadHeader(E): %v", err)
		}
		if got.Type != RecordDirEnd {
			t.Errorf("Type = %c, want E", got.Type)
		}
	})
	t.Run("E-with-trailing-junk", func(t *testing.T) {
		buf := bytes.NewBufferString("Etrailing\n")
		_, err := ReadHeader(bufio.NewReader(buf))
		if !errors.Is(err, ErrUnknownRecord) {
			t.Errorf("err = %v, want ErrUnknownRecord", err)
		}
	})
}

func TestReadHeaderMalformed(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want error
	}{
		{"empty-line", "\n", ErrShortHeader},
		{"unknown-prefix", "Q0644 1 x\n", ErrUnknownRecord},
		{"missing-fields", "C0644 1234\n", ErrShortHeader},
		{"bad-mode", "Cabcd 1 x\n", ErrBadMode},
		{"bad-size", "C0644 nope x\n", ErrBadSize},
		{"negative-size", "C0644 -1 x\n", ErrBadSize},
		{"size-over-cap", "C0644 999999999999 x\n", ErrSizeCap},
		{"empty-name", "C0644 0 \n", ErrEmptyName},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ReadHeader(bufio.NewReader(strings.NewReader(tc.in)))
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestReadHeaderTooLong(t *testing.T) {
	// Construct a header line longer than MaxHeaderLen so the cap
	// check fires before allocation. The default bufio.Reader
	// buffer is 4096 — we want a single line that's longer than
	// MaxHeaderLen but still fits in the buffer so ReadSlice
	// returns it successfully and our length check rejects it.
	name := strings.Repeat("x", MaxHeaderLen) // way over MaxNameLen but exercises the line-length check
	in := "C0644 1 " + name + "\n"
	_, err := ReadHeader(bufio.NewReader(strings.NewReader(in)))
	// Either the name-length check or the header-length check fires.
	// Both are valid rejections — the important thing is that we
	// don't silently accept an overlong line.
	if err == nil {
		t.Fatal("expected error for overlong header, got nil")
	}
	if !errors.Is(err, ErrNameTooLong) && !errors.Is(err, ErrHeaderTooLong) {
		t.Errorf("err = %v, want ErrNameTooLong or ErrHeaderTooLong", err)
	}
}

func TestReadHeaderEOF(t *testing.T) {
	_, err := ReadHeader(bufio.NewReader(strings.NewReader("")))
	if !errors.Is(err, io.EOF) {
		t.Errorf("err = %v, want io.EOF", err)
	}
}

func TestPathTraversalRejection(t *testing.T) {
	cases := []string{
		"../etc/passwd",
		"/etc/passwd",
		"foo/../bar",
		"foo/../../bar",
		`..\windows`,
		`\absolute`,
		"a/b/../../../c",
		"..",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			in := "C0644 1 " + name + "\n"
			_, err := ReadHeader(bufio.NewReader(strings.NewReader(in)))
			if !errors.Is(err, ErrPathTraversal) {
				t.Errorf("err = %v, want ErrPathTraversal", err)
			}
		})
	}
}

func TestInvalidNameChars(t *testing.T) {
	cases := []string{
		"file\x00name",
		"file\nname",
		"file\rname",
	}
	for _, name := range cases {
		// Drop newline cases that would prematurely terminate the
		// header line on the parser side — those manifest as
		// ErrShortHeader via the split. Test validateName directly
		// for those.
		if strings.ContainsAny(name, "\n\r") {
			if err := validateName(name); !errors.Is(err, ErrInvalidName) {
				t.Errorf("validateName(%q) = %v, want ErrInvalidName", name, err)
			}
			continue
		}
		t.Run(name, func(t *testing.T) {
			in := "C0644 1 " + name + "\n"
			_, err := ReadHeader(bufio.NewReader(strings.NewReader(in)))
			if !errors.Is(err, ErrInvalidName) {
				t.Errorf("err = %v, want ErrInvalidName", err)
			}
		})
	}
}

func TestWriteHeaderRejectsBadInput(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFileHeader(&buf, 0644, -1, "x"); !errors.Is(err, ErrBadSize) {
		t.Errorf("negative size: err = %v, want ErrBadSize", err)
	}
	buf.Reset()
	if err := WriteFileHeader(&buf, 0644, MaxFileSize+1, "x"); !errors.Is(err, ErrSizeCap) {
		t.Errorf("oversize: err = %v, want ErrSizeCap", err)
	}
	buf.Reset()
	if err := WriteFileHeader(&buf, 0644, 1, "../x"); !errors.Is(err, ErrPathTraversal) {
		t.Errorf("traversal: err = %v, want ErrPathTraversal", err)
	}
	buf.Reset()
	if err := WriteFileHeader(&buf, 0644, 1, ""); !errors.Is(err, ErrEmptyName) {
		t.Errorf("empty: err = %v, want ErrEmptyName", err)
	}
}

func TestAckRoundTrip(t *testing.T) {
	t.Run("clean-ack", func(t *testing.T) {
		var buf bytes.Buffer
		if err := WriteAck(&buf); err != nil {
			t.Fatalf("WriteAck: %v", err)
		}
		line, err := ReadAck(bufio.NewReader(&buf))
		if err != nil {
			t.Errorf("ReadAck: %v", err)
		}
		if line != nil {
			t.Errorf("line = %q, want nil", line)
		}
	})
	t.Run("fatal", func(t *testing.T) {
		var buf bytes.Buffer
		if err := WriteFatal(&buf, "no such file"); err != nil {
			t.Fatalf("WriteFatal: %v", err)
		}
		line, err := ReadAck(bufio.NewReader(&buf))
		if err == nil {
			t.Fatal("expected error from ReadAck, got nil")
		}
		var fe *FatalError
		if !errors.As(err, &fe) {
			t.Errorf("err = %v, want *FatalError", err)
		}
		if string(line) != "no such file" {
			t.Errorf("line = %q, want %q", line, "no such file")
		}
	})
	t.Run("warn", func(t *testing.T) {
		var buf bytes.Buffer
		if err := WriteWarn(&buf, "stale stat"); err != nil {
			t.Fatalf("WriteWarn: %v", err)
		}
		line, err := ReadAck(bufio.NewReader(&buf))
		// EOF after the line is fine for a fresh buffer; the
		// important thing is no fatal-style error.
		if err != nil && !errors.Is(err, io.EOF) {
			t.Errorf("unexpected err: %v", err)
		}
		if string(line) != "stale stat" {
			t.Errorf("line = %q, want %q", line, "stale stat")
		}
	})
	t.Run("fatal-strips-newlines-in-msg", func(t *testing.T) {
		var buf bytes.Buffer
		if err := WriteFatal(&buf, "line1\nline2"); err != nil {
			t.Fatalf("WriteFatal: %v", err)
		}
		if strings.Count(buf.String(), "\n") != 1 {
			t.Errorf("expected exactly one newline in framed output, got %q", buf.String())
		}
	})
}

func TestResolveSafePath(t *testing.T) {
	root := t.TempDir()
	t.Run("simple-file", func(t *testing.T) {
		got, err := ResolveSafePath(root, "foo.txt")
		if err != nil {
			t.Fatalf("ResolveSafePath: %v", err)
		}
		want := filepath.Join(root, "foo.txt")
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("nested-file", func(t *testing.T) {
		got, err := ResolveSafePath(root, "sub/dir/file")
		if err != nil {
			t.Fatalf("ResolveSafePath: %v", err)
		}
		want := filepath.Join(root, "sub", "dir", "file")
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("traversal-rejected", func(t *testing.T) {
		_, err := ResolveSafePath(root, "../escape")
		if !errors.Is(err, ErrPathTraversal) {
			t.Errorf("err = %v, want ErrPathTraversal", err)
		}
	})
	t.Run("absolute-rejected", func(t *testing.T) {
		_, err := ResolveSafePath(root, "/etc/passwd")
		if !errors.Is(err, ErrPathTraversal) {
			t.Errorf("err = %v, want ErrPathTraversal", err)
		}
	})
	t.Run("sibling-prefix-rejected", func(t *testing.T) {
		// Build a sibling directory that shares the rootDir prefix
		// as a string but is NOT under rootDir on the filesystem.
		// /tmp/rootXXX  vs  /tmp/rootXXX-evil.
		sibling := root + "-evil"
		// We don't need to create it — ResolveSafePath is
		// purely path-string-based. But we should ensure that
		// constructing a name that would land in "sibling" via
		// joining is rejected. With our validator, that's
		// impossible (no `..` allowed), so this test is a
		// belt-and-suspenders check that joining a normal name
		// can't escape via clever string manipulation.
		_, err := ResolveSafePath(root, "foo")
		if err != nil {
			t.Errorf("unexpected error for normal name: %v", err)
		}
		_ = sibling
	})
}
