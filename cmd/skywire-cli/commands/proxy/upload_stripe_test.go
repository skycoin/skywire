// Package skysocksc — the striped upload path of pkg/skysocks against the real
// chunked-upload sink of `loadtest serve`.
//
// These are the two halves of the contract meeting: the client cuts a POST into
// offset-addressed PUTs and the sink absorbs them, hashing the contiguous prefix
// once. Nothing here stands in for either side — the handler is loadtestUpload
// itself, the proxy is a real skysocks.Client, and the tunnels are real yamux
// sessions that can be killed under a transfer.
package skysocksc

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0magnet/yamux"

	"github.com/skycoin/skywire/pkg/skysocks"
)

// --- a minimal exit ---------------------------------------------------------

// upFakeExit is a SOCKS5 server over yamux that ignores the CONNECT target and
// splices to one backend, so a test browser CONNECTing to "example.com:80"
// reaches the sink. pace, when non-zero, throttles the CLIENT→backend direction
// so a chunk stays in flight long enough to be killed mid-upload.
type upFakeExit struct {
	sess     *yamux.Session
	accepted atomic.Int64
}

func upConnPair(t *testing.T) (net.Conn, net.Conn) {
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

// upSlowWriter paces a copy so an upload chunk is still on the wire when the
// test cuts its tunnel.
type upSlowWriter struct {
	w     io.Writer
	chunk int
	pause time.Duration
}

func (s upSlowWriter) Write(p []byte) (int, error) {
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

func startUpFakeExit(t *testing.T, backendAddr string, pace time.Duration) (net.Conn, *upFakeExit) {
	t.Helper()
	cli, srv := upConnPair(t)
	sess, err := yamux.Server(srv, yamux.DefaultConfig())
	if err != nil {
		t.Fatalf("yamux server: %v", err)
	}
	e := &upFakeExit{sess: sess}
	go func() {
		for {
			st, err := sess.AcceptStream()
			if err != nil {
				return
			}
			e.accepted.Add(1)
			go upServeSocks(st, backendAddr, pace)
		}
	}()
	return cli, e
}

func upServeSocks(st net.Conn, backendAddr string, pace time.Duration) {
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
	if _, err := st.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	be, err := net.Dial("tcp", backendAddr)
	if err != nil {
		return
	}
	defer be.Close() //nolint:errcheck
	done := make(chan struct{}, 2)
	go func() { //nolint:errcheck,gosec
		var w io.Writer = be
		if pace > 0 {
			w = upSlowWriter{w: be, chunk: 32 << 10, pause: pace}
		}
		_, _ = io.Copy(w, st) //nolint:errcheck,gosec
		done <- struct{}{}
	}()
	go func() { _, _ = io.Copy(st, be); done <- struct{}{} }() //nolint:errcheck,gosec
	<-done
}

// --- the sink ---------------------------------------------------------------

// upSink serves the REAL loadtestUpload handler and counts what it answered, so
// a test can assert a 425 was actually exercised rather than assumed.
type upSink struct {
	puts     atomic.Int64
	inflight atomic.Int64
	early    atomic.Int64
	posts    atomic.Int64
}

type upRecorder struct {
	http.ResponseWriter
	sink *upSink
}

func (r *upRecorder) WriteHeader(code int) {
	if code == http.StatusTooEarly {
		r.sink.early.Add(1)
	}
	r.ResponseWriter.WriteHeader(code)
}

func (s *upSink) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			s.puts.Add(1)
			s.inflight.Add(1)
			defer s.inflight.Add(-1)
		case http.MethodPost:
			s.posts.Add(1)
		}
		loadtestUpload(&upRecorder{ResponseWriter: w, sink: s}, r)
	}
}

// --- the browser ------------------------------------------------------------

