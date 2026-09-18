// Package skysocks — the in-order writer streams the FRONTIER chunk.
//
// The chunk the browser is waiting on must reach it as its bytes arrive: the
// old whole-chunk barrier is what made a mid-transfer cut cost a whole chunk
// (rig 2026-09-16, mux-standby-8: detection at +1.8 s, first byte at +7.0 s).
package skysocks

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// rsOriginHook paces one range response: prefix bytes first, then a pause, then
// either the remainder or — with cut — an abrupt close of the connection
// mid-body. attempt counts the requests seen for that same start offset.
type rsOriginHook func(start, end int64, attempt int) (prefix int64, pause time.Duration, cut bool)

// rsParseRange parses a "bytes=a-b" request range against a known size.
func rsParseRange(h string, size int64) (start, end int64, ok bool) {
	if !strings.HasPrefix(h, "bytes=") {
		return 0, 0, false
	}
	a, b, found := strings.Cut(strings.TrimPrefix(h, "bytes="), "-")
	if !found {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(a, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	end = size - 1
	if b != "" {
		if end, err = strconv.ParseInt(b, 10, 64); err != nil {
			return 0, 0, false
		}
	}
	if end >= size {
		end = size - 1
	}
	if start < 0 || start > end {
		return 0, 0, false
	}
	return start, end, true
}

// rsRangeOrigin is a minimal HTTP/1.1 range origin serving blob, with hook
// deciding how each range is paced. A raw listener rather than httptest: the
// tests need to hold back — or cut — the tail of one specific range, which the
// net/http response writer will not do.
func rsRangeOrigin(t *testing.T, blob []byte, hook rsOriginHook) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("origin listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() }) //nolint:errcheck,gosec
	var mu sync.Mutex
	attempts := map[int64]int{}
	size := int64(len(blob))

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close() //nolint:errcheck,gosec
				br := bufio.NewReader(conn)
				for {
					req, err := http.ReadRequest(br)
					if err != nil {
						return
					}
					start, end, ok := rsParseRange(req.Header.Get("Range"), size)
					if !ok {
						_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nAccept-Ranges: bytes\r\nContent-Length: %d\r\n\r\n", size) //nolint:errcheck
						_, _ = conn.Write(blob)                                                                                 //nolint:errcheck
						return
					}
					mu.Lock()
					attempts[start]++
					n := attempts[start]
					mu.Unlock()
					var (
						prefix int64
						pause  time.Duration
						cut    bool
					)
					if hook != nil {
						prefix, pause, cut = hook(start, end, n)
					}
					body := blob[start : end+1]
					if prefix <= 0 || prefix > int64(len(body)) {
						prefix = int64(len(body))
					}
					hdr := fmt.Sprintf("HTTP/1.1 206 Partial Content\r\nAccept-Ranges: bytes\r\nEtag: \"blobv1\"\r\n"+
						"Content-Range: bytes %d-%d/%d\r\nContent-Length: %d\r\n\r\n", start, end, size, len(body))
					if _, err := io.WriteString(conn, hdr); err != nil {
						return
					}
					if _, err := conn.Write(body[:prefix]); err != nil {
						return
					}
					if pause > 0 {
						time.Sleep(pause)
					}
					if cut {
						return // FIN mid-body: the chunk must resume, not restart
					}
					if prefix < int64(len(body)) {
						if _, err := conn.Write(body[prefix:]); err != nil {
							return
						}
					}
				}
			}(conn)
		}
	}()

	return ln.Addr().String()
}

// rsBlob is a deterministic test object.
func rsBlob(size int) []byte {
	b := make([]byte, size)
	for i := range b {
		b[i] = byte(i*29 + 5)
	}
	return b
}

// rsReadHeadThenOne reads exactly head bytes, then ONE more byte, returning how
// long that one byte took to arrive — the time-to-first-byte of the chunk after
// the one the browser has just finished.
func rsReadHeadThenOne(t *testing.T, body io.Reader, head int64) (time.Duration, []byte) {
	t.Helper()
	buf := make([]byte, head+1)
	if _, err := io.ReadFull(body, buf[:head]); err != nil {
		t.Fatalf("read chunk0: %v", err)
	}
	start := time.Now()
	if _, err := io.ReadFull(body, buf[head:]); err != nil {
		t.Fatalf("read first byte of the frontier chunk: %v", err)
	}

	return time.Since(start), buf
}

// TestRangeSplitFrontierStreamsBeforeChunkCompletes: the origin holds back the
// TAIL of the frontier chunk. Its already-received prefix must reach the browser
// at once — before the chunk completes — instead of waiting out the hold behind
// the old whole-chunk barrier.
func TestRangeSplitFrontierStreamsBeforeChunkCompletes(t *testing.T) {
	const (
		blobSize = 4 << 20
		chunkSz  = int64(1 << 20) // also caps the probe chunk, so chunk0 = 1 MiB
		prefix   = int64(256 << 10)
		hold     = 1200 * time.Millisecond
	)
	blob := rsBlob(blobSize)
	want := sha256.Sum256(blob)

	origin := rsRangeOrigin(t, blob, func(start, _ int64, _ int) (int64, time.Duration, bool) {
		if start == chunkSz { // the frontier chunk: prefix now, tail after the hold
			return prefix, hold, false
		}
		return 0, 0, false
	})

	proxy := newRSTestClient(t, origin, 2, chunkSz)
	conn, resp := socks5GetStreaming(t, proxy)
	defer conn.Close()      //nolint:errcheck
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	ttfb, head := rsReadHeadThenOne(t, resp.Body, chunkSz)
	t.Logf("frontier ttfb (tail held %v): %v", hold, ttfb)
	if ttfb > hold/3 {
		t.Fatalf("first byte of the held chunk took %v — the writer waited for the whole chunk (tail held %v)", ttfb, hold)
	}

	rest, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	got := append(head, rest...) //nolint:gocritic // head is a fresh buffer
	if len(got) != blobSize {
		t.Fatalf("body len = %d, want %d", len(got), blobSize)
	}
	if sha256.Sum256(got) != want {
		t.Fatal("reassembled body does not match the origin (streaming corrupted the object)")
	}
}

