package skysocksc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	"github.com/stretchr/testify/require"
)

// loadtestUploadRig isolates a test from the package-level session store and
// restores the sink's limits afterwards.
func loadtestUploadRig(t *testing.T, window int64, sessions int) {
	t.Helper()
	oldWindow, oldSessions, oldIdle, oldNow := loadtestUploadWindow, loadtestUploadSessions, loadtestUploadIdle, loadtestNow
	loadtestUploadWindow, loadtestUploadSessions = window, sessions
	loadtestUploadMu.Lock()
	loadtestUploads = map[string]*loadtestUploadSession{}
	loadtestUploadMu.Unlock()
	t.Cleanup(func() {
		loadtestUploadWindow, loadtestUploadSessions, loadtestUploadIdle, loadtestNow = oldWindow, oldSessions, oldIdle, oldNow
		loadtestUploadMu.Lock()
		loadtestUploads = map[string]*loadtestUploadSession{}
		loadtestUploadMu.Unlock()
	})
}

// loadtestUploadPut sends one offset-addressed chunk of an object of total bytes.
func loadtestUploadPut(id string, body []byte, start, total uint64) *httptest.ResponseRecorder {
	end := start + uint64(len(body)) - 1
	req := httptest.NewRequest(http.MethodPut,
		"/upload?id="+id+"&bytes="+strconv.FormatUint(total, 10), bytes.NewReader(body))
	req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, total))
	rec := httptest.NewRecorder()
	loadtestUpload(rec, req)
	return rec
}

// loadtestUploadBody is a deterministic payload whose every chunk differs, so a
// chunk absorbed at the wrong offset cannot hash to the right value.
func loadtestUploadBody(n uint64) []byte {
	b := make([]byte, n)
	loadtestPattern(b, n, 0)
	return b
}

func loadtestUploadReceived(t *testing.T, rec *httptest.ResponseRecorder) uint64 {
	t.Helper()
	v, err := strconv.ParseUint(rec.Header().Get("X-Upload-Received"), 10, 63)
	require.NoError(t, err, "every 2xx carries X-Upload-Received")
	return v
}

// TestLoadtestUploadAdvertisesChunking pins the opt-in signal: the ONE header a
// client may key the chunked path on, plus the limits it must respect.
func TestLoadtestUploadAdvertisesChunking(t *testing.T) {
	loadtestUploadRig(t, 1<<20, 4)
	for _, method := range []string{http.MethodHead, http.MethodOptions} {
		rec := httptest.NewRecorder()
		loadtestUpload(rec, httptest.NewRequest(method, "/upload", nil))
		require.Equal(t, http.StatusOK, rec.Code, method)
		require.Equal(t, "bytes", rec.Header().Get("X-Chunked-Upload"), method)
		require.Equal(t, strconv.FormatInt(1<<20, 10), rec.Header().Get("X-Upload-Window"))
		require.Equal(t, "4", rec.Header().Get("X-Upload-Sessions"))
		require.NotEmpty(t, rec.Header().Get("X-Upload-Idle"))
	}
}

// TestLoadtestUploadInOrderMatchesPlainPost is the contract the bench rests on:
// the chunked path's final answer is the plain POST's answer for the same bytes,
// so a hash check does not care which path carried them.
func TestLoadtestUploadInOrderMatchesPlainPost(t *testing.T) {
	loadtestUploadRig(t, 1<<20, 4)
	const total, chunk = 300_001, 64 * 1024
	body := loadtestUploadBody(total)

	var last *httptest.ResponseRecorder
	for off := 0; off < total; off += chunk {
		end := off + chunk
		if end > total {
			end = total
		}
		rec := loadtestUploadPut("obj", body[off:end], uint64(off), total)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, uint64(end), loadtestUploadReceived(t, rec), "the ack is the durable prefix")
		last = rec
	}
	sum := sha256.Sum256(body)
	require.Equal(t, hex.EncodeToString(sum[:]), last.Header().Get("X-Sha256"))
	require.Contains(t, last.Body.String(), `"bytes":300001`)
	require.Contains(t, last.Body.String(), hex.EncodeToString(sum[:]))

	// The plain POST path is untouched and answers the same thing.
	rec := httptest.NewRecorder()
	loadtestUpload(rec, httptest.NewRequest(http.MethodPost, "/upload", bytes.NewReader(body)))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, last.Body.String(), rec.Body.String(), "same JSON either way")
}

