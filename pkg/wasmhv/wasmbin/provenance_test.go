package wasmbin

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// committedBlob is a wasm artifact kept in the repository.
type committedBlob struct {
	path string
	// goStamped requires Go's own vcs.revision to be present, rather than only
	// checking it when it happens to be. See the comment on
	// TestCommittedWasmBuiltFromCommit for why the std-Go blob does not require
	// it yet.
	goStamped bool
}

var committedBlobs = []committedBlob{
	// Not goStamped yet: the committed blob predates the removal of
	// -buildvcs=false, so it carries no vcs.revision. Once it is regenerated it
	// will, and this can require it. Until then the check below still rejects a
	// vcs.modified=true if one appears.
	{path: filepath.Join("wasmgo", "wasm-visor.wasm.gz")},
	{path: filepath.Join("wasmtinygo", "wasm-visor.wasm.gz"), goStamped: true},
}

// dirtyVersion is a legacy guard. The Makefile no longer stamps a version via
// ldflags (the wasm-visor self-describes from Go's own vcs records instead), so
// current builds never emit this string; it stays only to reject an artifact
// built the old way — `git describe --always --dirty` appended -dirty after the
// commit it described. Anchoring on the hex keeps a stray "-dirty" elsewhere in
// tens of megabytes of binary from failing the build. The load-bearing signal
// is now goDirty / goRevision below.
var dirtyVersion = regexp.MustCompile(`[0-9a-f]{7,}-dirty`)

// goDirty is Go's own marker, recorded by the TinyGo toolchain.
var goDirty = []byte("vcs.modified=true")

// goRevision is Go's recorded revision, e.g. vcs.revision=<40 hex>.
var goRevision = regexp.MustCompile(`vcs\.revision=[0-9a-f]{40}`)

// TestCommittedWasmBuiltFromCommit fails when a committed wasm was built from a
// dirty working tree.
//
// These artifacts cannot be checked by rebuilding and comparing. The commit
// that compiles one is always earlier than the commit that carries it, so they
// never match; and the build stamps a timestamp, so two builds of identical
// source differ anyway. Byte equality is not available here.
//
// What is checkable is whether the thing was built from a commit at all. A
// build from an uncommitted tree is described by no commit, so nobody —
// including whoever built it — can reproduce it or say what is in it. That is
// the state this rejects.
//
// The load-bearing signal is Go's own vcs.revision/vcs.modified, which the
// equivalent check in skycoin reads. `go version -m` cannot parse wasm, but that
// is a limitation of that command, not of the format: the records themselves are
// written into a GOOS=js binary as plain text and can be read straight out of
// it, which is exactly what skycoin's ci-scripts/check-wasm-version.js does to
// its own js/wasm cipher. The std-Go blob here carries none only because it was
// built while the Makefile still passed -buildvcs=false; the TinyGo one, built
// without it, carries them. Note that a bare "vcs.revision" string DOES appear
// in any binary linking runtime/debug, which is why the pattern here requires
// the value too. (The legacy ldflags-stamped WASM_VERSION check, dirtyVersion,
// still runs but no current build emits it — see its comment above.)
//
// The revision is not checked against HEAD. Committing an artifact necessarily
// moves HEAD past the commit it was built at, so requiring equality would fail
// by construction.
func TestCommittedWasmBuiltFromCommit(t *testing.T) {
	for _, blob := range committedBlobs {
		t.Run(blob.path, func(t *testing.T) {
			data, err := readBlob(blob.path)
			if errors.Is(err, os.ErrNotExist) {
				// Absence is a different failure, and TestEmbeddedGet covers the
				// variant this build actually embeds.
				t.Skipf("%s is not committed in this tree", blob.path)
			}
			if err != nil {
				t.Fatalf("read %s: %v", blob.path, err)
			}

			if m := dirtyVersion.Find(data); m != nil {
				t.Errorf("%s was built from a dirty working tree (version %q).\n"+
					"No commit describes what is in that file. Commit the source first, then\n"+
					"rebuild it (make embed-wasm-visor / embed-wasm-visor-tinygo) and commit the result.",
					blob.path, m)
			}
			if bytes.Contains(data, goDirty) {
				t.Errorf("%s was built from a dirty working tree (vcs.modified=true). Commit the\n"+
					"source first, then rebuild and commit the artifact.", blob.path)
			}
			if blob.goStamped && !goRevision.Match(data) {
				t.Errorf("%s carries no VCS revision, which this toolchain does record.\n"+
					"It was built by something that cannot say what it was built from, which is as\n"+
					"unreproducible as a dirty build. Do not pass -buildvcs=false for a committed artifact.",
					blob.path)
			}
		})
	}
}

// readBlob decompresses a committed artifact. They are tens of megabytes, so
// the caller gets one allocation and the file is closed before scanning.
func readBlob(path string) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // a fixed path in this package
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck

	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer zr.Close() //nolint:errcheck
	return io.ReadAll(zr)
}
