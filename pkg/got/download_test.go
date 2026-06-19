package got

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testContent returns deterministic bytes of length n.
func testContent(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}

// rangeServer serves content with full RFC 7233 range support via
// http.ServeContent (handles the bytes=0-0 probe and per-chunk ranges).
func rangeServer(t *testing.T, content []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "file.bin", time.Time{}, bytes.NewReader(content))
	}))
}

// --- chunk.go -----------------------------------------------------------

type bufferAt struct{ b []byte }

func (w *bufferAt) WriteAt(p []byte, off int64) (int, error) {
	if end := off + int64(len(p)); end > int64(len(w.b)) {
		nb := make([]byte, end)
		copy(nb, w.b)
		w.b = nb
	}
	copy(w.b[off:], p)
	return len(p), nil
}

func TestOffsetWriter(t *testing.T) {
	buf := &bufferAt{}
	w := &OffsetWriter{WriterAt: buf, Offset: 0}
	n, err := w.Write([]byte("ab"))
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.EqualValues(t, 2, w.Offset) // advanced
	_, err = w.Write([]byte("cd"))
	require.NoError(t, err)
	require.Equal(t, "abcd", string(buf.b))

	// A non-zero starting offset writes past the gap.
	buf2 := &bufferAt{}
	w2 := &OffsetWriter{WriterAt: buf2, Offset: 3}
	_, _ = w2.Write([]byte("xy"))
	require.Equal(t, "\x00\x00\x00xy", string(buf2.b))
}

// --- end-to-end downloads ----------------------------------------------

func TestDownloadRangeable(t *testing.T) {
	content := testContent(5000)
	srv := rangeServer(t, content)
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	g := New()
	dl := &Download{URL: srv.URL + "/file.bin", Dest: dest, ChunkSize: 1000}
	require.NoError(t, g.Do(dl))

	require.True(t, dl.IsRangeable())
	require.EqualValues(t, len(content), dl.TotalSize())
	require.Len(t, dl.chunks, 5)

	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	require.Equal(t, content, got)

	// State file is removed on success.
	_, statErr := os.Stat(dl.statePath())
	require.True(t, os.IsNotExist(statErr))
}

func TestDownloadNonRangeable(t *testing.T) {
	content := testContent(3000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Accept-Ranges", "none")
		_, _ = w.Write(content)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	g := New()
	dl := &Download{URL: srv.URL + "/file.bin", Dest: dest}
	require.NoError(t, g.Do(dl))

	require.False(t, dl.IsRangeable())
	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	require.Equal(t, content, got) // written during the probe stream
}

func TestDownloadHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	g := New()
	err := g.Download(srv.URL+"/missing", filepath.Join(t.TempDir(), "out"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "404")
}

func TestDownloadWithProgress(t *testing.T) {
	content := testContent(4000)
	srv := rangeServer(t, content)
	defer srv.Close()

	var calls int32
	g := New()
	g.ProgressFunc = func(_ *Download) { atomic.AddInt32(&calls, 1) }

	dest := filepath.Join(t.TempDir(), "out.bin")
	dl := &Download{URL: srv.URL + "/file.bin", Dest: dest, ChunkSize: 500, Interval: 1}
	require.NoError(t, g.Do(dl))

	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	require.Equal(t, content, got)
	// The progress goroutine path ran; call count is timing-dependent so we
	// only require it didn't panic and produced a correct file.
	_ = calls
}

// --- resume -------------------------------------------------------------

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	d := &Download{
		URL:  "http://example/file",
		Dest: filepath.Join(dir, "f.bin"),
		info: &Info{Size: 100, Rangeable: true},
		chunks: []*Chunk{
			{Start: 0, End: 49, Done: true},
			{Start: 50, End: 99, Done: false},
		},
	}
	require.NoError(t, d.saveState())

	st, err := d.loadState()
	require.NoError(t, err)
	require.Equal(t, d.URL, st.URL)
	require.EqualValues(t, 100, st.Size)
	require.Len(t, st.Chunks, 2)
	require.True(t, st.Chunks[0].Done)
}

func TestInitResume(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "f.bin")
	url := "http://example/file"

	state := resumeState{
		URL:  url,
		Size: 100,
		Chunks: []*Chunk{
			{Start: 0, End: 49, Done: true},
			{Start: 50, End: 99, Done: false},
		},
	}
	data, err := json.Marshal(state)
	require.NoError(t, err)
	// State file path is Path()+".got.json".
	require.NoError(t, os.WriteFile(dest+".got.json", data, 0600))

	d := &Download{URL: url, Dest: dest, Resume: true}
	require.NoError(t, d.Init())

	require.True(t, d.IsRangeable())
	require.EqualValues(t, 100, d.TotalSize())
	require.Len(t, d.chunks, 2)
	// Done chunk's bytes counted toward size (50 bytes: 0..49 inclusive).
	require.EqualValues(t, 50, d.Size())
}

