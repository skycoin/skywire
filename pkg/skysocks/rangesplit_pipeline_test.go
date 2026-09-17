// Package skysocks range-split latency tests: one chunk must cost ONE round
// trip, the parallel fetches must start while chunk0 is still draining, and a
// finished fetch must admit the next one without waiting for its own in-order
// write.
package skysocks

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestExitConnectPipelined_OneRoundTrip: the greeting, the CONNECT request and
// the ranged GET must ALL reach the exit before it sends a single reply byte.
// The fake exit here replies only after reading the whole pipelined block, so a
// handshake that blocked on the method reply would deadlock instead of passing.
func TestExitConnectPipelined_OneRoundTrip(t *testing.T) {
	const host = "example.com"
	payload := []byte("GET /blob.bin HTTP/1.1\r\nHost: example.com\r\nRange: bytes=100-199\r\n\r\n")
	want, err := buildSocks5Connect(host, 80)
	if err != nil {
		t.Fatalf("buildSocks5Connect: %v", err)
	}
	want = append(want, payload...)

	cli, srv := net.Pipe()
	defer cli.Close() //nolint:errcheck
	defer srv.Close() //nolint:errcheck

	got := make(chan []byte, 1)
	go func() {
		buf := make([]byte, len(want))
		if _, err := io.ReadFull(srv, buf); err != nil {
			got <- nil
			return
		}
		got <- buf
		// Only NOW do the replies go back: method selection, then CONNECT.
		_, _ = srv.Write([]byte{0x05, 0x00})                               //nolint:errcheck,gosec
		_, _ = srv.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) //nolint:errcheck,gosec
	}()

	done := make(chan error, 1)
	go func() {
		done <- (&Client{}).exitConnectPipelined(cli, host, 80, payload)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("exitConnectPipelined: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("exitConnectPipelined blocked on a reply before writing the request — not pipelined")
	}

	sent := <-got
	if !bytes.Equal(sent, want) {
		t.Fatalf("exit received %d bytes, want the greeting+CONNECT+GET block (%d bytes)", len(sent), len(want))
	}
}

// TestExitConnect_StillBlocksForMethodReply pins the non-pipelined form used by
// the sequential rescue: greeting first, method reply, and only then CONNECT.
func TestExitConnect_StillBlocksForMethodReply(t *testing.T) {
	cli, srv := net.Pipe()
	defer cli.Close() //nolint:errcheck
	defer srv.Close() //nolint:errcheck

	connectSeen := make(chan int, 1)
	go func() {
		greeting := make([]byte, len(socks5Greeting))
		if _, err := io.ReadFull(srv, greeting); err != nil {
			connectSeen <- -1
			return
		}
		if _, err := srv.Write([]byte{0x05, 0x00}); err != nil {
			connectSeen <- -1
			return
		}
		rest := make([]byte, 5+len("example.com")+2)
		n, _ := io.ReadFull(srv, rest)                                     //nolint:errcheck
		_, _ = srv.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) //nolint:errcheck,gosec
		connectSeen <- n
	}()

	if err := (&Client{}).exitConnect(cli, "example.com", 80); err != nil {
		t.Fatalf("exitConnect: %v", err)
	}
	if n := <-connectSeen; n != 5+len("example.com")+2 {
		t.Fatalf("CONNECT bytes after the method reply = %d, want %d", n, 5+len("example.com")+2)
	}
}

// TestRangeSplitParallelFetchesStartDuringChunk0: the parallel range fetches must
// be in flight while chunk0 is still being written to the browser. The test
// client reads the response HEADERS and then stops reading, so the proxy's
// chunk0 copy blocks part-way; a range request for a later chunk must still
// reach the origin.
func TestRangeSplitParallelFetchesStartDuringChunk0(t *testing.T) {
	const chunkSize = 8 << 20
	const blobSize = 24 << 20
	blob := make([]byte, blobSize)
	for i := range blob {
		blob[i] = byte(i * 17)
	}

	ranges := make(chan string, 64)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case ranges <- r.Header.Get("Range"):
		default:
		}
		w.Header().Set("Etag", "\"blobv1\"")
		http.ServeContent(w, r, "blob.bin", time.Unix(0, 0), bytes.NewReader(blob))
	}))
	defer backend.Close()

	proxy := newRSTestClient(t, backend.Listener.Addr().String(), 4, chunkSize)

	// Reads the headers only; the body stays unread, so the proxy stalls part
	// way through writing chunk0 into the socket.
	resp := socks5Get(t, proxy, "/blob.bin")
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	deadline := time.After(10 * time.Second)
	for {
		select {
		case rng := <-ranges:
			// chunk0's probe is "bytes=0-...". Anything else is a parallel chunk.
			if rng != "" && !bytes.HasPrefix([]byte(rng), []byte("bytes=0-")) {
				return // a later chunk is already being fetched: success
			}
		case <-deadline:
			t.Fatal("no parallel chunk fetch started while chunk0 was still draining to the browser")
		}
	}
}

// TestChunkFetches_AdmitOnFetchCompletion: with the in-order write stalled on
// chunk 1, the fetch of chunk 1+concurrency must still be admitted — admission
// follows fetch completion, not delivery. Before this, a browser that stopped
// reading froze every later chunk's stream setup too, forcing a second wave of
// per-chunk round trips once it resumed.
func TestChunkFetches_AdmitOnFetchCompletion(t *testing.T) {
	const chunk = 8
	const total = chunk * 6  // chunk0 plus 5 fetched chunks
	c := rescueClient(chunk) // concurrency 2

	// A pipe nobody ever reads: the first conn.Write blocks until cleanup.
	blocked, peer := net.Pipe()
	t.Cleanup(func() {
		blocked.Close() //nolint:errcheck,gosec
		peer.Close()    //nolint:errcheck,gosec
	})

	started := make(chan int64, 16)
	fetch := func(start, end int64) ([]byte, error) {
		started <- start
		return make([]byte, end-start+1), nil
	}
	go c.streamRemainingChunks(blocked, total, fetch, nil)

	// chunk 1 is the first fetched chunk (offset chunk); with concurrency 2,
	// chunk 1+2 sits at offset 3*chunk.
	const wantStart = 3 * chunk
	deadline := time.After(5 * time.Second)
	for {
		select {
		case s := <-started:
			if s == wantStart {
				return
			}
		case <-deadline:
			t.Fatalf("chunk at offset %d was never fetched — admission still waits for the in-order write", wantStart)
		}
	}
}
