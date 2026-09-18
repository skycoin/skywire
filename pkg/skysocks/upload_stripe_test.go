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

	"github.com/0magnet/yamux"

	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
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

// TestUploadPerTunnelIsTheConcurrency: one tunnel carries uploadConcurrency
// chunks whatever the chunk size is. The old ceiling — chunk x inflight at or
// below the router's 8 MiB per-leg send window — was over-conservative: a writer
// parked at the window is a writer with bytes already queued for the next
// opening, and holding it to 2 cost a single striped upload 22 % of the
// single-route reference.
func TestUploadPerTunnelIsTheConcurrency(t *testing.T) {
	defer restoreUploadTunables(uploadChunkBytes, uploadMemBytes, uploadStripeMinBytes, uploadConcurrency)()
	s := &uploadStripe{u: &uploadCandidate{}}
	for _, chunk := range []int64{4 << 20, 8 << 20} {
		uploadChunkBytes = chunk
		if got := s.perTunnel(); got != uploadConcurrency {
			t.Fatalf("%d-byte chunks → %d in flight per tunnel, want %d", chunk, got, uploadConcurrency)
		}
	}
	uploadConcurrency = 0
	if got := s.perTunnel(); got != 1 {
		t.Fatalf("a concurrency of 0 → %d, want the 1 floor", got)
	}
}

