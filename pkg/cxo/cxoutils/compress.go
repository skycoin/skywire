// Package cxoutils pkg/cxo/cxoutils/compress.go c2-net-cxo
// gzip helpers for CXO payloads. CXO propagates and stores object bytes
// verbatim — it has no built-in compression — so large JSON feeds (e.g. the
// TPD all-transports snapshot) travel uncompressed unless the publisher
// compresses the body itself. Gzip on publish + Gunzip on read closes that gap
// for both storage and propagation.
package cxoutils

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"io"
	"sync"
)

// gzipMagic is the two-byte gzip header (RFC 1952). Gunzip uses it to
// auto-detect whether a payload is compressed, which makes the format
// self-describing: a subscriber handles both gzipped and raw bodies, so a
// publisher can switch to gzip without breaking readers that predate this
// helper (they simply won't recognize the bytes — callers of such feeds have
// an HTTP fallback), and readers that postdate it transparently accept the old
// raw bodies too.
var gzipMagic = []byte{0x1f, 0x8b}

var gzipWriterPool = sync.Pool{
	New: func() any { return gzip.NewWriter(io.Discard) },
}

// Gzip returns the gzip-compressed form of b. Empty input returns nil so the
// round-trip Gunzip(Gzip(nil)) is nil, not a valid-but-empty gzip stream.
func Gzip(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	var buf bytes.Buffer
	zw := gzipWriterPool.Get().(*gzip.Writer)
	zw.Reset(&buf)
	_, _ = zw.Write(b) //nolint:errcheck // writing to a bytes.Buffer cannot fail
	_ = zw.Close()     //nolint:errcheck // flush; the Buffer never errors
	gzipWriterPool.Put(zw)
	return buf.Bytes()
}

// Gunzip returns the decompressed form of b if it is a gzip stream (detected by
// the gzip magic header), otherwise b unchanged. This lets a reader accept both
// gzipped and raw payloads on the same feed. On a decompression error the
// original bytes are returned so a malformed stream degrades to "treat as raw"
// rather than dropping the payload.
func Gunzip(b []byte) []byte {
	if len(b) < 2 || b[0] != gzipMagic[0] || b[1] != gzipMagic[1] {
		return b
	}
	zr, _ := gzipReaderPool.Get().(*gzip.Reader)
	var err error
	if zr == nil {
		zr, err = gzip.NewReader(bytes.NewReader(b))
	} else {
		err = zr.Reset(bytes.NewReader(b))
	}
	if err != nil {
		return b
	}
	defer gzipReaderPool.Put(zr)
	out := make([]byte, 0, gunzipSizeHint(b))
	for {
		if len(out) == cap(out) {
			out = append(out, 0)[:len(out)] // let append pick the growth
		}
		n, err := zr.Read(out[len(out):cap(out)])
		out = out[:len(out)+n]
		if err == io.EOF {
			return out
		}
		if err != nil {
			return b
		}
	}
}

// gzipReaderPool reuses readers, each of which allocates a 32 kB window.
var gzipReaderPool sync.Pool

// gunzipSizeHint is the output size to allocate up front: the stream's ISIZE
// trailer (RFC 1952 section 2.3.1), which only a well formed stream gets right,
// so it is capped at a ratio no real CXO body exceeds.
func gunzipSizeHint(b []byte) int {
	const maxRatio = 64
	if len(b) < 18 {
		return 512
	}
	n := int(binary.LittleEndian.Uint32(b[len(b)-4:]))
	return min(n+1, len(b)*maxRatio)
}