// TestLoadtestUploadAbsorbsOutOfOrderAndDuplicates covers the three arrivals a
// striped sender produces on a lossy path: a chunk ahead of the frontier, a
// re-send of one already durable, and a re-send of one still held.
func TestLoadtestUploadAbsorbsOutOfOrderAndDuplicates(t *testing.T) {
	loadtestUploadRig(t, 1<<20, 4)
	const total, chunk = 4 * 4096, 4096
	body := loadtestUploadBody(total)
	at := func(i int) []byte { return body[i*chunk : (i+1)*chunk] }

	// Chunk 1 arrives first and waits in the window: 202, not 200 — it is held,
	// not durable, and the sender may not drop it yet.
	rec := loadtestUploadPut("obj", at(1), chunk, total)
	require.Equal(t, http.StatusAccepted, rec.Code)
	require.Equal(t, uint64(0), loadtestUploadReceived(t, rec))

	// A re-send of a held chunk replaces it in place.
	rec = loadtestUploadPut("obj", at(1), chunk, total)
	require.Equal(t, http.StatusAccepted, rec.Code)
	require.Equal(t, uint64(0), loadtestUploadReceived(t, rec))

	// Chunk 0 is the frontier: it absorbs itself and drains chunk 1 behind it.
	rec = loadtestUploadPut("obj", at(0), 0, total)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(2*chunk), loadtestUploadReceived(t, rec))

	// A chunk already inside the durable prefix is idempotent, not a re-hash.
	rec = loadtestUploadPut("obj", at(0), 0, total)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(2*chunk), loadtestUploadReceived(t, rec))

	// A chunk that overlaps the frontier is trimmed, not double-counted.
	rec = loadtestUploadPut("obj", body[chunk:3*chunk], chunk, total)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(3*chunk), loadtestUploadReceived(t, rec))

	rec = loadtestUploadPut("obj", at(3), 3*chunk, total)
	require.Equal(t, http.StatusOK, rec.Code)
	sum := sha256.Sum256(body)
	require.Equal(t, hex.EncodeToString(sum[:]), rec.Header().Get("X-Sha256"),
		"the prefix hash equals the object's hash despite the reordering")

	// Once complete the session keeps answering the same thing, for a late
	// re-send or a resume probe.
	rec = loadtestUploadPut("obj", at(2), 2*chunk, total)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), hex.EncodeToString(sum[:]))

	rec = httptest.NewRecorder()
	loadtestUpload(rec, httptest.NewRequest(http.MethodGet, "/upload?id=obj", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(total), loadtestUploadReceived(t, rec))
	require.Contains(t, rec.Body.String(), hex.EncodeToString(sum[:]))

	rec = httptest.NewRecorder()
	loadtestUpload(rec, httptest.NewRequest(http.MethodGet, "/upload?id=nope", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// TestLoadtestUploadWindowFullBacksOff proves the back-pressure a sender needs:
// a chunk too far ahead is refused with a resume offset and a Retry-After, and
// is accepted once the frontier has moved.
func TestLoadtestUploadWindowFullBacksOff(t *testing.T) {
	const chunk = 4096
	loadtestUploadRig(t, 2*chunk, 4) // room for two held chunks
	const total = 8 * chunk
	body := loadtestUploadBody(total)
	at := func(i int) []byte { return body[i*chunk : (i+1)*chunk] }

	// The window is a byte range: [acked, acked+window). Chunk 1 is inside it —
	// held, so 202: received, not committed.
	require.Equal(t, http.StatusAccepted, loadtestUploadPut("obj", at(1), chunk, total).Code)

	// Chunk 2 ends past the window's reach — refused, with the offset to resume
	// from and a Retry-After, so the sender backs off instead of being dropped.
	rec := loadtestUploadPut("obj", at(2), 2*chunk, total)
	require.Equal(t, http.StatusTooEarly, rec.Code, "past the window's reach")
	require.Equal(t, "1", rec.Header().Get("Retry-After"))
	require.Equal(t, "0", rec.Header().Get("X-Next-Offset"), "resume from the durable prefix")

	// Overlapping re-sends inside the reach are held until the buffered bytes
	// would exceed the window, then refused the same way.
	require.Equal(t, http.StatusAccepted, loadtestUploadPut("obj", body[5000:2*chunk], 5000, total).Code)
	rec = loadtestUploadPut("obj", body[6000:2*chunk], 6000, total)
	require.Equal(t, http.StatusTooEarly, rec.Code, "the window is full")
	require.Equal(t, "1", rec.Header().Get("Retry-After"))

	// The frontier is never refused: it evicts a held chunk rather than stall.
	// Eviction is only sound because held chunks were answered 202 and never
	// 200 — a 200'd chunk is durable and may never be dropped.
	evicted := loadtestUploadEvicted.Load()
	rec = loadtestUploadPut("obj", at(0), 0, total)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(2*chunk), loadtestUploadReceived(t, rec), "chunk 1 drained behind it")
	require.Greater(t, loadtestUploadEvicted.Load(), evicted, "the drop is counted, not silent")

	for i := 2; i < 8; i++ {
		rec = loadtestUploadPut("obj", at(i), uint64(i*chunk), total)
		require.Equal(t, http.StatusOK, rec.Code)
	}
	sum := sha256.Sum256(body)
	require.Equal(t, hex.EncodeToString(sum[:]), rec.Header().Get("X-Sha256"))
}

// TestLoadtestUploadBoundsMemory is the OOM guard (#4252 is why): the sink holds
// at most one window per session and at most --upload-sessions sessions, whatever
// the object's size or how badly the chunks are ordered.
func TestLoadtestUploadBoundsMemory(t *testing.T) {
	const chunk = 4096
	loadtestUploadRig(t, 2*chunk, 2)
	const total = 64 * chunk
	body := loadtestUploadBody(total)

	for i := 16; i > 0; i-- { // deliberately reversed, so nothing ever drains
		loadtestUploadPut("obj", body[i*chunk:(i+1)*chunk], uint64(i*chunk), total)
	}
	loadtestUploadMu.Lock()
	s := loadtestUploads["obj"]
	loadtestUploadMu.Unlock()
	require.NotNil(t, s)
	s.mu.Lock()
	held, acked := s.heldBytes, s.acked
	s.mu.Unlock()
	require.Zero(t, acked, "no prefix without chunk 0")
	require.LessOrEqual(t, held, loadtestWindow(), "held bytes never exceed the window")

	// A second session is fine; the third is refused with a Retry-After, so the
	// ceiling is window x sessions.
	require.Equal(t, http.StatusOK, loadtestUploadPut("obj2", body[:chunk], 0, total).Code)
	rec := loadtestUploadPut("obj3", body[:chunk], 0, total)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, "1", rec.Header().Get("Retry-After"))

	// An idle session expires and its buffers go with it, so the refusal is not
	// permanent.
	loadtestNow = func() time.Time { return time.Now().Add(2*loadtestUploadIdle + time.Second) }
	require.Equal(t, http.StatusOK, loadtestUploadPut("obj4", body[:chunk], 0, total).Code)
	loadtestUploadMu.Lock()
	_, stale := loadtestUploads["obj"]
	live := len(loadtestUploads)
	loadtestUploadMu.Unlock()
	require.False(t, stale, "the idle session was expired")
	require.Equal(t, 1, live)
}

// TestLoadtestUploadSlowChunkIsNotIdle is the mid-read eviction: a chunk read
// slower than --upload-idle (the degrade bench's whole shape) must not have its
// session swept out from under it. If it does, the slow reader commits into an
// orphan while the next chunk opens a fresh session at acked=0, and the durable
// prefix walks backwards with no error and no log.
func TestLoadtestUploadSlowChunkIsNotIdle(t *testing.T) {
	const total, chunk = 1 << 20, 3 * loadtestUploadReadStep
	loadtestUploadRig(t, 1<<20, 4)
	body := loadtestUploadBody(total)

	// The handler reads the clock from another goroutine, so the test moves a
	// guarded value rather than swapping the function.
	t0 := time.Now()
	var clockMu sync.Mutex
	clock := t0
	setClock := func(at time.Time) {
		clockMu.Lock()
		defer clockMu.Unlock()
		clock = at
	}
	loadtestNow = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return clock
	}

	pr, pw := io.Pipe()
	req := httptest.NewRequest(http.MethodPut, "/upload?id=slow&bytes="+strconv.Itoa(total), pr)
	req.Header.Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", chunk-1, total))
	rec := httptest.NewRecorder()
	served := make(chan struct{})
	go func() {
		defer close(served)
		loadtestUpload(rec, req)
	}()

	session := func() *loadtestUploadSession {
		loadtestUploadMu.Lock()
		defer loadtestUploadMu.Unlock()
		return loadtestUploads["slow"]
	}
	require.Eventually(t, func() bool {
		s := session()
		if s == nil {
			return false
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.inflight > 0
	}, 5*time.Second, time.Millisecond, "the chunk is admitted and being read")

	// One read step lands, then the clock jumps past the idle limit.
	_, err := pw.Write(body[:loadtestUploadReadStep])
	require.NoError(t, err)
	t1 := t0.Add(2 * loadtestUploadIdle)
	setClock(t1)
	_, err = pw.Write(body[loadtestUploadReadStep : 2*loadtestUploadReadStep])
	require.NoError(t, err)

	// Progress, not admission, is what liveness is measured on: the second step
	// re-stamps the session while the chunk is still incomplete.
	s := session()
	require.NotNil(t, s)
	require.Eventually(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.last.Equal(t1)
	}, 5*time.Second, time.Millisecond, "arriving bytes re-stamp the session")

	// A second upload sweeps, at a clock well past the reading session's last
	// progress. It survives only because it has a chunk in flight.
	setClock(t1.Add(2 * loadtestUploadIdle))
	require.Equal(t, http.StatusOK, loadtestUploadPut("other", body[:1024], 0, total).Code)
	require.Same(t, s, session(), "a session with a chunk in flight is never idle")

	_, err = pw.Write(body[2*loadtestUploadReadStep : chunk])
	require.NoError(t, err)
	require.NoError(t, pw.Close())
	<-served

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(chunk), loadtestUploadReceived(t, rec))
	require.Same(t, s, session(), "it committed into the session it was admitted to")
}

