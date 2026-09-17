// Package skysocks — range-split failover when a tunnel dies mid-download.
package skysocks

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0magnet/yamux"
)

// slowWriter paces a copy so a chunk stays in flight long enough to be killed
// mid-transfer.
type slowWriter struct {
	w     io.Writer
	chunk int
	pause time.Duration
}

func (s slowWriter) Write(p []byte) (int, error) {
	written := 0
	for written < len(p) {
		end := written + s.chunk
		if end > len(p) {
			end = len(p)
		}
		n, err := s.w.Write(p[written:end])
		written += n
		if err != nil {
			return written, err
		}
		time.Sleep(s.pause)
	}

	return written, nil
}

// rsSlowExit is rsFakeExit paced down: it reaches the same range-capable backend
// but delivers ~320 KB/s, so a range chunk routed to it is reliably still in
// flight — mid-body — when the test kills the tunnel underneath it. accepted
// counts the streams the client opened on it.
func rsSlowExit(conn net.Conn, backendAddr string, accepted *atomic.Int64) {
	sess, err := yamux.Server(conn, yamux.DefaultConfig())
	if err != nil {
		return
	}
	for {
		st, err := sess.AcceptStream()
		if err != nil {
			return
		}
		accepted.Add(1)
		go func(st net.Conn) {
			defer st.Close() //nolint:errcheck
			hdr := make([]byte, 2)
			if _, err := io.ReadFull(st, hdr); err != nil || hdr[0] != 0x05 {
				return
			}
			if _, err := io.ReadFull(st, make([]byte, int(hdr[1]))); err != nil {
				return
			}
			if _, err := st.Write([]byte{0x05, 0x00}); err != nil {
				return
			}
			rh := make([]byte, 4)
			if _, err := io.ReadFull(st, rh); err != nil {
				return
			}
			switch rh[3] {
			case 0x01:
				_, _ = io.ReadFull(st, make([]byte, 4)) //nolint:errcheck,gosec
			case 0x03:
				l := make([]byte, 1)
				_, _ = io.ReadFull(st, l)                       //nolint:errcheck,gosec
				_, _ = io.ReadFull(st, make([]byte, int(l[0]))) //nolint:errcheck,gosec
			case 0x04:
				_, _ = io.ReadFull(st, make([]byte, 16)) //nolint:errcheck,gosec
			}
			_, _ = io.ReadFull(st, make([]byte, 2)) //nolint:errcheck,gosec
			if _, err := st.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
				return
			}
			be, err := net.Dial("tcp", backendAddr)
			if err != nil {
				return
			}
			defer be.Close() //nolint:errcheck
			done := make(chan struct{}, 2)
			go func() { _, _ = io.Copy(be, st); done <- struct{}{} }() //nolint:errcheck,gosec
			go func() {                                                //nolint:errcheck,gosec
				_, _ = io.Copy(slowWriter{w: st, chunk: 32 << 10, pause: 100 * time.Millisecond}, be)
				done <- struct{}{}
			}()
			<-done
		}(st)
	}
}

// connPair returns the two ends of a local TCP connection.
func connPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close() //nolint:errcheck
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept() //nolint:errcheck
		accepted <- c
	}()
	a, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial pair: %v", err)
	}
	return a, <-accepted
}