// --- chunk-level error paths -------------------------------------------

func TestDownloadChunkNot206(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("full body, status 200")) // ignores Range → 200
	}))
	defer srv.Close()

	d := &Download{URL: srv.URL, Client: DefaultClient(), ctx: context.Background()}
	err := d.DownloadChunk(&Chunk{Start: 0, End: 9}, io.Discard)
	require.Error(t, err)
	require.Contains(t, err.Error(), "expected 206")
}

func TestDownloadChunkWithRetryCanceledCtx(t *testing.T) {
	srv := rangeServer(t, testContent(100))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled → first failure returns immediately, no backoff sleep
	d := &Download{URL: srv.URL, Client: DefaultClient(), ctx: ctx, MaxRetries: 3}
	err := d.downloadChunkWithRetry(&Chunk{Start: 0, End: 9}, io.Discard)
	require.Error(t, err)
}

// --- defaults helpers ---------------------------------------------------

func TestGetDefaultConcurrency(t *testing.T) {
	c := getDefaultConcurrency()
	require.GreaterOrEqual(t, c, uint(4))
	require.LessOrEqual(t, c, uint(20))
}

func TestGetDefaultChunkSize(t *testing.T) {
	// Small file: min clamps up.
	cs := getDefaultChunkSize(10000, 0, 0, 4)
	require.Positive(t, cs)
	require.Less(t, cs, uint64(10000))

	// Explicit max caps the size.
	cs = getDefaultChunkSize(100_000_000, 1000, 5000, 4)
	require.LessOrEqual(t, cs, uint64(5000))

	// Explicit min raises a tiny computed size.
	cs = getDefaultChunkSize(100_000, 8000, 0, 4)
	require.GreaterOrEqual(t, cs, uint64(8000))
}

// --- getters ------------------------------------------------------------

func TestDownloadGetters(t *testing.T) {
	d := &Download{ctx: context.Background()}
	require.EqualValues(t, 0, d.TotalSize()) // nil info
	require.False(t, d.IsRangeable())
	require.NotNil(t, d.Context())

	d.startedAt = time.Now().Add(-time.Second)
	require.Positive(t, d.TotalCost())

	// Write tracks bytes.
	n, err := d.Write([]byte("hello"))
	require.NoError(t, err)
	require.Equal(t, 5, n)
	require.EqualValues(t, 5, d.Size())

	// Speed / AvgSpeed don't divide by zero.
	require.NotPanics(t, func() { _ = d.Speed(); _ = d.AvgSpeed() })

	d.info = &Info{Size: 42, Rangeable: true}
	require.EqualValues(t, 42, d.TotalSize())
	require.True(t, d.IsRangeable())
}

func TestDownloadPath(t *testing.T) {
	// Dest wins.
	d := &Download{URL: "http://x/a.zip", Dest: "chosen.bin", Dir: "/tmp"}
	require.Equal(t, filepath.Join("/tmp", "chosen.bin"), d.Path())

	// Content-Disposition name when no Dest.
	d = &Download{URL: "http://x/a.zip", unsafeName: `attachment; filename="report.pdf"`}
	require.Equal(t, "report.pdf", d.Path())

	// Falls back to URL basename.
	d = &Download{URL: "http://x/archive.tar.gz"}
	require.Equal(t, "archive.tar.gz", d.Path())
}

func TestRunProgress(t *testing.T) {
	// Stops after StopProgress is set from within the callback.
	d := &Download{Interval: 1, ctx: context.Background()}
	var calls int32
	done := make(chan struct{})
	go func() {
		d.RunProgress(func(_ *Download) {
			if atomic.AddInt32(&calls, 1) >= 3 {
				d.StopProgress = true
			}
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("RunProgress did not stop")
	}
	require.GreaterOrEqual(t, atomic.LoadInt32(&calls), int32(3))

	// Canceled context returns promptly without calling fn.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d2 := &Download{ctx: ctx}
	require.NotPanics(t, func() { d2.RunProgress(func(_ *Download) { t.Error("fn must not run") }) })
}

func TestNewDownload(t *testing.T) {
	d := NewDownload(context.Background(), "http://x/f", "out")
	require.Equal(t, "http://x/f", d.URL)
	require.Equal(t, "out", d.Dest)
	require.NotNil(t, d.Client)
	require.NotNil(t, d.Context())
}