// upPost drives a SOCKS5 CONNECT to example.com:80 and an Expect-100-continue
// POST through the proxy — the shape `bench/bench.sh` and `bench/run-mux.sh`
// give curl (`-X POST --data-binary @file`). It returns the decoded answer.
func upPost(t *testing.T, proxyAddr, path string, body []byte) (int, string, int64) {
	t.Helper()
	const host = "example.com"
	c, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer c.Close() //nolint:errcheck
	if _, err := c.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatalf("greeting: %v", err)
	}
	if _, err := io.ReadFull(c, make([]byte, 2)); err != nil {
		t.Fatalf("method reply: %v", err)
	}
	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))} //nolint:gosec // the test host is short
	req = append(req, host...)
	req = append(req, 0x00, 0x50)
	if _, err := c.Write(req); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := io.ReadFull(c, make([]byte, 10)); err != nil {
		t.Fatalf("connect reply: %v", err)
	}
	if _, err := fmt.Fprintf(c, "POST %s HTTP/1.1\r\nHost: %s\r\nContent-Type: application/octet-stream\r\nContent-Length: %d\r\nExpect: 100-continue\r\n\r\n",
		path, host, len(body)); err != nil {
		t.Fatalf("head: %v", err)
	}
	br := bufio.NewReader(c)
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("interim: %v", err)
	}
	if !strings.Contains(line, " 100 ") {
		t.Fatalf("first response line = %q, want a 100 Continue", line)
	}
	for {
		l, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("interim headers: %v", err)
		}
		if strings.TrimRight(l, "\r\n") == "" {
			break
		}
	}
	if _, err := c.Write(body); err != nil {
		t.Fatalf("body: %v", err)
	}
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodPost})
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	var got struct {
		Bytes  int64  `json:"bytes"`
		Sha256 string `json:"sha256"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode %d response: %v", resp.StatusCode, err)
	}
	return resp.StatusCode, got.Sha256, got.Bytes
}

// upProxy builds a two-tunnel skysocks client in front of the two fake exits and
// returns the SOCKS address it listens on.
func upProxy(t *testing.T, conns []net.Conn) string {
	t.Helper()
	client, err := skysocks.NewMultiClient(conns, nil)
	if err != nil {
		t.Fatalf("new multi client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() }) //nolint:errcheck

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	addr := probe.Addr().String()
	probe.Close()                                   //nolint:errcheck,gosec
	go func() { _ = client.ListenAndServe(addr) }() //nolint:errcheck
	for i := 0; i < 200; i++ {
		if cc, err := net.Dial("tcp", addr); err == nil {
			cc.Close() //nolint:errcheck,gosec
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return addr
}

// upResetSink drops the sink's session table between tests: sessions live for
// loadtestUploadIdle and only four may exist at once, so without this the fifth
// test in one process is answered 503 by a sink full of finished uploads.
func upResetSink() {
	loadtestUploadMu.Lock()
	loadtestUploads = map[string]*loadtestUploadSession{}
	loadtestUploadMu.Unlock()
}

func upBlob(n int) ([]byte, string) {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*17 + 5)
	}
	sum := sha256.Sum256(b)
	return b, hex.EncodeToString(sum[:])
}

// TestStripedUploadReachesTheSinkWhole is the acceptance case: an ordinary POST
// through the proxy is cut into acked chunks, spread over both tunnels, and the
// sink's whole-object hash comes back as the answer to that one POST — which is
// exactly what the bench checks.
func TestStripedUploadReachesTheSinkWhole(t *testing.T) {
	upResetSink()
	blob, want := upBlob(16 << 20) // four 4 MiB chunks

	sink := &upSink{}
	backend := httptest.NewServer(sink.handler())
	defer backend.Close()

	aCli, aExit := startUpFakeExit(t, backend.Listener.Addr().String(), 0)
	bCli, bExit := startUpFakeExit(t, backend.Listener.Addr().String(), 0)
	proxy := upProxy(t, []net.Conn{aCli, bCli})

	code, sum, n := upPost(t, proxy, "/upload", blob)
	if code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}
	if n != int64(len(blob)) || sum != want {
		t.Fatalf("sink saw %d bytes / %s, want %d / %s", n, sum, len(blob), want)
	}
	if p := sink.puts.Load(); p < 4 {
		t.Fatalf("sink saw %d chunk PUT(s), want at least the 4 the object splits into", p)
	}
	if p := sink.posts.Load(); p != 0 {
		t.Fatalf("sink saw %d whole-body POST(s): the upload was not striped", p)
	}
	// Both tunnels must have carried chunks — a striped upload that stacks on one
	// tunnel is the thing this exists to stop.
	if aExit.accepted.Load() < 2 || bExit.accepted.Load() < 2 {
		t.Fatalf("streams per tunnel: A=%d B=%d — the chunks did not spread", aExit.accepted.Load(), bExit.accepted.Load())
	}
	t.Logf("%d PUTs over tunnels A=%d B=%d streams", sink.puts.Load(), aExit.accepted.Load(), bExit.accepted.Load())
}

// TestStripedUploadSurvivesATunnelCut is the measured failure this path exists
// for: the same cut that downloads survive in about a second lost 3 of 3
// uploads. A chunk whose tunnel dies is re-sent on the survivor, the sink
// absorbs the duplicate range it had already taken, and the object still hashes.
func TestStripedUploadSurvivesATunnelCut(t *testing.T) {
	upResetSink()
	blob, want := upBlob(16 << 20)

	sink := &upSink{}
	backend := httptest.NewServer(sink.handler())
	defer backend.Close()

	aCli, _ := startUpFakeExit(t, backend.Listener.Addr().String(), 0)
	// Tunnel B is paced to ~1.6 MB/s upstream, so a chunk on it is reliably still
	// being absorbed when the cut comes.
	bCli, bExit := startUpFakeExit(t, backend.Listener.Addr().String(), 20*time.Millisecond)
	proxy := upProxy(t, []net.Conn{aCli, bCli})

	type result struct {
		code int
		sum  string
		n    int64
	}
	done := make(chan result, 1)
	go func() {
		code, sum, n := upPost(t, proxy, "/upload", blob)
		done <- result{code, sum, n}
	}()

	// Cut tunnel B while it is carrying a chunk the sink has not acked.
	deadline := time.Now().Add(30 * time.Second)
	cut := false
	for time.Now().Before(deadline) {
		if sink.inflight.Load() > 0 && bExit.sess.NumStreams() > 0 && bExit.accepted.Load() >= 1 {
			time.Sleep(150 * time.Millisecond) // let the chunk get well under way
			t.Logf("cutting tunnel B with %d PUT(s) in flight, %d stream(s) on B", sink.inflight.Load(), bExit.sess.NumStreams())
			_ = bExit.sess.Close() //nolint:errcheck
			cut = true
			break
		}
		select {
		case r := <-done:
			t.Fatalf("the upload finished (%d) before a chunk was ever in flight on the doomed tunnel", r.code)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !cut {
		t.Fatal("no chunk was ever in flight on the doomed tunnel")
	}

	select {
	case r := <-done:
		if r.code != 200 {
			t.Fatalf("status after the cut = %d, want 200", r.code)
		}
		if r.n != int64(len(blob)) || r.sum != want {
			t.Fatalf("after the cut the sink saw %d bytes / %s, want %d / %s", r.n, r.sum, len(blob), want)
		}
	case <-time.After(120 * time.Second):
		t.Fatal("the upload never completed after the tunnel died")
	}
	t.Logf("%d PUT(s) total, %d of them re-sends", sink.puts.Load(), sink.puts.Load()-4)
}

// TestStripedUploadWaitsOutTheSinksReorderWindow: the sink advertises a window
// and then has less of it than it said — the case the client cannot plan around,
// since it sizes its own in-flight bytes on the advertisement. Every chunk ahead
// of the frontier is then refused with 425 + Retry-After, and the client must
// wait and come back rather than fail the upload.
func TestStripedUploadWaitsOutTheSinksReorderWindow(t *testing.T) {
	upResetSink()
	window, sessions := loadtestUploadWindow, loadtestUploadSessions
	loadtestUploadSessions = 4
	defer func() { loadtestUploadWindow, loadtestUploadSessions = window, sessions }()

	blob, want := upBlob(12 << 20) // three chunks, at most one admissible at a time

	sink := &upSink{}
	// The advertisement says 16 MiB; the moment it has been made, the window is
	// one chunk.
	tighten := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead || r.Method == http.MethodOptions {
			loadtestUploadWindow = 16 << 20
			sink.handler()(w, r)
			loadtestUploadWindow = 4 << 20
			return
		}
		sink.handler()(w, r)
	})
	backend := httptest.NewServer(tighten)
	defer backend.Close()

	aCli, _ := startUpFakeExit(t, backend.Listener.Addr().String(), 0)
	bCli, _ := startUpFakeExit(t, backend.Listener.Addr().String(), 0)
	proxy := upProxy(t, []net.Conn{aCli, bCli})

	code, sum, n := upPost(t, proxy, "/upload", blob)
	if code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}
	if n != int64(len(blob)) || sum != want {
		t.Fatalf("sink saw %d bytes / %s, want %d / %s", n, sum, len(blob), want)
	}
	if sink.early.Load() == 0 {
		t.Fatal("the sink never had to refuse a chunk: the 425 path was not exercised")
	}
	t.Logf("%d chunk(s) held off with 425, %d PUT(s) in total", sink.early.Load(), sink.puts.Load())
}
