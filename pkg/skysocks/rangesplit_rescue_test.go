// Package skysocks range-split sequential-rescue tests: a chunk that exhausts
// its retries must degrade to sequential streaming of the remainder — never a
// clean-looking short body (the silent 4MiB truncation observed live).
package skysocks

import (
	"bytes"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// collectConn drains one end of a net.Pipe into a buffer so the writer side
// (streamRemainingChunks) never blocks.
func collectConn(t *testing.T) (net.Conn, *sync.Mutex, *bytes.Buffer) {
	t.Helper()
	a, b := net.Pipe()
	var mu sync.Mutex
	var buf bytes.Buffer
	go func() {
		tmp := make([]byte, 4096)
		for {
			n, err := b.Read(tmp)
			if n > 0 {
				mu.Lock()
				buf.Write(tmp[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		a.Close() //nolint:errcheck,gosec
		b.Close() //nolint:errcheck,gosec
	})
	return a, &mu, &buf
}

// rescueClient builds a minimal Client whose chunkSize/concurrency drive
// streamRemainingChunks directly (no network).
func rescueClient(chunkSize int64) *Client {
	c := &Client{closeC: make(chan struct{})}
	c.rs = rangeSplitConfig{enabled: true, concurrency: 2, chunkSize: chunkSize}
	return c
}

// TestStreamRemainingChunks_RescueCompletesDownload: chunk 2 fails permanently;
// the rescue streams the remainder (resuming across a partial first attempt),
// so the client receives every byte of [chunkSize, total).
func TestStreamRemainingChunks_RescueCompletesDownload(t *testing.T) {
	const chunk = 8
	const total = 40 // chunk0 (8B, not part of this call) + 4 chunks of 8
	c := rescueClient(chunk)
	conn, mu, got := collectConn(t)

	pattern := func(start, end int64) []byte {
		out := make([]byte, end-start+1)
		for i := range out {
			out[i] = byte((start + int64(i)) % 251) //nolint:gosec // %251 bounds the value
		}
		return out
	}

	fetch := func(start, end int64) ([]byte, error) {
		if start == 2*chunk { // third chunk (bytes 16-23) always fails
			return nil, errors.New("chunk permanently unavailable")
		}
		return pattern(start, end), nil
	}
	// Rescue: first attempt delivers 5 bytes then errors; the retry (resumed at
	// the advanced offset) delivers the rest.
	attempt := 0
	rescue := func(w net.Conn, start int64) (int64, error) {
		attempt++
		if attempt == 1 {
			p := pattern(start, start+4)
			n, _ := w.Write(p) //nolint:errcheck // the partial write IS the scenario
			return int64(n), errors.New("stream died mid-tail")
		}
		p := pattern(start, total-1)
		n, err := w.Write(p)
		return int64(n), err
	}

	c.streamRemainingChunks(conn, total, fetch, rescue)

	want := pattern(chunk, total-1) // [chunkSize, total) in order
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		ok := bytes.Equal(got.Bytes(), want)
		n := got.Len()
		mu.Unlock()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("rescued stream = %d bytes, want %d contiguous bytes", n, len(want))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if attempt != 2 {
		t.Errorf("rescue attempts = %d, want 2 (partial then resumed)", attempt)
	}
}

// TestStreamRemainingChunks_NoRescueKeepsTruncation: with a nil rescue the old
// behavior stands — the stream ends at the failed chunk boundary.
func TestStreamRemainingChunks_NoRescueKeepsTruncation(t *testing.T) {
	const chunk = 8
	const total = 32
	c := rescueClient(chunk)
	conn, mu, got := collectConn(t)

	fetch := func(start, end int64) ([]byte, error) {
		if start == 2*chunk {
			return nil, errors.New("gone")
		}
		return make([]byte, end-start+1), nil
	}
	c.streamRemainingChunks(conn, total, fetch, nil)

	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	n := got.Len()
	mu.Unlock()
	if n != chunk { // only the second chunk (bytes 8-15) made it
		t.Errorf("truncation point = %d bytes, want %d", n, chunk)
	}
}

// TestStreamRemainingChunks_RescueGivesUpWithoutProgress: a rescue that never
// delivers a byte is abandoned after rsRescueAttempts tries.
func TestStreamRemainingChunks_RescueGivesUpWithoutProgress(t *testing.T) {
	const chunk = 8
	const total = 32
	c := rescueClient(chunk)
	conn, _, _ := collectConn(t)

	fetch := func(start, end int64) ([]byte, error) {
		return nil, errors.New("all chunks fail")
	}
	calls := 0
	rescue := func(_ net.Conn, _ int64) (int64, error) {
		calls++
		return 0, errors.New("still dead")
	}
	done := make(chan struct{})
	go func() {
		c.streamRemainingChunks(conn, total, fetch, rescue)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("streamRemainingChunks did not return — zero-progress rescue must give up")
	}
	if calls != rsRescueAttempts {
		t.Errorf("zero-progress rescue attempts = %d, want %d", calls, rsRescueAttempts)
	}
}

// TestCopyWithIdleTimeout_ProgressBeatsIdle: a source that trickles slower than
// the idle window per chunk still completes (progress refreshes the deadline),
// and the byte count is exact.
func TestCopyWithIdleTimeout_ProgressBeatsIdle(t *testing.T) {
	src, feeder := net.Pipe()
	defer src.Close()    //nolint:errcheck
	defer feeder.Close() //nolint:errcheck
	go func() {
		for i := 0; i < 4; i++ {
			time.Sleep(30 * time.Millisecond)
			feeder.Write([]byte{byte(i), byte(i), byte(i)}) //nolint:errcheck,gosec
		}
	}()
	var out bytes.Buffer
	n, err := copyWithIdleTimeout(&out, src, src, 12, 100*time.Millisecond)
	if err != nil || n != 12 {
		t.Fatalf("copyWithIdleTimeout = (%d, %v), want (12, nil)", n, err)
	}
}

// TestCopyWithIdleTimeout_SilenceFails: a source that stops sending fails within
// one idle window, reporting the bytes delivered so far.
func TestCopyWithIdleTimeout_SilenceFails(t *testing.T) {
	src, feeder := net.Pipe()
	defer src.Close()    //nolint:errcheck
	defer feeder.Close() //nolint:errcheck
	go func() {
		feeder.Write([]byte("abcde")) //nolint:errcheck,gosec
		// then silence — never close, never send
	}()
	var out bytes.Buffer
	start := time.Now()
	n, err := copyWithIdleTimeout(&out, src, src, 100, 80*time.Millisecond)
	if err == nil {
		t.Fatal("silent source must fail the copy")
	}
	if n != 5 {
		t.Errorf("delivered bytes = %d, want 5", n)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("silence detection took %v, want ~one idle window", elapsed)
	}
	if !errors.Is(err, io.EOF) && !isTimeout(err) {
		t.Logf("failure error (acceptable): %v", err)
	}
}

// isTimeout reports whether err is a net timeout.
func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