// newRSTwoTunnelClient builds a range-splitting client over TWO tunnels:
// tunnel A reaches a real range-capable backend at full speed, tunnel B is the
// paced rsSlowExit above. It returns the proxy address, the client, tunnel B's session (to kill)
// and the count of streams B accepted.
func newRSTwoTunnelClient(t *testing.T, backendAddr string, conc int, chunk int64) (string, *Client, *yamux.Session, *atomic.Int64) {
	t.Helper()
	aCli, aExit := connPair(t)
	bCli, bExit := connPair(t)
	go rsFakeExit(t, aExit, backendAddr)
	bAccepted := new(atomic.Int64)
	go rsSlowExit(bExit, backendAddr, bAccepted)

	client, err := NewMultiClient([]net.Conn{aCli, bCli}, nil)
	if err != nil {
		t.Fatalf("new multi client: %v", err)
	}
	client.SetRangeSplit(true, conc, chunk)
	t.Cleanup(func() { _ = client.Close() }) //nolint:errcheck

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	proxyAddr := probe.Addr().String()
	probe.Close()                                        //nolint:errcheck,gosec
	go func() { _ = client.ListenAndServe(proxyAddr) }() //nolint:errcheck
	for i := 0; i < 200; i++ {
		if cc, err := net.Dial("tcp", proxyAddr); err == nil {
			cc.Close() //nolint:errcheck,gosec
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The readiness dial above leaves a stream on tunnel A for a moment; wait for
	// it to drain so the browser connection itself is striped onto A (the pick is
	// least-loaded, ties to the lowest index) and the CHUNKS are what land on B.
	sessions := client.snapshotSessions()
	for i := 0; i < 300; i++ {
		if sessions[0].NumStreams() == 0 && sessions[1].NumStreams() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return proxyAddr, client, sessions[1], bAccepted
}

// TestRangeSplitChunkFailoverOnTunnelLoss is the degradation case measured live:
// a two-tunnel client is mid-download when one tunnel's transport goes away, so
// the chunks in flight on it can never complete. Killing that tunnel's session
// must fail those chunks AT ONCE and refetch them on the surviving tunnel — the
// browser's next byte within a second — instead of waiting out the per-chunk
// idle/probe deadlines and then degrading the whole remainder to the single
// sequential rescue stream.
func TestRangeSplitChunkFailoverOnTunnelLoss(t *testing.T) {
	const blobSize = 8 << 20
	blob := make([]byte, blobSize)
	for i := range blob {
		blob[i] = byte(i*37 + 11)
	}
	want := sha256.Sum256(blob)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Etag", "\"blobv1\"")
		http.ServeContent(w, r, "blob.bin", time.Unix(0, 0), bytes.NewReader(blob))
	}))
	defer backend.Close()

	// 512 KiB chunks over 8 MiB → 16 chunks across 4 concurrent streams, so
	// several are in flight on the doomed tunnel at any moment.
	proxy, client, bSess, bAccepted := newRSTwoTunnelClient(t, backend.Listener.Addr().String(), 4, 512<<10)

	// Park a stream on the doomed tunnel so the BROWSER connection is striped onto
	// the healthy one (the pick is least-loaded). The browser's own exit stream is
	// bound to its tunnel for the life of the connection and is not a range chunk;
	// this test is about the chunks.
	park, err := bSess.Open()
	if err != nil {
		t.Fatalf("park stream: %v", err)
	}

	conn, resp := socks5GetStreaming(t, proxy, "/blob.bin")
	defer conn.Close() //nolint:errcheck
	defer resp.Body.Close()
	park.Close() //nolint:errcheck,gosec

	var (
		mu  sync.Mutex
		got []byte
	)
	readErr := make(chan error, 1)
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				mu.Lock()
				got = append(got, buf[:n]...)
				mu.Unlock()
			}
			if err != nil {
				if err == io.EOF {
					err = nil
				}
				readErr <- err
				return
			}
		}
	}()

	// Wait until chunks are genuinely in flight on the doomed tunnel.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		n := len(got)
		mu.Unlock()
		// Some bytes delivered, and at least two chunk streams live on the
		// doomed tunnel — so killing it now kills fetches mid-body.
		if n > 0 && n < blobSize && bAccepted.Load() >= 2 && bSess.NumStreams() > 0 {
			break
		}
	}
	mu.Lock()
	before := len(got)
	mu.Unlock()
	if before == 0 || before >= blobSize {
		t.Fatalf("no chunk was in flight on the doomed tunnel (delivered %d/%d)", before, blobSize)
	}
	acceptedAtCut := bAccepted.Load()
	t.Logf("cut at %d/%d bytes, tunnel B accepted %d stream(s), %d still open", before, blobSize, acceptedAtCut, bSess.NumStreams())

	// Kill tunnel B — the live equivalent of its last transport being removed.
	cut := time.Now()
	_ = bSess.Close() //nolint:errcheck

	// (1)+(2): the next browser byte must arrive at once, not after a deadline.
	next := time.Duration(-1)
	for time.Since(cut) < 30*time.Second {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n > before {
			next = time.Since(cut)
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if next < 0 {
		t.Fatalf("no byte reached the browser in 30s after the tunnel died")
	}
	t.Logf("first byte after the cut: %v", next)

	// (3): the download still completes, byte-identical.
	select {
	case err := <-readErr:
		if err != nil {
			t.Fatalf("body read: %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("download never finished after the tunnel died")
	}
	mu.Lock()
	final := got
	mu.Unlock()
	if len(final) != blobSize {
		t.Fatalf("body len = %d, want %d (TRUNCATED)", len(final), blobSize)
	}
	if sha256.Sum256(final) != want {
		t.Fatal("reassembled body does not match origin after the tunnel died")
	}

	// No chunk may be issued to the dead tunnel afterwards.
	if n := bAccepted.Load(); n != acceptedAtCut {
		t.Fatalf("tunnel B accepted %d stream(s) after it was closed", n-acceptedAtCut)
	}
	for i := 0; i < 64; i++ {
		if s := client.pickSessionFor(pickRecv); s == bSess {
			t.Fatal("pickSessionFor returned the closed tunnel")
		}
	}

	if next > time.Second {
		t.Fatalf("first byte after the tunnel died took %v, want < 1s", next)
	}
	// And it must not have SLEPT: a chunk whose tunnel died is refetched at once,
	// so the gap stays well inside one retry backoff rather than a multiple of it.
	if next >= rsChunkRetryBackoff {
		t.Fatalf("first byte after the tunnel died took %v — the refetch backed off (>= %v)", next, rsChunkRetryBackoff)
	}
}

// socks5GetStreaming is socks5Get without draining the body: the caller reads it
// incrementally so it can time the bytes.
func socks5GetStreaming(t *testing.T, proxyAddr, path string) (net.Conn, *http.Response) {
	t.Helper()
	const host = "example.com"
	c, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	if _, err := c.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatalf("greeting: %v", err)
	}
	if _, err := io.ReadFull(c, make([]byte, 2)); err != nil {
		t.Fatalf("method reply: %v", err)
	}
	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))} //nolint:gosec // test host is short
	req = append(req, host...)
	req = append(req, 0x00, 0x50)
	if _, err := c.Write(req); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := readSocks5Reply(c); err != nil {
		t.Fatalf("connect reply: %v", err)
	}
	if _, err := fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: %s\r\nUser-Agent: itest\r\n\r\n", path, host); err != nil {
		t.Fatalf("get: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return c, resp
}
