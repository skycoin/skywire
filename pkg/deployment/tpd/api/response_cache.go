// Package api pkg/deployment/tpd/api/response_cache.go c4-net-discovery
package api

import (
	"bytes"
	"compress/gzip"
	"io"
	"sync"
	"time"
)

// respCacheSlot is one memoized response body and its gzip copy.
type respCacheSlot struct {
	raw       []byte
	gz        []byte
	expiresAt time.Time
}

// gzipWriterPool reuses *gzip.Writer instances across calls to gzipBytes.
// Each gzip.NewWriter allocates a flate.compressor of ~70-100KB.
var gzipWriterPool = sync.Pool{
	New: func() any {
		return gzip.NewWriter(io.Discard)
	},
}

func gzipBytes(b []byte) []byte {
	var buf bytes.Buffer
	zw := gzipWriterPool.Get().(*gzip.Writer)
	zw.Reset(&buf)
	_, _ = zw.Write(b) //nolint:errcheck
	_ = zw.Close()     //nolint:errcheck
	gzipWriterPool.Put(zw)
	return buf.Bytes()
}
