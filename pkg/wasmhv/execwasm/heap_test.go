// Package execwasm pkg/wasmhv/execwasm/heap_test.go c3-wasm-embed
package execwasm

import (
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
)

// noCopyBudget is what the accessors and one whole streamed response are
// allowed to leave on the heap: io.Copy's 32 KiB buffer, the http recorder,
// a gzip reader. Far below the ~37 MB module, which is the point — the test
// is here to catch a return to embed.FS.ReadFile, which pinned the module on
// the heap of every visor for the life of the process.
const noCopyBudget = 1 << 20

// TestEmbedded_AccessorsMakeNoHeapCopy: Present/Size/Stamp/Revision are what
// the visor calls at startup (execModuleSource, execModuleDesc) whether or not
// a browser ever asks for the module. They must be a directory lookup and an
// 8-byte read, not a copy of the blob.
func TestEmbedded_AccessorsMakeNoHeapCopy(t *testing.T) {
	n := requireStagedModule(t)

	delta := heapDeltaKiB(func() {
		if got := embeddedSize(); got != n {
			t.Errorf("embeddedSize() = %d, want %d", got, n)
		}
		if s := trailerStamp(n); s == "" {
			t.Error("trailerStamp() = \"\" for a staged module")
		}
		_ = embeddedRevision()
	})
	t.Logf("accessors: heap +%d KiB for a %d MiB module", delta, n>>20)
	if delta > noCopyBudget/1024 {
		t.Errorf("accessors allocated %d KiB; want below %d KiB (the module is %d MiB)",
			delta, noCopyBudget/1024, n>>20)
	}
}

// TestEmbedded_ServeMakesNoHeapCopy: serving the module streams it out of the
// binary's read-only mapping. A response carrying all ~37 MB must not put all
// ~37 MB on the heap to do it — a per-request copy is how a handful of page
// loads OOMs a 4 GB exit host.
func TestEmbedded_ServeMakesNoHeapCopy(t *testing.T) {
	n := requireStagedModule(t)

	var served int64
	delta := heapDeltaKiB(func() {
		r := httptest.NewRequest(http.MethodGet, "/skywire.wasm", nil)
		r.Header.Set("Accept-Encoding", "gzip")
		// The recorder's own buffer would hold the response; discard instead,
		// so what is measured is the serving path and not the test's sink.
		w := &discardRecorder{h: http.Header{}}
		ServeEmbedded(w, r)
		served = w.n
	})
	t.Logf("serve: heap +%d KiB for %d bytes written", delta, served)
	if served != n {
		t.Errorf("served %d bytes, want the whole module (%d)", served, n)
	}
	if delta > noCopyBudget/1024 {
		t.Errorf("serving allocated %d KiB; want below %d KiB", delta, noCopyBudget/1024)
	}
}

// TestGz_IsTheCopyingPath is the control: it shows the measurement above would
// have caught the old behavior. Gz() is the one accessor that still copies —
// for skycoin-web, which cannot take a stream — so its delta is the module's
// size, and nothing on a visor's path may call it.
func TestGz_IsTheCopyingPath(t *testing.T) {
	n := requireStagedModule(t)

	var b []byte
	delta := heapDeltaKiB(func() { b = Gz() })
	runtime.KeepAlive(b)
	t.Logf("Gz(): heap +%d KiB", delta)
	if int64(len(b)) != n {
		t.Fatalf("Gz() returned %d bytes, want %d", len(b), n)
	}
	if delta < (n>>10)/2 {
		t.Errorf("Gz() left only %d KiB on the heap for a %d KiB copy; the measurement is not seeing allocations",
			delta, n>>10)
	}
}

// TestServeEmbedded_ETagAndBytes: the streamed response is the blob verbatim,
// with the stamp as its ETag and a conditional request short-circuiting.
func TestServeEmbedded_ETagAndBytes(t *testing.T) {
	n := requireStagedModule(t)

	r := httptest.NewRequest(http.MethodGet, "/skywire.wasm", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := &discardRecorder{h: http.Header{}}
	ServeEmbedded(w, r)
	if w.code != 0 && w.code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.code)
	}
	if got := w.h.Get("Content-Encoding"); got != "gzip" {
		t.Errorf("content-encoding = %q, want gzip", got)
	}
	if got := w.h.Get("ETag"); got != `"`+Stamp()+`"` {
		t.Errorf("etag = %q, want %q", got, `"`+Stamp()+`"`)
	}
	if w.n != n {
		t.Errorf("wrote %d bytes, want %d", w.n, n)
	}
	if w.first[0] != 0x1f || w.first[1] != 0x8b {
		t.Errorf("body does not start with the gzip magic: % x", w.first)
	}

	r2 := httptest.NewRequest(http.MethodGet, "/skywire.wasm", nil)
	r2.Header.Set("If-None-Match", `"`+Stamp()+`"`)
	w2 := &discardRecorder{h: http.Header{}}
	ServeEmbedded(w2, r2)
	if w2.code != http.StatusNotModified {
		t.Errorf("conditional request status = %d, want 304", w2.code)
	}
	if w2.n != 0 {
		t.Errorf("conditional request wrote %d bytes, want none", w2.n)
	}
}

func requireStagedModule(t *testing.T) int64 {
	t.Helper()
	if !Present() {
		t.Skip("no skywire.wasm module staged in this build (make embed-exec-wasm)")
	}
	n := Size()
	if n < 4<<20 {
		t.Skipf("staged module is %d bytes; too small to tell a copy from measurement noise", n)
	}
	return n
}

// discardRecorder is an http.ResponseWriter that counts what it is given and
// keeps only the first bytes, so measuring a 37 MB response does not measure
// the buffer holding it.
type discardRecorder struct {
	h     http.Header
	code  int
	n     int64
	first [2]byte
}

func (d *discardRecorder) Header() http.Header { return d.h }

func (d *discardRecorder) Write(p []byte) (int, error) {
	if d.n == 0 && len(p) >= 2 {
		d.first[0], d.first[1] = p[0], p[1]
	}
	d.n += int64(len(p))
	return len(p), nil
}

func (d *discardRecorder) WriteHeader(code int) { d.code = code }

var _ io.Writer = (*discardRecorder)(nil)

// heapDeltaKiB reports how much heap f leaves behind, in KiB. Both samples are
// taken after a GC, so what is left is what is still referenced; the
// subtraction stays in the unsigned domain (the delta can legitimately be
// negative) so there is no unchecked uint64→int64 narrowing.
func heapDeltaKiB(f func()) int64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	f()
	runtime.GC()
	runtime.ReadMemStats(&after)

	d, neg := after.HeapAlloc-before.HeapAlloc, false
	if after.HeapAlloc < before.HeapAlloc {
		d, neg = before.HeapAlloc-after.HeapAlloc, true
	}
	d /= 1024
	if d > math.MaxInt64 {
		d = math.MaxInt64
	}
	if neg {
		return -int64(d)
	}
	return int64(d)
}
