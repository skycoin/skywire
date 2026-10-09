package netutil

import (
	"bytes"
	"io"
	"testing"
)

// chunkReader returns at most chunk bytes per Read and hides WriterTo.
type chunkReader struct {
	r     io.Reader
	chunk int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(p) > c.chunk {
		p = p[:c.chunk]
	}
	return c.r.Read(p)
}

type plainWriter struct{ b bytes.Buffer }

func (w *plainWriter) Write(p []byte) (int, error) { return w.b.Write(p) }

// A chatty stream keeps the small buffer, a bulk one moves to the large one,
// a WriterTo source takes none, and every byte arrives either way.
func TestCopyAdaptive(t *testing.T) {
	data := bytes.Repeat([]byte("skywire-"), 64*1024)
	for _, tc := range []struct {
		name  string
		src   io.Reader
		size  int
		bytes int
	}{
		{"small reads", &chunkReader{r: bytes.NewReader(data[:20000]), chunk: 512}, copyBufSmall, 20000},
		{"bulk", &chunkReader{r: bytes.NewReader(data), chunk: 1 << 20}, copyBufLarge, len(data)},
		{"writer to", bytes.NewReader(data), 0, len(data)},
	} {
		var w plainWriter
		size, err := copyAdaptive(&w, tc.src)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if size != tc.size {
			t.Fatalf("%s: ended on a %d-byte buffer, want %d", tc.name, size, tc.size)
		}
		if !bytes.Equal(w.b.Bytes(), data[:tc.bytes]) {
			t.Fatalf("%s: copied %d bytes that do not match", tc.name, w.b.Len())
		}
	}
}
