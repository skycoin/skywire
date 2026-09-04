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

// rsFakeExit is a minimal SOCKS5 server over yamux that ignores the requested
// destination and always splices to backendAddr — so a test browser CONNECTing to
// "example.com:80" reaches a local range-capable backend. It stands in for the
// skysocks exit server without any mesh.
func rsFakeExit(t *testing.T, conn net.Conn, backendAddr string) {
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
			// SOCKS5 greeting.
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
			// CONNECT request — parse and discard the target.
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
			if _, err := st.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
				return
			}
			// Splice to the real backend.
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

// socks5Get drives a SOCKS5 CONNECT to example.com:80 + a plain GET through proxyAddr and
// returns the parsed response with its body fully read.
func socks5Get(t *testing.T, proxyAddr, path string) *http.Response {
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
	req = append(req, 0x00, 0x50) // port 80
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
	return resp
}

func newRSTestClient(t *testing.T, backendAddr string, conc int, chunk int64) string {
	t.Helper()
	// TCP socketpair between the client and the fake exit.
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
	exitConn := <-dialed
	go rsFakeExit(t, exitConn, backendAddr)

	client, err := NewClient(cliConn, nil)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	client.SetRangeSplit(true, conc, chunk)
	t.Cleanup(func() { _ = client.Close() }) //nolint:errcheck

	// Bind a free port, hand it to ListenAndServe.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	proxyAddr := probe.Addr().String()
	probe.Close()                                        //nolint:errcheck,gosec
	go func() { _ = client.ListenAndServe(proxyAddr) }() //nolint:errcheck
	// Wait until the proxy is accepting.
	for i := 0; i < 100; i++ {
		if cc, err := net.Dial("tcp", proxyAddr); err == nil {
			cc.Close() //nolint:errcheck,gosec
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return proxyAddr
}

// TestRangeSplitByteIdentity is the core acceptance test: a plain GET through the
// proxy, split into concurrent ranges over yamux, reassembles byte-identically.
func TestRangeSplitByteIdentity(t *testing.T) {
	const blobSize = 20 << 20
	blob := make([]byte, blobSize)
	for i := range blob {
		blob[i] = byte(i*31 + 7)
	}
	want := sha256.Sum256(blob)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Etag", "\"blobv1\"")
		http.ServeContent(w, r, "blob.bin", time.Unix(0, 0), bytes.NewReader(blob))
	}))
	defer backend.Close()

	// 1 MiB chunks over 20 MiB → 20 chunks across 4 concurrent streams.
	proxy := newRSTestClient(t, backend.Listener.Addr().String(), 4, 1<<20)

	resp := socks5Get(t, proxy, "/blob.bin")
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if resp.ContentLength != blobSize {
		t.Fatalf("Content-Length = %d, want %d", resp.ContentLength, blobSize)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(got) != blobSize {
		t.Fatalf("body len = %d, want %d", len(got), blobSize)
	}
	if sha256.Sum256(got) != want {
		t.Fatal("reassembled body does not match origin (byte-identity FAILED)")
	}
}

// TestRangeSplitNonRangeServer: an origin that ignores Range (always 200, no
// Accept-Ranges) must fall back to a byte-identical single-stream relay.
func TestRangeSplitNonRangeServer(t *testing.T) {
	blob := bytes.Repeat([]byte("XYZ0"), 5<<20) // 20 MiB, but server ignores Range
	want := sha256.Sum256(blob)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Deliberately ignore any Range header: plain 200 with the whole body.
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(200)
		_, _ = w.Write(blob) //nolint:errcheck
	}))
	defer backend.Close()

	proxy := newRSTestClient(t, backend.Listener.Addr().String(), 4, 1<<20)
	resp := socks5Get(t, proxy, "/nr.bin")
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if sha256.Sum256(got) != want {
		t.Fatalf("non-range fallback body mismatch: got %d want %d bytes", len(got), len(blob))
	}
}

// TestRangeSplitSmallFile: a file smaller than one chunk is served in a single
// fetch (no split) and still matches.
func TestRangeSplitSmallFile(t *testing.T) {
	blob := bytes.Repeat([]byte("abcd"), 1000) // 4000 bytes
	want := sha256.Sum256(blob)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "s.bin", time.Unix(0, 0), bytes.NewReader(blob))
	}))
	defer backend.Close()

	proxy := newRSTestClient(t, backend.Listener.Addr().String(), 4, 1<<20)
	resp := socks5Get(t, proxy, "/s.bin")
	defer resp.Body.Close() //nolint:errcheck
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if sha256.Sum256(got) != want {
		t.Fatalf("small-file body mismatch: got %d bytes", len(got))
	}
}