// TestLoadtestUploadSealedSessionFreesItsSlot: a completed upload has nothing
// left to receive and holds no buffers, so it must not spend a concurrency slot
// until it idles out — four completed uploads used to refuse the fifth with 503
// for two minutes. It stays answerable for a late re-send or a resume probe.
func TestLoadtestUploadSealedSessionFreesItsSlot(t *testing.T) {
	const total = 4096
	loadtestUploadRig(t, 1<<20, 2)
	body := loadtestUploadBody(total)
	sum := sha256.Sum256(body)

	for i := 0; i < 2*loadtestUploadSealedMemo; i++ {
		id := fmt.Sprintf("obj%d", i)
		rec := loadtestUploadPut(id, body, 0, total)
		require.Equal(t, http.StatusOK, rec.Code, "upload %d is refused by nothing", i)
		require.Equal(t, hex.EncodeToString(sum[:]), rec.Header().Get("X-Sha256"))
	}

	// The most recent completions still answer a late re-send and a resume
	// probe, and the memo of them is bounded.
	last := fmt.Sprintf("obj%d", 2*loadtestUploadSealedMemo-1)
	rec := loadtestUploadPut(last, body, 0, total)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), hex.EncodeToString(sum[:]))

	rec = httptest.NewRecorder()
	loadtestUpload(rec, httptest.NewRequest(http.MethodGet, "/upload?id="+last, nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(total), loadtestUploadReceived(t, rec))

	loadtestUploadMu.Lock()
	live := len(loadtestUploads)
	loadtestUploadMu.Unlock()
	require.LessOrEqual(t, live, loadtestUploadSealedMemo+1, "sealed sessions are a bounded memo")

	// And an unsealed session still costs a slot: the cap is not defeated.
	require.Equal(t, http.StatusAccepted, loadtestUploadPut("partA", body[2048:], 2048, total).Code)
	require.Equal(t, http.StatusAccepted, loadtestUploadPut("partB", body[2048:], 2048, total).Code)
	require.Equal(t, http.StatusServiceUnavailable, loadtestUploadPut("partC", body[2048:], 2048, total).Code)
}

