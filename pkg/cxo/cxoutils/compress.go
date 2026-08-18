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
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return b
	}
	defer func() { _ = zr.Close() }() //nolint:errcheck
	out, err := io.ReadAll(zr)
	if err != nil {
		return b
	}
	return out
}