// TestUploadSlotsHonorTheSinksWindow: the buffers alive at once are the
// smaller of our own ceiling and the reorder window the sink advertised.
func TestUploadSlotsHonorTheSinksWindow(t *testing.T) {
	defer restoreUploadTunables(uploadChunkBytes, uploadMemBytes, uploadStripeMinBytes, uploadConcurrency)()
	uploadChunkBytes = 4 << 20
	uploadMemBytes = 32 << 20
	uploadConcurrency = 4
	s := &uploadStripe{u: &uploadCandidate{}}
	if got := s.slots(); got != 8 {
		t.Fatalf("an origin that did not advertise a window → %d slots, want our own 8", got)
	}
	s.u.window = 64 << 20 // the sink's default: 16 chunks, 4 of them headroom
	if got := s.slots(); got != 8 {
		t.Fatalf("a 64 MiB window → %d slots, want our own 8 (the window is not the binding one)", got)
	}
	// An OLD sink advertising 16 MiB: 4 chunks, and the headroom is capped at half
	// the window so raising the concurrency cannot narrow it to a single buffer.
	s.u.window = 16 << 20
	if got := s.slots(); got != 2 {
		t.Fatalf("a 16 MiB window → %d slots, want 2", got)
	}
	s.u.window = 8 << 20 // two chunks: one live, one the re-send headroom
	if got := s.slots(); got != 1 {
		t.Fatalf("an 8 MiB window → %d slots, want 1", got)
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
	// holdFirst keeps chunk 0 — and with it the contiguous prefix, which nothing
	// else can advance — inside its handler for this long. earlyStarts is every
	// other chunk start that reached the sink before chunk 0's response went out.
	holdFirst   time.Duration
	released    atomic.Bool
	earlyStarts map[int64]bool
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
		if s.holdFirst > 0 {
			if start == 0 {
				defer s.released.Store(true)
			} else if !s.released.Load() {
				s.mu.Lock()
				s.earlyStarts[start] = true
				s.mu.Unlock()
			}
		}
		buf, err := io.ReadAll(io.LimitReader(r.Body, end-start+1))
		if err != nil || int64(len(buf)) != end-start+1 {
			http.Error(w, "short chunk", http.StatusBadRequest)
			return
		}
		if s.delay > 0 {
			time.Sleep(s.delay)
		}
		if start == 0 && s.holdFirst > 0 {
			time.Sleep(s.holdFirst)
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
// Every caller posts to the same upload path, so it is fixed here rather than
// passed in.
func socks5Upload(t *testing.T, proxyAddr string, body []byte) *http.Response {
	t.Helper()
	const host = "example.com"
	const path = "/upload"
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

	proxy := newRSTestClient(t, backend.Listener.Addr().String(), rsTestConcurrency, 1<<20)
	uploadHeldPeak.Store(0)

	resp := socks5Upload(t, proxy, blob)
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
		resp := socks5Upload(t, proxy, blob)
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

	proxy := newRSTestClient(t, backend.Listener.Addr().String(), rsTestConcurrency, 1<<20)
	resp := socks5Upload(t, proxy, blob)
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

// TestUploadSlotsLeaveTheSinkHeadroom: the live buffers plus one tunnel's worth
// of re-send always fit the sink's window. Without the headroom a re-send is a
// second charge beside the copy the sink is still holding, and the sink evicts
// an already-202'd chunk to admit it — the cut bench's 5 s-per-eviction stall
// (bench/2026-09-16/3194b7cc8-smoke).
func TestUploadSlotsLeaveTheSinkHeadroom(t *testing.T) {
	defer restoreUploadTunables(uploadChunkBytes, uploadMemBytes, uploadStripeMinBytes, uploadConcurrency)()
	uploadChunkBytes = 4 << 20
	uploadMemBytes = 64 << 20
	uploadConcurrency = 4
	for _, window := range []int64{8 << 20, 16 << 20, 32 << 20, 64 << 20} {
		s := &uploadStripe{u: &uploadCandidate{window: window}}
		live := int64(s.slots()) * uploadChunkBytes
		resend := int64(s.headroom()) * uploadChunkBytes
		if live+resend > window && s.slots() > 1 {
			t.Fatalf("a %d-byte window → %d live + %d re-send bytes, past the window",
				window, live, resend)
		}
	}
	s := &uploadStripe{u: &uploadCandidate{window: 32 << 20}}
	if got := s.slots(); got != 4 {
		t.Fatalf("a 32 MiB window with 4 MiB chunks → %d slots, want 8 less the 4-chunk headroom", got)
	}
}

// TestReadChunkAckNamesTheEvictedChunks: the sink's eviction notice is parsed,
// and an ack without one leaves the list empty (an old sink is unchanged).
func TestReadChunkAckNamesTheEvictedChunks(t *testing.T) {
	read := func(raw string) chunkAck {
		t.Helper()
		resp, err := http.ReadResponse(bufio.NewReader(strings.NewReader(raw)), &http.Request{Method: http.MethodPut})
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		return readChunkAck(resp, nil)
	}
	ack := read("HTTP/1.1 202 Accepted\r\nX-Upload-Received: 4096\r\nX-Upload-Evicted: 8192, 12288\r\nContent-Length: 0\r\n\r\n")
	if len(ack.evicted) != 2 || ack.evicted[0] != 8192 || ack.evicted[1] != 12288 {
		t.Fatalf("evicted = %v, want [8192 12288]", ack.evicted)
	}
	if ack := read("HTTP/1.1 202 Accepted\r\nX-Upload-Received: 4096\r\nContent-Length: 0\r\n\r\n"); ack.evicted != nil {
		t.Fatalf("a sink that does not send the header → %v, want none", ack.evicted)
	}
}

// TestEvictedChunkIsResentWithoutTheBackstop: a named eviction re-queues the
// chunk at once. The uploadDurableWait backstop still covers a notice lost with
// its tunnel, but it must no longer be the ordinary path — at 5 s per eviction
// it is what failed the object after uploadResendPasses.
func TestEvictedChunkIsResentWithoutTheBackstop(t *testing.T) {
	s := newUploadStripe(nil, &uploadCandidate{total: 16384}, nil)
	s.mu.Lock()
	s.sending = 1 // something is still going out: the backstop cannot fire
	s.mu.Unlock()

	done := make(chan bool, 1)
	go func() { done <- s.awaitDurable(8192, 12287) }()
	select {
	case got := <-done:
		t.Fatalf("awaitDurable returned %v before any ack", got)
	case <-time.After(100 * time.Millisecond):
	}

	s.note(chunkAck{status: http.StatusAccepted, received: 4096, evicted: []int64{8192}})
	start := time.Now()
	select {
	case got := <-done:
		if got {
			t.Fatal("a named eviction must send the chunk again, not report it durable")
		}
		if elapsed := time.Since(start); elapsed >= uploadDurableWait {
			t.Fatalf("the re-send waited %v; the notice must not go through the backstop", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a named eviction did not re-queue the chunk")
	}

	// The notice is consumed: the same chunk, sent again, waits on the prefix.
	s.mu.Lock()
	stale := s.evicted[8192]
	s.mu.Unlock()
	if stale {
		t.Fatal("the notice must be cleared once it has been acted on")
	}

	// A prefix that passes the chunk is durability, notice or not.
	s.note(chunkAck{status: http.StatusOK, received: 12288})
	if !s.awaitDurable(8192, 12287) {
		t.Fatal("a prefix past the chunk's end is durable")
	}
}

// --- the chunk's two idle round trips -------------------------------------

// readSocks5ConnectRequest reads a SOCKS5 greeting and CONNECT request the way
// the exit's own server does, WITHOUT answering either. What the client wrote
// behind them is left on the stream for the caller to look at.
func readSocks5ConnectRequest(st net.Conn) error {
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(st, hdr); err != nil {
		return err
	}
	if hdr[0] != 0x05 {
		return fmt.Errorf("greeting version %d", hdr[0])
	}
	if _, err := io.ReadFull(st, make([]byte, int(hdr[1]))); err != nil {
		return err
	}
	rh := make([]byte, 4)
	if _, err := io.ReadFull(st, rh); err != nil {
		return err
	}
	switch rh[3] {
	case 0x01:
		_, _ = io.ReadFull(st, make([]byte, 4)) //nolint:errcheck,gosec
	case 0x03:
		l := make([]byte, 1)
		if _, err := io.ReadFull(st, l); err != nil {
			return err
		}
		_, _ = io.ReadFull(st, make([]byte, int(l[0]))) //nolint:errcheck,gosec
	case 0x04:
		_, _ = io.ReadFull(st, make([]byte, 16)) //nolint:errcheck,gosec
	}
	_, err := io.ReadFull(st, make([]byte, 2)) // port
	return err
}

// newUploadStripeOnExit builds a striped upload whose single tunnel ends in a
// fake exit that runs exit() on every stream, so a test can script the SOCKS5
// replies of one chunk PUT byte by byte.
func newUploadStripeOnExit(t *testing.T, total int64, exit func(net.Conn)) *uploadStripe {
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
	exitConn := <-dialed
	go func() {
		sess, err := yamux.Server(exitConn, yamux.DefaultConfig())
		if err != nil {
			return
		}
		for {
			st, err := sess.AcceptStream()
			if err != nil {
				return
			}
			go exit(st)
		}
	}()
	client, err := NewClient(cliConn, nil)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	client.SetRangeSplit(true, 1, 1<<20)
	t.Cleanup(func() { _ = client.Close() }) //nolint:errcheck

	req := mustParseRequest(t, "PUT /up HTTP/1.1\r\nHost: sink.example\r\nContent-Length: 0\r\n\r\n")
	return newUploadStripe(client, &uploadCandidate{req: req, host: "sink.example", total: total, stripe: true}, nil)
}

// TestPutChunkWritesTheBodyBeforeTheHandshakeReply: the chunk body goes out
// WHILE the SOCKS5 replies are still coming back, not after them. Blocking on
// the handshake first left one leg RTT (~142 ms live) idle at the head of every
// chunk, which is most of the gap a single striped upload had to the
// single-route reference. The exit here answers NOTHING until the whole body has
// arrived, so a client that waited for the method reply deadlocks instead.
func TestPutChunkWritesTheBodyBeforeTheHandshakeReply(t *testing.T) {
	buf := make([]byte, 64<<10)
	for i := range buf {
		buf[i] = byte(i * 7)
	}
	saw := make(chan int, 1)
	s := newUploadStripeOnExit(t, int64(len(buf)), func(st net.Conn) {
		defer st.Close() //nolint:errcheck
		if err := readSocks5ConnectRequest(st); err != nil {
			saw <- -1
			return
		}
		n, p := 0, make([]byte, 32<<10)
		for n < len(buf) { // head + body: past len(buf) the body is certainly flowing
			k, err := st.Read(p)
			if err != nil {
				saw <- n
				return
			}
			n += k
		}
		saw <- n
		ack := fmt.Sprintf("HTTP/1.1 202 Accepted\r\nX-Upload-Received: %d\r\nContent-Length: 0\r\n\r\n", len(buf))
		_, _ = st.Write([]byte{0x05, 0x00}) //nolint:errcheck,gosec
		_, _ = st.Write(socks5OKReply)      //nolint:errcheck,gosec
		_, _ = io.WriteString(st, ack)      //nolint:errcheck,gosec
	})

	done := make(chan error, 1)
	var ack chunkAck
	go func() {
		var err error
		ack, err = s.putChunk(0, int64(len(buf))-1, buf)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("putChunk: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("putChunk never finished: the body waited for the handshake reply instead of overlapping it")
	}
	if n := <-saw; n < len(buf) {
		t.Fatalf("the exit saw %d bytes before it answered, want at least the %d-byte body", n, len(buf))
	}
	if ack.status != http.StatusAccepted || ack.received != int64(len(buf)) {
		t.Fatalf("ack = %+v, want a 202 with the whole chunk received", ack)
	}
}

// TestPutChunkFailsCleanlyWhenTheHandshakeFails: overlapping the body with the
// handshake must not turn a refused handshake into a leaked writer. The exit
// selects a method we do not offer and then stops reading, so the body goroutine
// is parked mid-chunk when the failure lands; putChunk has to end it, JOIN it and
// hand deliverChunk an ordinary failed attempt. The buffer is touched after the
// return, so -race fails the test if the writer outlived the attempt.
func TestPutChunkFailsCleanlyWhenTheHandshakeFails(t *testing.T) {
	buf := make([]byte, 512<<10) // larger than the yamux window: the writer parks
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	s := newUploadStripeOnExit(t, int64(len(buf)), func(st net.Conn) {
		defer st.Close() //nolint:errcheck
		if err := readSocks5ConnectRequest(st); err != nil {
			return
		}
		_, _ = st.Write([]byte{0x05, 0xFF}) //nolint:errcheck,gosec // no acceptable method
		<-stop
	})

	done := make(chan error, 1)
	go func() {
		_, err := s.putChunk(0, int64(len(buf))-1, buf)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a refused SOCKS5 handshake must fail the chunk")
		}
		if !strings.Contains(err.Error(), "non-no-auth") {
			t.Fatalf("putChunk = %v, want the handshake's own refusal", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("putChunk hung on a refused handshake")
	}
	buf[0] ^= 0xFF // a writer still running would be a data race here
}

// TestUploadSlotIsFreedOnTheAckNotTheDurability: a chunk the sink has ACKED but
// whose bytes are not yet in its contiguous prefix keeps its buffer — the
// re-send needs the bytes — and gives up its admission SLOT, so the next chunk's
// stream opens at once. Holding the slot to durability made every chunk queue
// behind whichever one the prefix was waiting on: the slots refilled in a burst
// and the tunnels idled at each wave edge.
//
// The sink holds chunk 0 (and with it the prefix) until the test releases it. A
// client that freed the slot only on durability could not get chunk 2 out at
// all, because nothing can advance the prefix while chunk 0 is held.
func TestUploadSlotIsFreedOnTheAckNotTheDurability(t *testing.T) {
	defer restoreUploadTunables(uploadChunkBytes, uploadMemBytes, uploadStripeMinBytes, uploadConcurrency)()
	uploadChunkBytes = 64 << 10
	uploadMemBytes = 1 << 20 // 16 buffers: the admission slot is the binding gate
	uploadStripeMinBytes = 64 << 10
	uploadConcurrency = 2

	blob := make([]byte, 4*(64<<10))
	for i := range blob {
		blob[i] = byte(i*13 + 5)
	}
	want := sha256.Sum256(blob)

	sink := &stubSink{holdFirst: 600 * time.Millisecond, earlyStarts: map[int64]bool{}}
	backend := httptest.NewServer(sink.handler())
	defer backend.Close()

	resp := socks5Upload(t, newRSTestClient(t, backend.Listener.Addr().String(), rsTestConcurrency, 1<<20), blob)
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
	sink.mu.Lock()
	early := len(sink.earlyStarts)
	third := sink.earlyStarts[2*(64<<10)]
	sink.mu.Unlock()
	if !third {
		t.Fatalf("only %d chunk(s) reached the sink while chunk 0 was held: the slot was not freed until the chunk was durable", early)
	}
}

// TestUploadChunkIsPlannedUnderTheKnobCeiling: upload.chunk_bytes is the
// CEILING. The chunk an object is cut with comes from its length under that
// ceiling, is decided once, and does not move when the knob does mid-object —
// the sink addresses a chunk by its offset and cannot be told a boundary twice.
func TestUploadChunkIsPlannedUnderTheKnobCeiling(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	defer restoreUploadTunables(uploadChunkBytes, uploadMemBytes, uploadStripeMinBytes, uploadConcurrency)()
	uploadChunkBytes, uploadMemBytes, uploadConcurrency = 4<<20, 32<<20, 4

	// No client, so one active tunnel: the 10 MB target is half the object and
	// the 4 MiB ceiling binds, evened to three equal chunks.
	s := &uploadStripe{u: &uploadCandidate{total: 10_000_000}}
	if got := s.tunables().chunk; got != 3_333_334 {
		t.Fatalf("planned chunk = %d, want 3333334", got)
	}

	// The knob capped lower caps the plan of the NEXT object.
	if !skysettings.Apply(map[string]int64{skysettings.UploadChunkBytes: 1 << 20}) {
		t.Fatal("the knob did not take")
	}
	capped := &uploadStripe{u: &uploadCandidate{total: 10_000_000}}
	if got := capped.tunables().chunk; got != 1_000_000 {
		t.Fatalf("with a 1 MiB ceiling the planned chunk = %d, want 1000000", got)
	}
	// ...and the object already cut keeps the size it was cut with.
	if got := s.tunables().chunk; got != 3_333_334 {
		t.Fatalf("the in-flight object's chunk moved to %d", got)
	}
	// The slot arithmetic divides by the PLANNED size, not the ceiling: 32 MiB of
	// memory over 1 MB chunks is 32 buffers, not the 8 the 4 MiB ceiling gives.
	if got := capped.slots(); got != 33 {
		t.Fatalf("slots at a 1000000-byte chunk = %d, want 33", got)
	}
}

// TestStripedUploadChunksAreSizedFromTheObject is the plan on the real path: a
// 10 MB body through the striped upload reaches the sink as equal planned
// chunks, not as 4 MiB steps ending in a 1.6 MB runt that one tunnel carries
// alone (the 10 MB cell ran at half the single-route reference that way —
// bench/2026-09-16/2ca6cf7b3-sweep/sweep/upload.chunk_bytes.tsv).
func TestStripedUploadChunksAreSizedFromTheObject(t *testing.T) {
	defer restoreUploadTunables(uploadChunkBytes, uploadMemBytes, uploadStripeMinBytes, uploadConcurrency)()
	uploadChunkBytes, uploadMemBytes, uploadStripeMinBytes, uploadConcurrency = 4<<20, 32<<20, 4<<20, 4

	const total = 10_000_000
	blob := make([]byte, total)
	for i := range blob {
		blob[i] = byte(i*31 + 7)
	}
	want := sha256.Sum256(blob)

	sink := &stubSink{}
	backend := httptest.NewServer(sink.handler())
	defer backend.Close()

	proxy := newRSTestClient(t, backend.Listener.Addr().String(), rsTestConcurrency, 1<<20)
	resp := socks5Upload(t, proxy, blob)
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
	if got.Bytes != total || got.Sha256 != hex.EncodeToString(want[:]) {
		t.Fatalf("sink saw %d bytes / %s, want %d / %s", got.Bytes, got.Sha256, total, hex.EncodeToString(want[:]))
	}

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.whole != 0 {
		t.Fatalf("%d whole-body POST(s) reached the sink: the upload was not striped", sink.whole)
	}
	// One active tunnel, a 4 MiB ceiling: three chunks of 3,333,334 bytes.
	if len(sink.have) != 3 {
		t.Fatalf("the sink took %d chunk(s), want 3", len(sink.have))
	}
	var smallest, largest int64
	for _, n := range sink.have {
		if n > largest {
			largest = n
		}
		if smallest == 0 || n < smallest {
			smallest = n
		}
	}
	if largest > uploadChunkBytes {
		t.Fatalf("a %d-byte chunk passed the %d ceiling", largest, uploadChunkBytes)
	}
	if smallest*10 < largest*9 {
		t.Fatalf("chunks ran %d..%d bytes: the plan left a runt", smallest, largest)
	}
	t.Logf("10 MB in %d chunks of %d..%d bytes under a %d ceiling", len(sink.have), smallest, largest, uploadChunkBytes)
}