// TestLoadtestUploadRefusalKeepsTheConnection is the back-pressure protocol over
// a real socket, which every other test skips by calling the handler directly.
// Go's server gives up on a connection once more than 256 KiB of a declared body
// is left unread, so a refusal that did not drain closed the socket under a
// sender still writing its chunk: it takes EPIPE and never reads the
// X-Next-Offset/Retry-After it was supposed to back off on.
func TestLoadtestUploadRefusalKeepsTheConnection(t *testing.T) {
	const total, chunk = 4 << 20, 300 << 10 // a chunk well past the 256 KiB cliff
	loadtestUploadRig(t, 512<<10, 4)
	body := loadtestUploadBody(total)

	srv := httptest.NewServer(http.HandlerFunc(loadtestUpload))
	defer srv.Close()

	var dials atomic.Int64
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dials.Add(1)
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}}

	put := func(start uint64) *http.Response {
		t.Helper()
		end := start + chunk - 1
		req, err := http.NewRequest(http.MethodPut, //nolint:noctx // the test server is local
			srv.URL+"/upload?id=sock&bytes="+strconv.Itoa(total), bytes.NewReader(body[start:end+1]))
		require.NoError(t, err)
		req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, total))
		resp, err := client.Do(req)
		require.NoError(t, err, "the sender reads the answer instead of taking EPIPE")
		_, err = io.Copy(io.Discard, resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp
	}

	// Far past the window's reach: refused, with the offset to resume from.
	resp := put(2 << 20)
	require.Equal(t, http.StatusTooEarly, resp.StatusCode)
	require.Equal(t, "0", resp.Header.Get("X-Next-Offset"))
	require.Equal(t, "1", resp.Header.Get("Retry-After"))
	require.False(t, resp.Close, "the refusal does not close the connection")

	// The frontier chunk then goes down the SAME connection.
	resp = put(0)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, strconv.Itoa(chunk), resp.Header.Get("X-Upload-Received"))
	require.EqualValues(t, 1, dials.Load(), "one connection carried both")
}

