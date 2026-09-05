package got

import (
	"io"
)

// OffsetWriter wraps an io.WriterAt to implement io.Writer at a given offset.
type OffsetWriter struct {
	io.WriterAt
	Offset int64
}

// Write implements io.Writer, writing at the current offset and advancing it.
func (dst *OffsetWriter) Write(b []byte) (n int, err error) {
	n, err = dst.WriteAt(b, dst.Offset)
	dst.Offset += int64(n)
	return
}

// Chunk represents a byte range for partial content download.
// Chunk and OffsetWriter are the byte-range primitives inherited
// from melbahja/got.
type Chunk struct {
	Start, End uint64
	Done       bool
}
