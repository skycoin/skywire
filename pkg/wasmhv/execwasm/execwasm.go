// Package execwasm pkg/wasmhv/execwasm/execwasm.go c3-wasm-embed
//
// The full skywire command module for GOOS=js, embedded in the native binary
// by the two-stage build: the module is built first (GOOS=js GOARCH=wasm go
// build .), gzipped into blob/skywire.wasm.gz, then the native binary is built
// with it inside. It is the desk's `skywire` command and the tab visor; hv
// serve and the native hypervisor serve it at /skywire.wasm.
//
// A source build without that step embeds only blob/README: Present() is
// false and the servers fall back to an on-disk module (ExecWasmPath) or to
// the legacy page. The gzipped bytes are what is embedded and what is served
// when the client accepts gzip (every browser does); the module is never held
// inflated in memory.
//
// It is never held GZIPPED in memory either. embed.FS keeps the blob in the
// binary's read-only data — file-backed pages the kernel shares and evicts —
// and every accessor here stays out of the Go heap: Present/Size are a
// directory lookup, Stamp reads the gzip trailer's last 8 bytes, and serving
// streams from the mapping (serve.go). Materializing it with
// embed.FS.ReadFile, as this package did until a heap profile of a live exit
// visor showed it, put 37 MB of anonymous, GC-tracked memory in every visor
// for the life of the process, whether or not a browser ever asked for the
// module: Present() alone, called from the UI server's startup, was enough.
package execwasm

import (
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"sync"
)

const blobName = "blob/skywire.wasm.gz"

var (
	once  sync.Once
	size  int64
	stamp string
	rev   string
)

func load() {
	once.Do(func() {
		size = embeddedSize()
		if size == 0 {
			return
		}
		stamp = trailerStamp(size)
		rev = embeddedRevision()
	})
}

// Present reports whether a module is embedded.
func Present() bool {
	load()
	return size > 0
}

// Size is the embedded module's gzipped size in bytes, 0 when none. It is the
// Content-Length of what Serve sends a gzip-accepting client.
func Size() int64 {
	load()
	return size
}

// Open returns a handle on the embedded module's gzipped bytes, for a caller
// that streams them (io.Copy is enough; the embed handle is also an io.Seeker
// and io.ReaderAt). Close it. The error is fs.ErrNotExist when this build
// embeds no module.
func Open() (fs.File, error) { return embeddedOpen() }

// Gz returns a COPY of the embedded module — ~37 MB of fresh heap on every
// call, and nothing here caches it. It is for the one consumer that cannot
// take a stream: skycoin-web's RegisterCipherWasm, which keeps the bytes
// itself, in the `skywire skycoin web` command. Everything that serves the
// module over HTTP uses Open or ServeEmbedded instead.
func Gz() []byte {
	load()
	if size == 0 {
		return nil
	}
	f, err := embeddedOpen()
	if err != nil {
		return nil
	}
	defer f.Close() //nolint:errcheck
	b := make([]byte, size)
	if _, err := io.ReadFull(f, b); err != nil {
		return nil
	}
	return b
}

// Stamp is a short content fingerprint of the embedded module, "" when none:
// it changes with every build, so pages polling the served version reload.
//
// It is the gzip member's own trailer — the CRC32 of the uncompressed module
// and its length, as 16 hex characters — read from the last 8 bytes of the
// blob. A hash over the whole blob would be just as good an ETag and would
// mean reading 37 MB at startup on every visor, including the Orange Pis;
// pkg/geoip identifies its embedded database the same way.
func Stamp() string {
	load()
	return stamp
}

// trailerStamp reads the gzip trailer (CRC32, ISIZE) of the embedded module
// and renders it as 16 hex characters. "" if the blob is too short or the
// handle supports neither ReadAt nor Seek (it is embed.FS, which supports
// both).
func trailerStamp(n int64) string {
	if n < 8 {
		return ""
	}
	f, err := embeddedOpen()
	if err != nil {
		return ""
	}
	defer f.Close() //nolint:errcheck
	var t [8]byte
	switch h := f.(type) {
	case io.ReaderAt:
		if _, err := h.ReadAt(t[:], n-8); err != nil {
			return ""
		}
	case io.Seeker:
		if _, err := h.Seek(n-8, io.SeekStart); err != nil {
			return ""
		}
		if _, err := io.ReadFull(f, t[:]); err != nil {
			return ""
		}
	default:
		return ""
	}
	return fmt.Sprintf("%08x%08x", binary.LittleEndian.Uint32(t[:4]), binary.LittleEndian.Uint32(t[4:]))
}

// revisionName is written beside the module by `make embed-exec-wasm`: the
// vcs.revision the module was built from, lifted at build time because the
// module is never inflated in memory and scanning 176 MB for it at startup
// would defeat that.
const revisionName = "blob/revision.txt"

// Revision is the commit the embedded module was built from, or "" when that
// is not recorded (a source build with no staged module, or one staged before
// this was written).
//
// It exists because the two failure modes here are silent. `go build .` embeds
// whatever blob/ already holds rather than rebuilding it, so a native binary
// happily serves a module many commits older than itself; and the served page
// reports the module's own version, which simply looks like a different number
// rather than a stale one. Comparing this to buildinfo.Commit() is the only
// cheap way to notice.
func Revision() string {
	load()
	return rev
}
