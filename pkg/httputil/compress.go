// Package httputil pkg/httputil/compress.go c0-com-util
package httputil

import (
	"bufio"
	"compress/gzip"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
)

// CompressMinBytes is the default body size below which CompressMin leaves a
// response uncompressed. The deployment services answer most requests with a
// few hundred bytes (one entry, one binding, one health line); gzipping those
// costs a flate writer (~800 KB of state, pooled) and CPU for no wire saving
// — flate was 15% of dmsg-discovery's CPU and the pooled writers 110 MB of the
// address resolver's heap (2026-09-10).
const CompressMinBytes = 1024

var gzipPool = sync.Pool{New: func() any { return gzip.NewWriter(nil) }}

// CompressMin gzips responses larger than minBytes for clients that accept
// gzip, when the content type is text, JSON, JavaScript, XML or SVG. Smaller
// responses, other types, flushes before the threshold, and hijacked
// connections pass through untouched. level is a compress/gzip level.
func CompressMin(minBytes, level int) func(http.Handler) http.Handler {
	if level < gzip.HuffmanOnly || level > gzip.BestCompression {
		level = gzip.DefaultCompression
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") || r.Method == http.MethodHead {
				next.ServeHTTP(w, r)
				return
			}
			cw := &compressWriter{ResponseWriter: w, min: minBytes, level: level, status: http.StatusOK}
			defer cw.finish()
			next.ServeHTTP(cw, r)
		})
	}
}

// compressible reports whether a body of this content type is worth gzipping.
func compressible(ct string) bool {
	ct = strings.ToLower(ct)
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.TrimSpace(ct)
	switch {
	case strings.HasPrefix(ct, "text/"),
		ct == "application/json", ct == "application/javascript", ct == "application/x-javascript",
		ct == "application/xml", ct == "image/svg+xml",
		strings.HasSuffix(ct, "+json"), strings.HasSuffix(ct, "+xml"):
		return true
	}
	return false
}

// compressWriter buffers the response until it is known to exceed min bytes;
// then it either flushes the buffer through gzip and streams the rest
// compressed, or (small, non-compressible, flushed early) writes it plain.
type compressWriter struct {
	http.ResponseWriter
	min     int
	level   int
	status  int
	buf     []byte
	decided bool // headers sent to the underlying writer
	gz      *gzip.Writer
}

func (c *compressWriter) WriteHeader(status int) {
	if !c.decided {
		c.status = status
	}
}

func (c *compressWriter) Write(p []byte) (int, error) {
	if c.decided {
		if c.gz != nil {
			return c.gz.Write(p)
		}
		return c.ResponseWriter.Write(p)
	}
	c.buf = append(c.buf, p...)
	if len(c.buf) > c.min {
		if err := c.decide(true); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// decide sends the headers and the buffered bytes, compressed when the body
// is large and of a compressible type.
func (c *compressWriter) decide(large bool) error {
	c.decided = true
	h := c.ResponseWriter.Header()
	ct := h.Get("Content-Type")
	if ct == "" && len(c.buf) > 0 {
		ct = http.DetectContentType(c.buf)
		h.Set("Content-Type", ct)
	}
	if large && compressible(ct) && h.Get("Content-Encoding") == "" {
		h.Del("Content-Length")
		h.Set("Content-Encoding", "gzip")
		h.Add("Vary", "Accept-Encoding")
		gz := gzipPool.Get().(*gzip.Writer)
		gz.Reset(c.ResponseWriter)
		c.gz = gz
		c.ResponseWriter.WriteHeader(c.status)
		_, err := gz.Write(c.buf)
		c.buf = nil
		return err
	}
	c.ResponseWriter.WriteHeader(c.status)
	if len(c.buf) > 0 {
		_, err := c.ResponseWriter.Write(c.buf)
		c.buf = nil
		return err
	}
	return nil
}

// finish closes out the response: undecided (small) bodies go plain; a gzip
// stream is closed and its writer returned to the pool.
func (c *compressWriter) finish() {
	if !c.decided {
		_ = c.decide(false) //nolint:errcheck // response already ending
		return
	}
	if c.gz != nil {
		_ = c.gz.Close() //nolint:errcheck // response already ending
		gz := c.gz
		c.gz = nil
		gz.Reset(nil)
		gzipPool.Put(gz)
	}
}

// Flush streams what is buffered so far. A flush before the threshold commits
// to a plain response: the handler wants bytes on the wire now.
func (c *compressWriter) Flush() {
	if !c.decided {
		_ = c.decide(false) //nolint:errcheck // best-effort flush
	}
	if c.gz != nil {
		_ = c.gz.Flush() //nolint:errcheck // best-effort flush
	}
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack hands the connection over untouched (WebSocket upgrades).
func (c *compressWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := c.ResponseWriter.(http.Hijacker); ok {
		c.decided = true
		return hj.Hijack()
	}
	return nil, nil, errors.New("httputil: underlying ResponseWriter does not support hijacking")
}

// Unwrap exposes the underlying writer for http.ResponseController.
func (c *compressWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }
