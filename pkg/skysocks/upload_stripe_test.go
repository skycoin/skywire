// Package skysocks — classification and bounds of the striped upload path.
package skysocks

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- classification -------------------------------------------------------

func mustParseRequest(t *testing.T, head string) *http.Request {
	t.Helper()
	r, err := http.ReadRequest(bufio.NewReader(strings.NewReader(head)))
	if err != nil {
		t.Fatalf("parse request: %v", err)
	}
	return r
}

func TestClassifyUpload(t *testing.T) {
	big := strconv.FormatInt(uploadStripeMinBytes, 10)
	small := strconv.FormatInt(uploadReplayMaxBytes, 10)
	huge := strconv.FormatInt(uploadReplayMaxBytes+1, 10)

	cases := []struct {
		name           string
		head           string
		want           bool
		stripe, replay bool
	}{
		{"big POST stripes and replays", "POST /upload HTTP/1.1\r\nHost: h\r\nContent-Length: " + big + "\r\n\r\n", true, true, true},
		{"big PUT too", "PUT /upload HTTP/1.1\r\nHost: h\r\nContent-Length: " + big + "\r\n\r\n", true, true, true},
		{"over the replay cap stripes only", "POST /upload HTTP/1.1\r\nHost: h\r\nContent-Length: " + huge + "\r\n\r\n", true, true, false},
		{"at the replay cap", "POST /upload HTTP/1.1\r\nHost: h\r\nContent-Length: " + small + "\r\n\r\n", true, true, true},
		{"small POST replays only", "POST /u HTTP/1.1\r\nHost: h\r\nContent-Length: 10\r\n\r\n", true, false, true},
		{"GET is not an upload", "GET /u HTTP/1.1\r\nHost: h\r\n\r\n", false, false, false},
		{"no length at all", "POST /u HTTP/1.1\r\nHost: h\r\n\r\n", false, false, false},
		{"chunked has no last byte", "POST /u HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\n\r\n", false, false, false},
		{"an upgrade is not HTTP any more", "POST /u HTTP/1.1\r\nHost: h\r\nContent-Length: " + big + "\r\nUpgrade: websocket\r\n\r\n", false, false, false},
		{"already offset-addressed", "PUT /u HTTP/1.1\r\nHost: h\r\nContent-Length: " + big + "\r\nContent-Range: bytes 0-1/2\r\n\r\n", false, false, false},
		{"HTTP/1.0", "POST /u HTTP/1.0\r\nHost: h\r\nContent-Length: " + big + "\r\n\r\n", false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := classifyUpload(mustParseRequest(t, tc.head), []byte(tc.head), "h")
			if (u != nil) != tc.want {
				t.Fatalf("classifyUpload = %v, want candidate=%v", u, tc.want)
			}
			if u == nil {
				return
			}
			if u.stripe != tc.stripe || u.replay != tc.replay {
				t.Fatalf("stripe=%v replay=%v, want %v/%v", u.stripe, u.replay, tc.stripe, tc.replay)
			}
		})
	}
}

// TestUploadChunkHeadIsOffsetAddressed: the PUT a chunk rides carries the
// session id, the object size and its own range, and drops exactly the headers
// that would re-frame it.
func TestUploadChunkHeadIsOffsetAddressed(t *testing.T) {
	head := "POST /upload?tag=x HTTP/1.1\r\nHost: sink:18080\r\nExpect: 100-continue\r\nContent-Length: 99\r\nX-Keep: yes\r\n\r\n"
	u := classifyUpload(mustParseRequest(t, head), []byte(head), "sink")
	if u == nil {
		t.Fatal("not classified as an upload")
	}
	u.total = 99
	s := newUploadStripe(nil, u, nil)
	uri := s.chunkURI()
	if !strings.Contains(uri, "id="+s.id) || !strings.Contains(uri, "bytes=99") || !strings.Contains(uri, "tag=x") {
		t.Fatalf("chunk URI = %q, want the original query plus id and bytes", uri)
	}
	got := string(buildUploadHead(u.req, http.MethodPut, uri, 40, "bytes 0-39/99"))
	for _, want := range []string{"PUT " + uri + " HTTP/1.1\r\n", "Host: sink:18080\r\n", "Content-Range: bytes 0-39/99\r\n", "Content-Length: 40\r\n", "X-Keep: yes\r\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("chunk head %q missing %q", got, want)
		}
	}
	// Expect must never reach a chunk stream: we answer the browser's 100 ourselves.
	if strings.Contains(got, "Expect") {
		t.Fatalf("chunk head carries Expect: %q", got)
	}
}

