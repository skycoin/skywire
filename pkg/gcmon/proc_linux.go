//go:build linux

package gcmon

import (
	"bytes"
	"os"
	"strconv"
)

// readProc returns the resident set size in bytes and the thread count from
// /proc/self/status. Zero means unknown. buf is reused between calls.
func readProc(buf *[]byte) (rss, threads uint64) {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0, 0
	}
	defer f.Close() //nolint:errcheck
	if cap(*buf) < 4096 {
		*buf = make([]byte, 4096)
	}
	b := (*buf)[:cap(*buf)]
	n, _ := f.Read(b) //nolint:errcheck
	b = b[:n]
	for len(b) > 0 {
		line := b
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			line, b = b[:i], b[i+1:]
		} else {
			b = nil
		}
		switch {
		case bytes.HasPrefix(line, []byte("VmRSS:")):
			rss = procNumber(line[len("VmRSS:"):]) * 1024
		case bytes.HasPrefix(line, []byte("Threads:")):
			threads = procNumber(line[len("Threads:"):])
		}
	}
	return rss, threads
}

func procNumber(b []byte) uint64 {
	b = bytes.TrimSpace(b)
	if i := bytes.IndexByte(b, ' '); i >= 0 {
		b = b[:i]
	}
	n, err := strconv.ParseUint(string(b), 10, 64)
	if err != nil {
		return 0
	}
	return n
}
