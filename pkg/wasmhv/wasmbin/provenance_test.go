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
	// goStamped is true where the toolchain records Go's own
	// vcs.revision/vcs.modified. Only the TinyGo build does — see the comment
	// on TestCommittedWasmBuiltFromCommit.
	goStamped bool
}

var committedBlobs = []committedBlob{
	{path: filepath.Join("wasmgo", "wasm-visor.wasm.gz")},
	{path: filepath.Join("wasmtinygo", "wasm-visor.wasm.gz"), goStamped: true},
}

// dirtyVersion matches the version stamped by the Makefile when the tree had
// uncommitted changes: `git describe --always --dirty` appends -dirty after the
// commit it describes. Anchoring on the hex keeps a stray "-dirty" elsewhere in
// tens of megabytes of binary from failing the build.
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
// Two different signals, because the toolchains differ:
//
//   - the Makefile stamps WASM_VERSION from `git describe --always --dirty`,
//     which both builds carry. This is the load-bearing one.
//   - Go's own vcs.revision/vcs.modified, which the equivalent check in skycoin
//     reads, exists ONLY in the TinyGo artifact. GOOS=js emits no readable
//     buildinfo — no "Go buildinf" magic, and `go version -m` cannot parse
//     wasm — so requiring it of the std-Go blob would fail every time. Note
//     that a bare "vcs.revision" string DOES appear in any binary linking
//     runtime/debug, which is why the pattern here requires the value too.
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