// TestLoadtestUploadRejectsMalformedChunks pins the refusals that keep a partial
// or mis-addressed chunk from ever advancing the prefix hash.
func TestLoadtestUploadRejectsMalformedChunks(t *testing.T) {
	loadtestUploadRig(t, 1<<20, 4)
	const total = 4096
	body := loadtestUploadBody(total)

	bad := func(cr string, b []byte) int {
		req := httptest.NewRequest(http.MethodPut, "/upload?id=obj&bytes=4096", bytes.NewReader(b))
		req.Header.Set("Content-Range", cr)
		rec := httptest.NewRecorder()
		loadtestUpload(rec, req)
		return rec.Code
	}
	require.Equal(t, http.StatusBadRequest, bad("bytes */4096", body))
	require.Equal(t, http.StatusBadRequest, bad("bytes 0-4095/0", body))
	require.Equal(t, http.StatusBadRequest, bad("bytes 4096-8191/4096", body), "past the object")
	require.Equal(t, http.StatusBadRequest, bad("bytes 0-4095/4096", body[:10]), "Content-Length disagrees")
	require.Equal(t, http.StatusRequestEntityTooLarge, func() int {
		loadtestUploadRig(t, 1024, 4)
		return bad("bytes 0-4095/4096", body)
	}())

	// A size that disagrees with an existing session is a conflict, not a
	// silently corrupted object.
	loadtestUploadRig(t, 1<<20, 4)
	require.Equal(t, http.StatusOK, loadtestUploadPut("obj", body[:1024], 0, total).Code)
	req := httptest.NewRequest(http.MethodPut, "/upload?id=obj&bytes=8192", bytes.NewReader(body[:1024]))
	req.Header.Set("Content-Range", "bytes 0-1023/8192")
	rec := httptest.NewRecorder()
	loadtestUpload(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)

	// bytes= must agree with the Content-Range it accompanies.
	req = httptest.NewRequest(http.MethodPut, "/upload?id=obj&bytes=9999", bytes.NewReader(body[:1024]))
	req.Header.Set("Content-Range", "bytes 0-1023/4096")
	rec = httptest.NewRecorder()
	loadtestUpload(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// An unsupported verb is still refused.
	rec = httptest.NewRecorder()
	loadtestUpload(rec, httptest.NewRequest(http.MethodDelete, "/upload", nil))
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

// TestParseContentRange covers the parser on its own.
func TestParseContentRange(t *testing.T) {
	s, e, n, ok := parseContentRange("bytes 100-199/1000")
	require.True(t, ok)
	require.Equal(t, []uint64{100, 199, 1000}, []uint64{s, e, n})
	_, _, _, ok = parseContentRange(" bytes 0-0/1 ")
	require.True(t, ok)
	for _, bad := range []string{"", "bytes */1000", "bytes 100-99/1000", "bytes 100-199", "items 1-2/3", "bytes 0-1000/1000", "bytes a-b/c"} {
		_, _, _, ok = parseContentRange(bad)
		require.False(t, ok, bad)
	}
}

// TestLoadtestUploadConcurrentChunks is the striped sender's shape: several
// chunks of one object in flight at once, arriving in no particular order.
func TestLoadtestUploadConcurrentChunks(t *testing.T) {
	loadtestUploadRig(t, 1<<20, 4)
	const total, chunk = 32 * 1024, 4096
	body := loadtestUploadBody(total)

	order := []uint64{5, 2, 7, 0, 3, 6, 1, 4}
	done := make(chan struct{}, len(order))
	for _, i := range order {
		go func(i uint64) {
			defer func() { done <- struct{}{} }()
			// The reference client: a chunk is done only when it is inside the
			// durable prefix — a 200, or an X-Upload-Received past its end. A 202
			// (held, still evictable) and a 425 (refused) both mean re-send.
			for {
				rec := loadtestUploadPut("obj", body[i*chunk:(i+1)*chunk], i*chunk, total)
				switch rec.Code {
				case http.StatusOK:
					return
				case http.StatusAccepted:
					v, err := strconv.ParseUint(rec.Header().Get("X-Upload-Received"), 10, 63)
					if err == nil && v >= (i+1)*chunk {
						return
					}
				case http.StatusTooEarly:
				default:
					t.Errorf("chunk %d: unexpected %d", i, rec.Code)
					return
				}
			}
		}(i)
	}
	for range order {
		<-done
	}
	rec := httptest.NewRecorder()
	loadtestUpload(rec, httptest.NewRequest(http.MethodGet, "/upload?id=obj", nil))
	sum := sha256.Sum256(body)
	require.Equal(t, uint64(total), loadtestUploadReceived(t, rec))
	require.True(t, strings.Contains(rec.Body.String(), hex.EncodeToString(sum[:])))
}

// TestLoadtestUploadEvictionNamesTheOffsets: a held chunk dropped to admit the
// frontier is NAMED on the very next response. Without it the sender learns of
// the drop only from a prefix that stopped moving — a multi-second timeout per
// eviction, which is what turned a cut tunnel into a failed 50 MB object
// (bench/2026-09-16/3194b7cc8-smoke).
func TestLoadtestUploadEvictionNamesTheOffsets(t *testing.T) {
	const chunk = 4096
	loadtestUploadRig(t, 4*chunk, 4)
	const total = 8 * chunk
	body := loadtestUploadBody(total)
	at := func(i int) []byte { return body[i*chunk : (i+1)*chunk] }

	// Three chunks ahead of the frontier, all inside the window's reach.
	for i := 1; i <= 3; i++ {
		require.Equal(t, http.StatusAccepted,
			loadtestUploadPut("obj", at(i), uint64(i*chunk), total).Code)
	}

	// A frontier chunk that does not fit beside them evicts the furthest held
	// one — and says which one, on its own ack.
	rec := loadtestUploadPut("obj", body[:2*chunk], 0, total)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, strconv.Itoa(3*chunk), rec.Header().Get("X-Upload-Evicted"),
		"the dropped chunk is named on the ack, not left to a timeout")
	require.Equal(t, uint64(3*chunk), loadtestUploadReceived(t, rec), "chunks 1 and 2 drained behind it")

	// The notice is drained: the re-send's own ack does not repeat it.
	rec = loadtestUploadPut("obj", at(3), 3*chunk, total)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, rec.Header().Get("X-Upload-Evicted"), "a notice is delivered once")

	// Nothing was evicted on a clean object, so an old client sees no new header.
	rec = loadtestUploadPut("clean", at(0), 0, total)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, rec.Header().Get("X-Upload-Evicted"))
}

// TestLoadtestUploadEvictionNoticeIsBounded: the notice queue cannot grow
// without limit however many chunks are dropped.
func TestLoadtestUploadEvictionNoticeIsBounded(t *testing.T) {
	s := &loadtestUploadSession{held: map[uint64][]byte{}}
	for i := 0; i < loadtestUploadEvictNotices*3; i++ {
		s.held[uint64(i)] = []byte{0}
		s.heldBytes++
		s.evictFurthest()
	}
	require.Len(t, s.evicted, loadtestUploadEvictNotices)
	require.NotEmpty(t, s.takeEvicted())
	require.Empty(t, s.takeEvicted(), "draining leaves nothing behind")
}