func TestReadChunkAck(t *testing.T) {
	raw := "HTTP/1.1 425 Too Early\r\nRetry-After: 2\r\nX-Next-Offset: 4096\r\nX-Upload-Received: 4096\r\nContent-Length: 0\r\n\r\n"
	resp, err := http.ReadResponse(bufio.NewReader(strings.NewReader(raw)), &http.Request{Method: http.MethodPut})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	ack := readChunkAck(resp, nil)
	if ack.status != 425 || ack.retryAfter != 2*time.Second || ack.nextOffset != 4096 || ack.received != 4096 {
		t.Fatalf("ack = %+v", ack)
	}
	if ack.final != nil {
		t.Fatal("a 425 is not a completion")
	}

	body := []byte(`{"bytes":99,"sha256":"ab"}`)
	raw = "HTTP/1.1 200 OK\r\nX-Sha256: ab\r\nContent-Type: application/json\r\nContent-Length: 26\r\n\r\n" + string(body)
	resp, err = http.ReadResponse(bufio.NewReader(strings.NewReader(raw)), &http.Request{Method: http.MethodPut})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	ack = readChunkAck(resp, body)
	if ack.final == nil {
		t.Fatal("the ack carrying X-Sha256 is the browser's response")
	}
	out := string(ack.final)
	if !strings.HasPrefix(out, "HTTP/1.1 200 OK\r\n") || !strings.HasSuffix(out, string(body)) ||
		!strings.Contains(out, "X-Sha256: ab\r\n") || !strings.Contains(out, "Content-Length: 26\r\n") {
		t.Fatalf("serialized response = %q", out)
	}
}

func TestUploadRetryWaitIsBounded(t *testing.T) {
	if got := uploadRetryWait(0); got != uploadBusyBackoff {
		t.Fatalf("no Retry-After → %v, want %v", got, uploadBusyBackoff)
	}
	if got := uploadRetryWait(time.Hour); got != uploadEarlyWaitMax {
		t.Fatalf("an absurd Retry-After → %v, want it capped at %v", got, uploadEarlyWaitMax)
	}
}

// TestUploadPerTunnelHonorsTheSendWindow: chunk x per-tunnel inflight never
// passes the router's per-leg send window, whatever the chunk size is set to.
func TestUploadPerTunnelHonorsTheSendWindow(t *testing.T) {
	defer restoreUploadTunables(uploadChunkBytes, uploadMemBytes, uploadStripeMinBytes, uploadConcurrency)()
	s := &uploadStripe{}
	uploadChunkBytes = 4 << 20
	if got := s.perTunnel(); got != 2 || int64(got)*uploadChunkBytes > uploadSendWindowBytes {
		t.Fatalf("4 MiB chunks → %d in flight per tunnel, want 2 (<= %d bytes)", got, uploadSendWindowBytes)
	}
	uploadChunkBytes = 8 << 20
	if got := s.perTunnel(); got != 1 {
		t.Fatalf("8 MiB chunks → %d in flight per tunnel, want 1 (the window is %d)", got, uploadSendWindowBytes)
	}
}

// TestUploadSlotsHonorTheSinksWindow: the buffers alive at once are the
// smaller of our own ceiling and the reorder window the sink advertised.
func TestUploadSlotsHonorTheSinksWindow(t *testing.T) {
	defer restoreUploadTunables(uploadChunkBytes, uploadMemBytes, uploadStripeMinBytes, uploadConcurrency)()
	uploadChunkBytes = 4 << 20
	uploadMemBytes = 16 << 20
	s := &uploadStripe{u: &uploadCandidate{}}
	if got := s.slots(); got != 4 {
		t.Fatalf("an origin that did not advertise a window → %d slots, want our own 4", got)
	}
	s.u.window = 8 << 20
	if got := s.slots(); got != 2 {
		t.Fatalf("an 8 MiB window → %d slots, want 2", got)
	}
	s.u.window = 1 << 20 // smaller than one chunk: one chunk at a time, never none
	if got := s.slots(); got != 1 {
		t.Fatalf("a window under one chunk → %d slots, want 1", got)
	}
}

