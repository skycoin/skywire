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
	"testing"
	"time"

	"github.com/0magnet/yamux"
)

// rsGatedExit is a fake exit whose SOCKS5 CONNECT reply is withheld until
// release fires (or, when refuse is set, is a refusal followed by a close). It
// makes the exit round trip that the CONNECT reply costs observable: whatever
// the browser receives before release is something the proxy produced locally.
func rsGatedExit(t *testing.T, conn net.Conn, backendAddr string, release <-chan struct{}, refuse bool) {
	t.Helper()
	sess, err := yamux.Server(conn, yamux.DefaultConfig())
	if err != nil {
		return
	}
	for {
		st, err := sess.AcceptStream()
		if err != nil {
			return
		}
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
			_, _ = io.ReadFull(st, make([]byte, 2)) //nolint:errcheck,gosec // port
			if refuse {
				// REP=0x05 (connection refused), then the stream dies — exactly
				// what an exit that cannot reach the origin does.
				_, _ = st.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) //nolint:errcheck,gosec
				return
			}
			if release != nil {
				<-release
			}
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
			go func() { _, _ = io.Copy(st, be); done <- struct{}{} }() //nolint:errcheck,gosec
			<-done
		}(st)
	}
}

// newRSGatedClient wires a Client to rsGatedExit over a TCP socketpair and
// returns the proxy's SOCKS address.
func newRSGatedClient(t *testing.T, backendAddr string, release <-chan struct{}, refuse bool) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close() //nolint:errcheck
	dialed := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept() //nolint:errcheck
		dialed <- c
	}()
	cliConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial pair: %v", err)
	}
	go rsGatedExit(t, <-dialed, backendAddr, release, refuse)

	client, err := NewClient(cliConn, nil)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	client.SetRangeSplit(true, 4, 1<<20)
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
	return proxyAddr
}

// rsBrowserConnect performs the browser half of a SOCKS5 CONNECT to
// example.com:80 through proxyAddr, stopping just before the CONNECT reply.
func rsBrowserConnect(t *testing.T, proxyAddr string) net.Conn {
	t.Helper()
	const host = "example.com"
	c, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck
	if _, err := c.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatalf("greeting: %v", err)
	}
	if _, err := io.ReadFull(c, make([]byte, 2)); err != nil {
		t.Fatalf("method reply: %v", err)
	}
	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))} //nolint:gosec // test host is short
	req = append(req, host...)
	req = append(req, 0x00, 0x50) // port 80
	if _, err := c.Write(req); err != nil {
		t.Fatalf("connect: %v", err)
	}
	return c
}

// TestRangeSplitAnswersConnectOptimistically pins the prelude fix: the browser's
// CONNECT is answered from the proxy, so the browser can send its GET while the
// exit's own CONNECT reply is still in flight. The exit here will not answer
// CONNECT until release is closed, which happens only AFTER the browser has both
// its reply and its GET on the wire — on the serial ordering this read blocks
// for a whole exit round trip and the deadline fires.
func TestRangeSplitAnswersConnectOptimistically(t *testing.T) {
	const blobSize = 4 << 20
	blob := make([]byte, blobSize)
	for i := range blob {
		blob[i] = byte(i*31 + 7)
	}
	want := sha256.Sum256(blob)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Etag", `"blobv1"`)
		http.ServeContent(w, r, "blob.bin", time.Unix(0, 0), bytes.NewReader(blob))
	}))
	defer backend.Close()

	release := make(chan struct{})
	proxy := newRSGatedClient(t, backend.Listener.Addr().String(), release, false)
	c := rsBrowserConnect(t, proxy)

	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	reply, err := readSocks5Reply(c)
	if err != nil {
		t.Fatalf("the CONNECT reply must reach the browser without an exit round trip: %v", err)
	}
	if reply[1] != 0x00 {
		t.Fatalf("optimistic reply REP = %d, want 0 (succeeded)", reply[1])
	}
	_ = c.SetReadDeadline(time.Time{}) //nolint:errcheck

	// The browser's GET now goes out in the SAME round trip as the exit's reply.
	if _, err := fmt.Fprintf(c, "GET /blob.bin HTTP/1.1\r\nHost: example.com\r\nUser-Agent: itest\r\n\r\n"); err != nil {
		t.Fatalf("get: %v", err)
	}
	close(release)

	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK || resp.ContentLength != blobSize {
		t.Fatalf("status=%d content-length=%d, want 200 / %d", resp.StatusCode, resp.ContentLength, blobSize)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if sha256.Sum256(got) != want {
		t.Fatal("reassembled body does not match origin")
	}
}

// TestRangeSplitConnectRefusalYields502 pins the other half: once the browser
// has been told CONNECT succeeded, a refusal cannot be relayed verbatim, so the
// request head already in hand is answered with an HTTP 502 instead of the bare
// close that would look like a network fault to the browser.
func TestRangeSplitConnectRefusalYields502(t *testing.T) {
	proxy := newRSGatedClient(t, "127.0.0.1:1", nil, true)
	c := rsBrowserConnect(t, proxy)

	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	reply, err := readSocks5Reply(c)
	if err != nil {
		t.Fatalf("connect reply: %v", err)
	}
	if reply[1] != 0x00 {
		t.Fatalf("optimistic reply REP = %d, want 0 (succeeded)", reply[1])
	}
	if _, err := fmt.Fprintf(c, "GET /blob.bin HTTP/1.1\r\nHost: example.com\r\nUser-Agent: itest\r\n\r\n"); err != nil {
		t.Fatalf("get: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("a refused CONNECT must answer the browser, not close on it: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
}
