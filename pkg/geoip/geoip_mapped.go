//go:build !mobile && !js

// Package geoip pkg/geoip/geoip_mapped.go c0-com-util
package geoip

import (
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/oschwald/geoip2-golang/v2"
)

// The inflated database is ~60 MB. Held as a heap []byte it is 60 MB of
// resident, GC-accounted memory in every visor and dmsg server for the life
// of the process, and under a GOMEMLIMIT it eats that much of the budget
// and drives GC cycles for a blob that never changes. Written once to a
// cache file and memory-mapped instead, its pages are file-backed: the
// kernel shares them between processes on the host, evicts them under
// pressure, and the Go runtime never sees them. Decompression streams
// straight to the file, so the heap never holds the whole database.

// mappedDir is the directory the inflated database is cached in: the
// user's cache directory, else the system temp directory.
func mappedDir() string {
	if d, err := os.UserCacheDir(); err == nil && d != "" {
		return filepath.Join(d, "skywire")
	}
	return filepath.Join(os.TempDir(), "skywire-geoip")
}

// gzTrailer reads the CRC32 and uncompressed size a gzip member ends with;
// together they identify the database version and validate a cached copy
// without inflating anything.
func gzTrailer(gz []byte) (crc uint32, size int64, ok bool) {
	if len(gz) < 8 {
		return 0, 0, false
	}
	t := gz[len(gz)-8:]
	return binary.LittleEndian.Uint32(t[:4]), int64(binary.LittleEndian.Uint32(t[4:])), true
}

// mappedPath is the cache file for this build's database.
func mappedPath(dir string, crc uint32) string {
	return filepath.Join(dir, fmt.Sprintf("GeoLite2-City-%08x.mmdb", crc))
}

// openMapped returns a reader memory-mapped over a cached inflated copy of
// the embedded database under dir, writing the copy first if it is missing
// or the wrong size. The write goes to a temp file renamed into place, so a
// concurrent process (visor and dmsg server on one host) sees either no
// file or a complete one.
func openMapped(dir string) (*geoip2.Reader, error) {
	crc, size, ok := gzTrailer(embeddedGz)
	if !ok {
		return nil, fmt.Errorf("geoip: embedded database is not a gzip member")
	}
	path := mappedPath(dir, crc)
	if fi, err := os.Stat(path); err != nil || fi.Size() != size {
		if err := inflateTo(dir, path, size); err != nil {
			return nil, err
		}
	}
	return geoip2.Open(path)
}

func inflateTo(dir, path string, want int64) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("geoip: cache dir: %w", err)
	}
	f, err := os.CreateTemp(dir, "GeoLite2-City-*.tmp")
	if err != nil {
		return fmt.Errorf("geoip: cache file: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp) //nolint:errcheck // gone after a successful rename
	zr, err := newEmbeddedGzipReader()
	if err != nil {
		f.Close() //nolint:errcheck,gosec
		return err
	}
	n, err := io.Copy(f, zr) //nolint:gosec // G110: the source is the build's own embedded blob, not remote input
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("geoip: inflate to cache: %w", err)
	}
	if n != want {
		return fmt.Errorf("geoip: inflated %d bytes, gzip trailer says %d", n, want)
	}
	_ = os.Remove(path) //nolint:errcheck // Windows cannot rename over an existing (stale) file
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("geoip: publish cache file: %w", err)
	}
	return nil
}

func newEmbeddedGzipReader() (*gzip.Reader, error) {
	zr, err := gzip.NewReader(bytesReader(embeddedGz))
	if err != nil {
		return nil, fmt.Errorf("geoip: embedded database: %w", err)
	}
	return zr, nil
}