func TestUploadProbeCacheExpires(t *testing.T) {
	p := &uploadProbeCache{}
	if _, ok := p.get("h:80"); ok {
		t.Fatal("an unprobed origin must not answer from the cache")
	}
	p.put("h:80", uploadProbeEntry{ok: true, window: 16 << 20})
	e, cached := p.get("h:80")
	if !cached || !e.ok || e.window != 16<<20 {
		t.Fatalf("a fresh answer is cached whole: %+v cached=%v", e, cached)
	}
	p.mu.Lock()
	p.m["h:80"] = uploadProbeEntry{ok: true, at: time.Now().Add(-uploadProbeTTL - time.Second)}
	p.mu.Unlock()
	if _, cached := p.get("h:80"); cached {
		t.Fatal("an answer older than the TTL must be re-probed")
	}
}

// --- the memory bound -----------------------------------------------------

func restoreUploadTunables(chunk, mem, min int64, conc int) func() {
	return func() {
		uploadChunkBytes, uploadMemBytes, uploadStripeMinBytes, uploadConcurrency = chunk, mem, min, conc
	}
}

// stubSink is an opt-in chunked-upload sink: it advertises X-Chunked-Upload,
// absorbs offset-addressed PUTs into the object and answers the completing one
// with the object's hash. The real sink (#4988, cmd/skywire-cli/commands/proxy)
// is what the end-to-end tests there drive; this one exists so a test can hold a
// chunk's ack back and watch the client's memory gate.
type stubSink struct {
	mu    sync.Mutex
	obj   []byte
	have  map[int64]int64
	delay time.Duration
	whole int // plain POSTs (no id/Content-Range) that arrived
}

func (s *stubSink) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead || r.Method == http.MethodOptions {
			w.Header().Set("X-Chunked-Upload", "bytes")
			w.WriteHeader(http.StatusOK)
			return
		}
		cr := r.Header.Get("Content-Range")
		if cr == "" {
			s.mu.Lock()
			s.whole++
			s.mu.Unlock()
			h := sha256.New()
			n, _ := io.Copy(h, r.Body) //nolint:errcheck
			w.Header().Set("X-Sha256", hex.EncodeToString(h.Sum(nil)))
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"bytes": n, "sha256": hex.EncodeToString(h.Sum(nil))}) //nolint:errcheck
			return
		}
		var start, end, total int64
		if _, err := fmt.Sscanf(cr, "bytes %d-%d/%d", &start, &end, &total); err != nil {
			http.Error(w, "bad range", http.StatusBadRequest)
			return
		}
		buf, err := io.ReadAll(io.LimitReader(r.Body, end-start+1))
		if err != nil || int64(len(buf)) != end-start+1 {
			http.Error(w, "short chunk", http.StatusBadRequest)
			return
		}
		if s.delay > 0 {
			time.Sleep(s.delay)
		}
		s.mu.Lock()
		if s.obj == nil {
			s.obj = make([]byte, total)
			s.have = map[int64]int64{}
		}
		copy(s.obj[start:], buf)
		s.have[start] = end - start + 1
		var prefix int64
		for {
			n, ok := s.have[prefix]
			if !ok {
				break
			}
			prefix += n
		}
		done := prefix >= total
		sum := ""
		if done {
			sum = hex.EncodeToString(func() []byte { h := sha256.Sum256(s.obj); return h[:] }())
		}
		s.mu.Unlock()
		w.Header().Set("X-Upload-Received", strconv.FormatInt(prefix, 10))
		if done {
			w.Header().Set("X-Sha256", sum)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"bytes": total, "sha256": sum}) //nolint:errcheck
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"received": prefix, "total": total}) //nolint:errcheck
	}
}