// TestRangeSplitFrontierResumesAfterMidStreamCut: the frontier chunk's stream
// dies AFTER part of it has already been written to the browser. The refetch
// must resume from the byte after the last one streamed — never re-emit it,
// never leave a gap — and the object must still hash.
func TestRangeSplitFrontierResumesAfterMidStreamCut(t *testing.T) {
	const (
		blobSize = 4 << 20
		chunkSz  = int64(1 << 20)
		prefix   = int64(300 << 10)
	)
	blob := rsBlob(blobSize)
	want := sha256.Sum256(blob)

	var (
		mu          sync.Mutex
		resumeStart int64 = -1
		resumeAt    time.Time
	)
	origin := rsRangeOrigin(t, blob, func(start, _ int64, attempt int) (int64, time.Duration, bool) {
		if start == chunkSz && attempt == 1 {
			return prefix, 0, true // cut the frontier chunk mid-body
		}
		if start > chunkSz && start < 2*chunkSz {
			mu.Lock()
			if resumeStart < 0 {
				resumeStart, resumeAt = start, time.Now()
			}
			mu.Unlock()
		}
		return 0, 0, false
	})

	proxy := newRSTestClient(t, origin, 2, chunkSz)
	conn, resp := socks5GetStreaming(t, proxy)
	defer conn.Close()      //nolint:errcheck
	defer resp.Body.Close() //nolint:errcheck

	_, head := rsReadHeadThenOne(t, resp.Body, chunkSz)
	streamed := time.Now()
	rest, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	got := append(head, rest...) //nolint:gocritic // head is a fresh buffer

	mu.Lock()
	rStart, rAt := resumeStart, resumeAt
	mu.Unlock()
	if rStart < 0 {
		t.Fatal("the cut chunk was never refetched")
	}
	if rStart <= chunkSz {
		t.Fatalf("refetch asked for byte %d — the chunk restarted instead of resuming past the streamed prefix", rStart)
	}
	if rStart > chunkSz+prefix {
		t.Fatalf("refetch asked for byte %d, past the %d bytes the origin actually sent — a gap", rStart, chunkSz+prefix)
	}
	if !streamed.Before(rAt) {
		t.Fatalf("the browser's first byte of the frontier chunk (%v) did not precede the refetch (%v) — the prefix was buffered, not streamed", streamed, rAt)
	}
	t.Logf("cut at %d bytes into the frontier chunk; refetch resumed at byte %d", rStart-chunkSz, rStart)

	if len(got) != blobSize {
		t.Fatalf("body len = %d, want %d (re-emitted or lost bytes)", len(got), blobSize)
	}
	if sha256.Sum256(got) != want {
		t.Fatal("reassembled body does not match the origin after the mid-stream cut")
	}
}

// TestWriteInOrderRescueResumesAfterStreamedPrefix pins the writer's contract
// without a network: a chunk that streams j bytes and then fails permanently
// must hand the sequential rescue start+j, so the rescued tail neither repeats
// the streamed prefix nor skips a byte.
func TestWriteInOrderRescueResumesAfterStreamedPrefix(t *testing.T) {
	const (
		chunk = int64(16)
		total = int64(64) // chunk0 [0,16) is not part of this call
		got0  = int64(6)  // bytes the frontier chunk streams before it dies
	)
	c := rescueClient(chunk)
	conn, mu, out := collectConn(t)

	pattern := func(start, end int64) []byte {
		b := make([]byte, end-start+1)
		for i := range b {
			b[i] = byte((start + int64(i)) % 251) //nolint:gosec // %251 bounds the value
		}
		return b
	}
	fetch := func(start, end int64, prog rsProgress) ([]byte, error) {
		buf := pattern(start, end)
		if start == chunk {
			prog(buf, got0) // the browser gets these bytes, then the tunnel dies
			return nil, errors.New("tunnel closed under the fetch")
		}
		return buf, nil
	}
	var rescuedFrom int64 = -1
	rescue := func(w net.Conn, start int64) (int64, error) {
		if rescuedFrom < 0 {
			rescuedFrom = start
		}
		n, err := w.Write(pattern(start, total-1))
		return int64(n), err
	}

	c.startChunkFetchesFrom(chunk, total, chunk, fetch).writeInOrder(conn, rescue)

	if rescuedFrom != chunk+got0 {
		t.Fatalf("rescue resumed at %d, want %d (start + the %d streamed bytes)", rescuedFrom, chunk+got0, got0)
	}
	want := pattern(chunk, total-1)
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		ok := bytes.Equal(out.Bytes(), want)
		n := out.Len()
		mu.Unlock()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("delivered %d bytes, want the %d contiguous bytes of [%d,%d)", n, len(want), chunk, total)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
