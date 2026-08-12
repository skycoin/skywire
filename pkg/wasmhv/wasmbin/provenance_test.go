package wasmbin

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// committedBlobs are the wasm artifacts kept in the repository.
var committedBlobs = []string{
	filepath.Join("wasmgo", "wasm-visor.wasm.gz"),
	filepath.Join("wasmtinygo", "wasm-visor.wasm.gz"),
}

// TestCommittedWasmBuiltFromCommit fails when a committed wasm was built from a
// dirty working tree, or from a build that refused to say.
//
// These artifacts cannot be checked by rebuilding and comparing. The commit
// that compiles one is always earlier than the commit that carries it, so they
// never match; and the build stamps a timestamp, so two builds of identical
// source differ anyway. Byte equality is not available here.
//
// What is checkable is whether the thing was built from a commit at all. `go
// build` in a git work tree records vcs.revision and vcs.modified into the
// binary in plain text — no need to run the module or parse wasm.
// vcs.modified=true says the tree had uncommitted changes, so no commit
// describes what is in the file and nobody, including its author, can
// reproduce it. That is the state this rejects.
//
// A missing stamp is rejected for the same reason rather than passed over. It
// is the easier failure to reintroduce, because suppressing it looks tidy:
// -buildvcs=false was once passed here deliberately, to keep a "+dirty" suffix
// out of the version string of a blob built from an uncommitted tree. That
// removed the evidence and not the problem — precisely the state this rejects,
// with the only witness turned off.
//
// The revision is NOT checked against HEAD. Committing an artifact necessarily
// moves HEAD past the commit it was built at, so requiring equality would fail
// every time by construction.
func TestCommittedWasmBuiltFromCommit(t *testing.T) {
	for _, blob := range committedBlobs {
		t.Run(blob, func(t *testing.T) {
			f, err := os.Open(blob) //nolint:gosec // a fixed path in this package
			if errors.Is(err, os.ErrNotExist) {
				// Absence is a different failure, and TestEmbeddedGet covers the
				// variant this build actually embeds.
				t.Skipf("%s is not committed in this tree", blob)
			}
			if err != nil {
				t.Fatalf("open %s: %v", blob, err)
			}
			defer f.Close() //nolint:errcheck

			zr, err := gzip.NewReader(f)
			if err != nil {
				t.Fatalf("%s is not gzip: %v", blob, err)
			}
			defer zr.Close() //nolint:errcheck

			found, err := scanFor(zr, [][]byte{
				[]byte("vcs.revision="),
				[]byte("vcs.modified=true"),
			})
			if err != nil {
				t.Fatalf("read %s: %v", blob, err)
			}

			if found[1] {
				t.Errorf("%s was built from a dirty working tree (vcs.modified=true).\n"+
					"No commit describes what is in that file. Commit the source first, then\n"+
					"rebuild it (make embed-wasm-visor / embed-wasm-visor-tinygo) and commit the result.", blob)
			}
			if !found[0] {
				t.Errorf("%s carries no VCS stamp (no vcs.revision).\n"+
					"It cannot say what it was built from, which is as unreproducible as a dirty\n"+
					"build. Do not pass -buildvcs=false when building a committed artifact.", blob)
			}
		})
	}
}

// scanFor reports which needles appear in r, streaming so a 46MB artifact is
// never held in memory at once. It keeps the tail of each chunk so a needle
// straddling a chunk boundary is still found.
func scanFor(r io.Reader, needles [][]byte) ([]bool, error) {
	found := make([]bool, len(needles))
	longest := 0
	for _, n := range needles {
		if len(n) > longest {
			longest = len(n)
		}
	}

	const chunk = 1 << 20
	buf := make([]byte, 0, chunk+longest)
	tmp := make([]byte, chunk)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			for i, needle := range needles {
				if !found[i] && bytes.Contains(buf, needle) {
					found[i] = true
				}
			}
			// Keep only enough tail for a needle spanning the seam.
			if keep := longest - 1; keep > 0 && len(buf) > keep {
				buf = append(buf[:0], buf[len(buf)-keep:]...)
			} else if keep <= 0 {
				buf = buf[:0]
			}
		}
		if errors.Is(err, io.EOF) {
			return found, nil
		}
		if err != nil {
			return found, err
		}
	}
}