// socks5Upload drives a SOCKS5 CONNECT to example.com:80 and an
// Expect-100-continue POST through the proxy, returning the final response.
func socks5Upload(t *testing.T, proxyAddr, path string, body []byte) *http.Response {
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
	req = append(req, 0x00, 0x50)
	if _, err := c.Write(req); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := readSocks5Reply(c); err != nil {
		t.Fatalf("connect reply: %v", err)
	}
	if _, err := fmt.Fprintf(c, "POST %s HTTP/1.1\r\nHost: %s\r\nUser-Agent: itest\r\nContent-Type: application/octet-stream\r\nContent-Length: %d\r\nExpect: 100-continue\r\n\r\n",
		path, host, len(body)); err != nil {
		t.Fatalf("head: %v", err)
	}
	br := bufio.NewReader(c)
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("interim: %v", err)
	}
	if statusCode(line) != 100 {
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
	return resp
}

// TestStripedUploadHoldsItsMemoryBound is the bound the design turns on: the
// client buffers uploadMemBytes of chunks at a time and no more, however large
// the body is. The sink holds every ack back for a moment so buffers would pile
// up if the gate did not stop them.
func TestStripedUploadHoldsItsMemoryBound(t *testing.T) {
	defer restoreUploadTunables(uploadChunkBytes, uploadMemBytes, uploadStripeMinBytes, uploadConcurrency)()
	uploadChunkBytes = 64 << 10
	uploadMemBytes = 256 << 10 // four buffers
	uploadStripeMinBytes = 64 << 10
	uploadConcurrency = 2

	blob := make([]byte, 4<<20) // 64 chunks against a 4-buffer ceiling
	for i := range blob {
		blob[i] = byte(i*29 + 3)
	}
	want := sha256.Sum256(blob)

	sink := &stubSink{delay: 5 * time.Millisecond}
	backend := httptest.NewServer(sink.handler())
	defer backend.Close()

	proxy := newRSTestClient(t, backend.Listener.Addr().String(), 4, 1<<20)
	uploadHeldPeak.Store(0)

	resp := socks5Upload(t, proxy, "/upload", blob)
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got struct {
		Bytes  int64  `json:"bytes"`
		Sha256 string `json:"sha256"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Bytes != int64(len(blob)) || got.Sha256 != hex.EncodeToString(want[:]) {
		t.Fatalf("sink saw %d bytes / %s, want %d / %s", got.Bytes, got.Sha256, len(blob), hex.EncodeToString(want[:]))
	}
	if sink.whole != 0 {
		t.Fatalf("%d whole-body POSTs reached the sink: the upload was not striped", sink.whole)
	}
	if peak := uploadHeldPeak.Load(); peak > uploadMemBytes {
		t.Fatalf("peak upload buffers = %d bytes, over the %d ceiling", peak, uploadMemBytes)
	}
	if peak := uploadHeldPeak.Load(); peak < uploadChunkBytes {
		t.Fatalf("peak upload buffers = %d: the gate never held a chunk, so the test proves nothing", peak)
	}
	t.Logf("peak buffered: %d bytes of a %d ceiling for a %d-byte body", uploadHeldPeak.Load(), uploadMemBytes, len(blob))
	if uploadHeld.Load() != 0 {
		t.Fatalf("%d bytes of chunk buffer outlived the upload", uploadHeld.Load())
	}
}

// TestGenericPostIsReplayedWhenItsTunnelDies: an origin that does not opt in
// cannot be striped or resumed — it has consumed an unknown prefix and there is
// no offset to name. The one thing that can be done is done: while the origin
// has committed nothing (a 100 Continue commits nothing), a body small enough to
// remember is re-sent whole on a surviving tunnel.
func TestGenericPostIsReplayedWhenItsTunnelDies(t *testing.T) {
	blob := make([]byte, 2<<20) // under the replay cap, over the slow origin's read
	for i := range blob {
		blob[i] = byte(i*13 + 9)
	}
	want := sha256.Sum256(blob)

	var posts atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed) // no opt-in
			return
		}
		posts.Add(1)
		// Read slowly, so the first attempt is still mid-body at the cut.
		h := sha256.New()
		buf := make([]byte, 32<<10)
		var n int64
		for {
			k, err := r.Body.Read(buf)
			if k > 0 {
				h.Write(buf[:k]) //nolint:errcheck,gosec // hash.Hash never errors
				n += int64(k)
				time.Sleep(10 * time.Millisecond)
			}
			if err != nil {
				break
			}
		}
		if n != int64(len(blob)) {
			http.Error(w, "short body", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"bytes": n, "sha256": hex.EncodeToString(h.Sum(nil))}) //nolint:errcheck
	}))
	defer backend.Close()

	proxy, client, _, _ := newRSTwoTunnelClient(t, backend.Listener.Addr().String(), 4, 1<<20)

	type result struct {
		sum   string
		bytes int64
	}
	done := make(chan result, 1)
	go func() {
		resp := socks5Upload(t, proxy, "/upload", blob)
		defer resp.Body.Close() //nolint:errcheck
		var got struct {
			Bytes  int64  `json:"bytes"`
			Sha256 string `json:"sha256"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Errorf("decode %d response: %v", resp.StatusCode, err)
			done <- result{}
			return
		}
		done <- result{got.Sha256, got.Bytes}
	}()

	// Kill the tunnel the browser connection landed on, mid-body.
	deadline := time.Now().Add(20 * time.Second)
	cut := false
	for time.Now().Before(deadline) && !cut {
		if posts.Load() > 0 {
			for _, s := range client.snapshotSessions() {
				if s != nil && !s.IsClosed() && s.NumStreams() > 0 {
					time.Sleep(100 * time.Millisecond)
					_ = s.Close() //nolint:errcheck
					cut = true
					break
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !cut {
		t.Fatal("the POST never reached the origin, so nothing was cut")
	}

	select {
	case got := <-done:
		if got.bytes != int64(len(blob)) || got.sum != hex.EncodeToString(want[:]) {
			t.Fatalf("after the cut the origin saw %d bytes / %s, want %d / %s",
				got.bytes, got.sum, len(blob), hex.EncodeToString(want[:]))
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the POST never completed after its tunnel died")
	}
	if n := posts.Load(); n < 2 {
		t.Fatalf("the origin saw %d POST(s): the request was never replayed", n)
	}
	t.Logf("the origin saw %d POST(s); the replayed one carried the whole body", posts.Load())
}

// TestUploadToNonOptInOriginIsUnchanged: an origin that does not advertise
// X-Chunked-Upload gets ONE whole-body POST, exactly as before.
func TestUploadToNonOptInOriginIsUnchanged(t *testing.T) {
	defer restoreUploadTunables(uploadChunkBytes, uploadMemBytes, uploadStripeMinBytes, uploadConcurrency)()
	uploadChunkBytes = 64 << 10
	uploadStripeMinBytes = 64 << 10

	blob := bytes.Repeat([]byte("nochunk!"), 64<<10) // 512 KiB
	want := sha256.Sum256(blob)

	var (
		mu    sync.Mutex
		posts int
		puts  int
	)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		switch r.Method {
		case http.MethodPost:
			posts++
		case http.MethodPut:
			puts++
		}
		mu.Unlock()
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed) // no opt-in, ever
			return
		}
		h := sha256.New()
		n, _ := io.Copy(h, r.Body)                                                                                  //nolint:errcheck
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"bytes": n, "sha256": hex.EncodeToString(h.Sum(nil))}) //nolint:errcheck
	}))
	defer backend.Close()

	proxy := newRSTestClient(t, backend.Listener.Addr().String(), 4, 1<<20)
	resp := socks5Upload(t, proxy, "/upload", blob)
	defer resp.Body.Close() //nolint:errcheck
	var got struct {
		Bytes  int64  `json:"bytes"`
		Sha256 string `json:"sha256"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Bytes != int64(len(blob)) || got.Sha256 != hex.EncodeToString(want[:]) {
		t.Fatalf("origin saw %d bytes / %s, want %d / %s", got.Bytes, got.Sha256, len(blob), hex.EncodeToString(want[:]))
	}
	mu.Lock()
	defer mu.Unlock()
	if posts != 1 || puts != 0 {
		t.Fatalf("origin saw %d POST(s) and %d PUT(s), want exactly one whole-body POST", posts, puts)
	}
}
