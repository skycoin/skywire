package shell

// Helpers for applets living outside this package (see shell/browser): path
// resolution against the interpreter's working directory, and the file and
// stream plumbing they would otherwise duplicate.

import (
	"bufio"
	"io"
	"path/filepath"
	"strings"

	"github.com/0magnet/afero"
	"github.com/0magnet/sh/v3/interp"
)

// Resolve makes path absolute against the shell's current directory.
func Resolve(hc *interp.HandlerContext, path string) string {
	return resolveArg(hc, path)
}

// Base is filepath.Base, so external applets need not import path/filepath.
func Base(path string) string { return filepath.Base(path) }

// ReadFile reads a file from the shell's filesystem, resolving relative paths
// against its working directory.
func ReadFile(s *Shell, hc *interp.HandlerContext, path string) ([]byte, error) {
	return afero.ReadFile(s.FS, resolveArg(hc, path))
}

// WriteFile writes a file to the shell's filesystem and returns the path it
// resolved to. Writing to an existing directory keeps the source base name.
func WriteFile(s *Shell, hc *interp.HandlerContext, path string, data []byte) (string, error) {
	dest := resolveArg(hc, path)
	if info, err := s.FS.Stat(dest); err == nil && info.IsDir() {
		dest = filepath.Join(dest, filepath.Base(path))
	}
	if err := afero.WriteFile(s.FS, dest, data, 0o644); err != nil {
		return "", err
	}
	return dest, nil
}

// ReadAll drains a reader (typically the applet's stdin).
func ReadAll(r io.Reader) ([]byte, error) { return io.ReadAll(r) }

// CopyLines calls fn for each newline-terminated line read from r, without the
// trailing newline. It returns when r is exhausted.
func CopyLines(r io.Reader, fn func(line string)) {
	reader := bufio.NewReader(r)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			fn(strings.TrimRight(line, "\r\n"))
		}
		if err != nil {
			return
		}
	}
}

// Printf, Println, Print and Write are the applet-output wrappers used by
// applets in other packages (see shell/browser). They drop the write error for
// the reason documented in write.go: a shell that cannot write its output has
// nowhere left to report that.
func Printf(w io.Writer, format string, a ...any) { fprintf(w, format, a...) }

// Println writes its operands and a newline, discarding any write error.
func Println(w io.Writer, a ...any) { fprintln(w, a...) }

// Print writes its operands, discarding any write error.
func Print(w io.Writer, a ...any) { fprint(w, a...) }

// Write writes raw bytes, discarding any write error.
func Write(w io.Writer, b []byte) { write(w, b) }
