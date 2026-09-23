// Package loadtest pkg/loadtest/sink.go c4-app-proxy
//
// The bench sink: the far end of `skywire cli proxy loadtest serve`, and the
// same handler the in-process mux benches run against. It lives in its own
// package so a test can mount it on an httptest server without pulling in the
// CLI.
//
//   - GET /              endless chunked octet-stream at the reader's line rate
//   - GET /?bytes=N      N bytes of a deterministic pattern, byte ranges (206 +
//     Content-Range) and X-Sha256 over the whole object
//   - POST|PUT /upload   whole-body or acked, offset-addressed chunked uploads
//
// See the chunked-upload contract below.
package loadtest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Handler is the sink's routes: the endless and fixed byte source on "/" and
// the upload sink on "/upload".
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/upload", serveUpload)
	mux.HandleFunc("/", serveRoot)
	return mux
}

// serveRoot is the byte source: ?bytes=N for a finite, certified object, and
// otherwise an endless stream paced by the reader (TCP backpressure sets the
// rate; the source itself never gaps).
func serveRoot(w http.ResponseWriter, r *http.Request) {
	if n := r.URL.Query().Get("bytes"); n != "" {
		serveFixed(w, r, n)
		return
	}
	buf := make([]byte, 256*1024) // zeros
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	fl, _ := w.(http.Flusher)
	for {
		if _, err := w.Write(buf); err != nil {
			return
		}
		if fl != nil {
			fl.Flush()
		}
		select {
		case <-r.Context().Done():
			return
		default:
		}
	}
}

// pattern fills buf with a deterministic byte pattern derived from
// seed and the absolute offset, so the same request always produces the same
// bytes and a corrupted or truncated transfer cannot hash to the expected
// value. An xorshift over the offset is cheap and has no long runs of one
// byte, unlike zeros, which would let a stalled reader look like progress
// under some compressing transports.
func pattern(buf []byte, seed, offset uint64) {
	x := seed ^ (offset * 0x9E3779B97F4A7C15)
	for i := range buf {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		buf[i] = byte(x & 0xff)
	}
}

// chunkBytes is the pattern's seeding granularity: the body is generated (and
// hashed) 256 KiB at a time from the ABSOLUTE offset, so any byte range is a
// slice of its aligned chunks and no range needs the bytes before it.
const chunkBytes = 256 * 1024

// sumEntry is one cached whole-object hash. ready is closed once sum is
// set, so the concurrent ranged GETs of a single range-split download share the
// one computation instead of each starting their own.
type sumEntry struct {
	ready chan struct{}
	sum   string
}

// sumCacheMax bounds the cache. The bench uses a handful of sizes; past
// the cap a size is hashed per request, exactly as before.
const sumCacheMax = 64

var (
	sumMu    sync.Mutex
	sums     = map[uint64]*sumEntry{}
	sumCalcs atomic.Uint64 // whole-object hash computations (the test asserts the cache holds)
)

// objectSum returns the hex SHA-256 of the whole n-byte body, computing it at
// most once per n. The body is a pure function of n, so the hash is too — but it
// used to be recomputed on EVERY request, including each ranged GET of a
// range-split download: 12 chunks of a 50 MB object hashed 600 MB on a 2-core
// exit, ~1.6 s of CPU that the equivalent single GET never paid, landing in
// time-to-first-byte and taxing the split for being a split.
func objectSum(n uint64) string {
	sumMu.Lock()
	e, hit := sums[n]
	if hit {
		sumMu.Unlock()
		<-e.ready
		return e.sum
	}
	e = &sumEntry{ready: make(chan struct{})}
	if len(sums) < sumCacheMax {
		sums[n] = e
	}
	sumMu.Unlock()

	sumCalcs.Add(1)
	buf := make([]byte, chunkBytes)
	h := sha256.New()
	for off := uint64(0); off < n; off += chunkBytes {
		m := uint64(chunkBytes)
		if n-off < m {
			m = n - off
		}
		pattern(buf[:m], n, off)
		h.Write(buf[:m]) //nolint:errcheck,gosec // hash.Hash never errors
	}
	e.sum = hex.EncodeToString(h.Sum(nil))
	close(e.ready)
	return e.sum
}

// serveFixed serves exactly n bytes of the pattern with the whole object's
// SHA-256 in X-Sha256 (cached per n by objectSum, so a ranged GET generates
// only the bytes it serves).
func serveFixed(w http.ResponseWriter, r *http.Request, nStr string) {
	n, err := strconv.ParseUint(nStr, 10, 63)
	if err != nil || n == 0 {
		http.Error(w, "bytes: want a positive integer", http.StatusBadRequest)
		return
	}
	const chunk = chunkBytes
	// Byte ranges (RFC 9110 §14): the proxy's transparent range-splitter fetches
	// a range-capable :80 origin as concurrent chunks over separate tunnels, so
	// the sink serves any byte range of the same deterministic body. The
	// pattern is seeded per 256 KiB chunk, so a range is produced from its
	// aligned chunks and sliced. X-Sha256 always certifies the WHOLE body.
	w.Header().Set("Accept-Ranges", "bytes")
	start, end := uint64(0), n-1
	status := http.StatusOK
	if rh := r.Header.Get("Range"); rh != "" {
		s, e, ok := parseByteRange(rh, n)
		if !ok {
			w.Header().Set("Content-Range", "bytes */"+strconv.FormatUint(n, 10))
			http.Error(w, "range not satisfiable", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		start, end, status = s, e, http.StatusPartialContent
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, n))
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.FormatUint(end-start+1, 10))
	w.Header().Set("X-Sha256", objectSum(n))
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	buf := make([]byte, chunk)
	fl, _ := w.(http.Flusher)
	for off := start - start%chunk; off <= end; off += chunk {
		m := uint64(chunk)
		if n-off < m {
			m = n - off
		}
		pattern(buf[:m], n, off)
		lo, hi := uint64(0), m
		if off < start {
			lo = start - off
		}
		if off+m-1 > end {
			hi = end - off + 1
		}
		if _, err := w.Write(buf[lo:hi]); err != nil {
			return
		}
		if fl != nil {
			fl.Flush()
		}
	}
}

// parseByteRange parses one "bytes=a-b", "bytes=a-" or "bytes=-k" range
// against a body of n bytes into an inclusive [start, end]. ok is false for a
// multi-range, an unparseable or an unsatisfiable request.
func parseByteRange(h string, n uint64) (start, end uint64, ok bool) {
	spec, found := strings.CutPrefix(h, "bytes=")
	if !found || strings.Contains(spec, ",") {
		return 0, 0, false
	}
	a, b, found := strings.Cut(spec, "-")
	if !found {
		return 0, 0, false
	}
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	switch {
	case a == "" && b != "": // suffix: the last k bytes
		k, err := strconv.ParseUint(b, 10, 63)
		if err != nil || k == 0 {
			return 0, 0, false
		}
		if k > n {
			k = n
		}
		return n - k, n - 1, true
	case a != "":
		s, err := strconv.ParseUint(a, 10, 63)
		if err != nil || s >= n {
			return 0, 0, false
		}
		e := n - 1
		if b != "" {
			v, err := strconv.ParseUint(b, 10, 63)
			if err != nil || v < s {
				return 0, 0, false
			}
			if v < e {
				e = v
			}
		}
		return s, e, true
	}
	return 0, 0, false
}

// ---------------------------------------------------------------------------
// Chunked, resumable uploads.
//
// A plain POST is one stream: if the tunnel under it dies the exit has consumed
// an unknown prefix, the sender cannot say where to resume, and the transfer is
// lost (measured: 30/45/44 MB of 50 delivered, hash_ok 0/3, on the same cut that
// downloads survive in 0.6-1.2 s). Downloads survive because a range addresses a
// chunk independently and a chunk can be re-fetched on a live tunnel. This gives
// the upload direction the same property: a chunk is addressed by its offset and
// is durable only once it is acked, so the sender may re-send it anywhere.
//
// The sink never holds the object. The SHA-256 is rolled over the CONTIGUOUS
// PREFIX as chunks are absorbed; chunks that arrive ahead of the frontier wait
// in a bounded reorder window (--upload-window, 64 MiB) and are absorbed when the
// frontier reaches them. Memory is therefore O(window x sessions), not O(object).
//
// Contract:
//
//	HEAD|OPTIONS /upload            -> X-Chunked-Upload: bytes (the opt-in signal),
//	                                   X-Upload-Window/-Sessions/-Idle
//	PUT /upload?id=<id>&bytes=<N>   -> with Content-Range: bytes <s>-<e>/<N>;
//	                                   200 = in the durable prefix, 202 = only
//	                                   held out of order (evictable, re-send it);
//	                                   both carry X-Upload-Received: <acked prefix>
//	                                   and, when a held chunk was just dropped,
//	                                   X-Upload-Evicted: <start>[,<start>...]
//	GET /upload?id=<id>             -> the same counters, for a resume probe
//	POST /upload                    -> unchanged whole-body sink (the control arm)
//
// The chunk that completes the object answers with the plain POST's JSON, so a
// hash check does not care which path carried the bytes.
//
// Harness (SINK=host:port; one 8 MiB object in four 2 MiB chunks, sent out of
// order, with one duplicate, ending on a hash the sender can compare):
//
//	head -c 8388608 /dev/urandom > /tmp/p; sha256sum /tmp/p
//	curl -sI "http://$SINK/upload" | grep -i x-chunked-upload
//	id=$(date +%s%N); put() { dd if=/tmp/p bs=2097152 skip=$1 count=1 2>/dev/null | curl -sS -X PUT -H "Expect:" -H "Content-Range: bytes $((2097152*$1))-$((2097152*($1+1)-1))/8388608" --data-binary @- -D- -o- "http://$SINK/upload?id=$id&bytes=8388608"; }
//	put 1; put 0; put 1; put 3; put 2
//	curl -sS "http://$SINK/upload?id=$id"
//
// put 1 is held (out of order), put 0 absorbs both, the second put 1 is a
// duplicate and is answered without re-hashing, and put 2 completes the object.

var (
	// UploadWindow bounds the bytes ONE session may buffer: chunks held
	// ahead of the frontier plus the chunks being read. A frontier chunk is
	// always admitted (held chunks are evicted for it if need be, and re-sent by
	// the client, which got 202 and not 200 for them), so the window can never
	// deadlock a session.
	//
	// 64 MiB is headroom, not an appetite: the striped client sizes its live
	// chunk buffers as min(its own 16 MiB memory cap / 4 MiB chunk, window/chunk
	// - 2), so a 16 MiB window left it two live chunks — one per tunnel, with an
	// ack round trip between them — and cost ~20% of single-upload throughput.
	// At 64 MiB the client's memory cap (4 chunks) binds again, and inflight
	// plus held never comes near the window.
	UploadWindow int64 = 64 << 20
	// UploadSessions caps concurrent sessions, so the sink's ceiling is
	// window x sessions (256 MiB by default) on an exit that has been OOM-killed
	// for less (#4252).
	UploadSessions = 4
	// UploadIdle expires a session that stopped making progress; its
	// buffers go with it.
	UploadIdle = 2 * time.Minute
)

// nowFn is the sink's clock, so the idle expiry is testable.
var nowFn = time.Now

const (
	// uploadReadStep is how much of a chunk is read between progress
	// stamps. A chunk read slower than --upload-idle is exactly what the degrade
	// bench produces, and a session evicted mid-read would commit into an orphan
	// and walk its prefix backwards, so liveness is measured on BYTES ARRIVING,
	// not on when the chunk was admitted.
	uploadReadStep = 256 << 10
	// uploadEvictNotices bounds the eviction notices one session queues
	// for its next response. Past it the sender falls back to noticing the drop
	// itself, so the header can never grow without limit.
	uploadEvictNotices = 32
	// uploadSealedMemo is how many completed objects stay answerable for
	// a late re-send or a resume probe. Sealed sessions hold no chunk buffers and
	// do not occupy a concurrency slot; past this many, the oldest is dropped.
	uploadSealedMemo = 8
)

var (
	// uploadExpired counts sessions dropped by the idle sweep, and
	// uploadEvicted counts held chunks dropped to admit a frontier chunk.
	// Both lose bytes a sender must re-send, so neither may be silent.
	uploadExpired atomic.Uint64
	uploadEvicted atomic.Uint64
)

// windowBytes is the reorder window as an unsigned bound, so all the offset
// arithmetic stays in one signedness. A non-positive flag value means no room
// for anything out of order, not an enormous window.
func windowBytes() uint64 {
	if UploadWindow <= 0 {
		return 0
	}
	return uint64(UploadWindow)
}

// uploadSession is one object in flight. hash rolls over the contiguous
// prefix only — the object itself is never held — and held keeps the chunks that
// arrived ahead of acked, keyed by their start offset.
type uploadSession struct {
	mu        sync.Mutex
	total     uint64 // immutable after creation
	acked     uint64 // contiguous prefix absorbed into hash
	hash      hash.Hash
	sum       string // set once acked == total
	held      map[uint64][]byte
	heldBytes uint64
	inflight  uint64 // admitted chunks still being read off the wire
	// evicted names the start offsets dropped from held since the last response
	// went out. A dropped chunk was answered 202 and only its sender can put it
	// back, so the drop is TOLD to the sender on the very next ack
	// (X-Upload-Evicted) instead of being inferred from a prefix that stopped
	// moving — which costs the sender a multi-second timeout per eviction.
	evicted []uint64
	last    time.Time
}

var (
	uploadMu sync.Mutex
	uploads  = map[string]*uploadSession{}
)

// serveUpload is the upload-direction sink. A plain POST (no id, no
// Content-Range) is the unchanged whole-body path; an id with a Content-Range is
// a chunk of a resumable object.
func serveUpload(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodHead, http.MethodOptions:
		uploadAdvertise(w)
		return
	case http.MethodGet:
		uploadStatus(w, r)
		return
	case http.MethodPost, http.MethodPut:
	default:
		uploadRefuse(w, r, http.StatusMethodNotAllowed, "POST or PUT a body")
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	cr := strings.TrimSpace(r.Header.Get("Content-Range"))
	if id == "" && cr == "" {
		uploadWhole(w, r)
		return
	}
	uploadChunk(w, r, id, cr)
}

// uploadRefuse answers a request whose body will not be absorbed. Go's
// server gives up on the connection once more than 256 KiB of a declared body is
// left unread, so a refusal that just returns closes the socket under a sender
// that is still writing its chunk: it takes EPIPE and never reads the
// X-Next-Offset/Retry-After the refusal was carrying, which is the whole
// back-pressure protocol. Reading the chunk out of the way first costs no memory
// (it goes to io.Discard) and keeps the connection reusable; a body too large to
// be any chunk of ours gets a deliberate, documented close instead.
func uploadRefuse(w http.ResponseWriter, r *http.Request, code int, msg string) {
	if !uploadDrain(r) {
		w.Header().Set("Connection", "close")
	}
	http.Error(w, msg, code)
}

// uploadDrain discards up to one window's worth of the body — the
// largest chunk the sink would ever have admitted. It reports whether the body
// was fully consumed.
func uploadDrain(r *http.Request) bool {
	if r.Body == nil {
		return true
	}
	limit := int64(windowBytes()) //nolint:gosec // G115: windowBytes is bounded by the flag
	if limit < 1<<20 {
		limit = 1 << 20
	}
	n, _ := io.CopyN(io.Discard, r.Body, limit+1) //nolint:errcheck // a read error is a dead conn either way
	return n <= limit
}

// uploadWhole reads and discards the body and answers with the byte
// count and SHA-256 it saw, so the sender can check both against what it sent.
func uploadWhole(w http.ResponseWriter, r *http.Request) {
	h := sha256.New()
	n, err := io.Copy(h, r.Body)
	if err != nil {
		http.Error(w, "read: "+err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"bytes": n, "sha256": hex.EncodeToString(h.Sum(nil))}) //nolint:errcheck
}

// uploadAdvertise is the opt-in signal. X-Chunked-Upload is the ONLY
// thing a client may key the chunked path on: sending a partial body to an
// origin that did not advertise it is data corruption.
func uploadAdvertise(w http.ResponseWriter) {
	w.Header().Set("X-Chunked-Upload", "bytes")
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("X-Upload-Window", strconv.FormatInt(UploadWindow, 10))
	w.Header().Set("X-Upload-Sessions", strconv.Itoa(UploadSessions))
	w.Header().Set("X-Upload-Idle", UploadIdle.String())
	w.Header().Set("Allow", "GET, HEAD, OPTIONS, POST, PUT")
	w.WriteHeader(http.StatusOK)
}

// uploadStatus answers a resume probe: how much of the object is
// durable, and the hash once it is whole.
func uploadStatus(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "id: want the upload session id", http.StatusBadRequest)
		return
	}
	uploadMu.Lock()
	s := uploads[id]
	uploadMu.Unlock()
	if s == nil {
		http.Error(w, "no such upload session", http.StatusNotFound)
		return
	}
	s.mu.Lock()
	s.last = nowFn()
	acked, sum, total, drops := s.acked, s.sum, s.total, s.takeEvicted()
	s.mu.Unlock()
	uploadSetEvicted(w, drops)
	uploadAck(w, http.StatusOK, id, "", acked, total, sum)
}

// uploadChunk absorbs one offset-addressed chunk.
func uploadChunk(w http.ResponseWriter, r *http.Request, id, cr string) {
	if id == "" {
		uploadRefuse(w, r, http.StatusBadRequest, "id: required with Content-Range")
		return
	}
	if len(id) > 128 {
		uploadRefuse(w, r, http.StatusBadRequest, "id: too long")
		return
	}
	start, end, total, ok := parseContentRange(cr)
	if !ok {
		uploadRefuse(w, r, http.StatusBadRequest, "Content-Range: want 'bytes <start>-<end>/<total>'")
		return
	}
	if q := r.URL.Query().Get("bytes"); q != "" {
		n, err := strconv.ParseUint(q, 10, 63)
		if err != nil || n != total {
			uploadRefuse(w, r, http.StatusBadRequest, "bytes: disagrees with Content-Range")
			return
		}
	}
	length := end - start + 1
	if length > windowBytes() {
		uploadRefuse(w, r, http.StatusRequestEntityTooLarge, "chunk larger than the reorder window")
		return
	}
	if r.ContentLength >= 0 && uint64(r.ContentLength) != length { //nolint:gosec // G115: guarded non-negative
		uploadRefuse(w, r, http.StatusBadRequest, "Content-Length disagrees with Content-Range")
		return
	}

	s, code, msg := uploadSessionFor(id, total)
	if s == nil {
		if code == http.StatusServiceUnavailable {
			w.Header().Set("Retry-After", "1")
		}
		uploadRefuse(w, r, code, msg)
		return
	}

	// Admission, then the read, then the commit — the read is NOT done under the
	// session lock, so the concurrent chunks of one object do not serialize. The
	// admitted length is reserved across the read, so the window bounds what is
	// actually in memory.
	s.mu.Lock()
	s.last = nowFn()
	if s.sum != "" || end < s.acked { // already durable: absorb the resend, do not re-hash
		acked, sum, drops := s.acked, s.sum, s.takeEvicted()
		s.mu.Unlock()
		_, _ = io.Copy(io.Discard, r.Body) //nolint:errcheck
		uploadSetEvicted(w, drops)
		uploadAck(w, http.StatusOK, id, cr, acked, total, sum)
		return
	}
	if start > s.acked {
		// Ahead of the frontier: it must fit the window, or the client backs off
		// and re-sends it later.
		if end >= s.acked+windowBytes() || s.inflight+s.heldBytes+length > windowBytes() {
			acked, drops := s.acked, s.takeEvicted()
			s.mu.Unlock()
			uploadSetEvicted(w, drops)
			w.Header().Set("X-Next-Offset", strconv.FormatUint(acked, 10))
			w.Header().Set("X-Upload-Received", strconv.FormatUint(acked, 10))
			w.Header().Set("Retry-After", "1")
			uploadRefuse(w, r, http.StatusTooEarly, "reorder window full")
			return
		}
	} else {
		// The frontier chunk always wins: held chunks are not durable (they were
		// answered 202, not 200), so evicting the furthest of them only costs a
		// re-send, while refusing the frontier would stall the object forever.
		for s.inflight+s.heldBytes+length > windowBytes() && len(s.held) > 0 {
			s.evictFurthest()
		}
		if s.inflight+s.heldBytes+length > windowBytes() {
			acked, drops := s.acked, s.takeEvicted()
			s.mu.Unlock()
			uploadSetEvicted(w, drops)
			w.Header().Set("X-Next-Offset", strconv.FormatUint(acked, 10))
			w.Header().Set("X-Upload-Received", strconv.FormatUint(acked, 10))
			w.Header().Set("Retry-After", "1")
			uploadRefuse(w, r, http.StatusTooEarly, "reorder window full")
			return
		}
	}
	s.inflight += length
	s.mu.Unlock()

	buf := make([]byte, length)
	if err := s.readChunk(r.Body, buf); err != nil {
		s.mu.Lock()
		s.inflight -= length
		s.mu.Unlock()
		// A short chunk never advances the prefix hash; the sender re-sends it.
		uploadRefuse(w, r, http.StatusBadRequest, "short chunk: "+err.Error())
		return
	}

	ackCode := http.StatusOK
	s.mu.Lock()
	s.inflight -= length
	s.last = nowFn()
	switch {
	case s.sum != "" || end < s.acked: // it became durable while we read
	case start <= s.acked: // the frontier, possibly overlapping: trim and absorb
		s.absorb(buf[s.acked-start:])
		s.drain()
	default: // ahead of the frontier: hold it (a re-send of a held chunk replaces it)
		if old, dup := s.held[start]; dup {
			s.heldBytes -= uint64(len(old))
		}
		s.held[start] = buf
		s.heldBytes += length
		// Held is NOT durable: the next frontier chunk may evict it. 202 says
		// "received, not committed" — only a 200 (or an X-Upload-Received past
		// the chunk's end) releases the sender from re-sending it.
		ackCode = http.StatusAccepted
	}
	s.finish()
	acked, sum, drops := s.acked, s.sum, s.takeEvicted()
	s.mu.Unlock()
	uploadSetEvicted(w, drops)
	uploadAck(w, ackCode, id, cr, acked, total, sum)
}

// readChunk reads one admitted chunk, stamping the session's liveness as the
// bytes arrive. Stamping only at admission would let the idle sweep evict a
// session whose chunk is merely slow — the degrade bench's whole shape — and the
// slow reader would then commit into an orphaned session while the next chunk
// opened a fresh one, walking the durable prefix backwards with no error and no
// log.
func (s *uploadSession) readChunk(body io.Reader, buf []byte) error {
	for off := 0; off < len(buf); {
		end := off + uploadReadStep
		if end > len(buf) {
			end = len(buf)
		}
		n, err := io.ReadFull(body, buf[off:end])
		off += n
		if n > 0 {
			s.mu.Lock()
			s.last = nowFn()
			s.mu.Unlock()
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// absorb folds bytes that start exactly at the frontier into the rolling hash.
func (s *uploadSession) absorb(b []byte) {
	if len(b) == 0 {
		return
	}
	s.hash.Write(b) //nolint:errcheck,gosec // hash.Hash never errors
	s.acked += uint64(len(b))
}

// drain absorbs every held chunk the frontier has reached, repeatedly, since
// absorbing one can make the next contiguous.
func (s *uploadSession) drain() {
	for {
		progressed := false
		for st, b := range s.held {
			end := st + uint64(len(b)) - 1
			if end < s.acked { // wholly superseded
				delete(s.held, st)
				s.heldBytes -= uint64(len(b))
				progressed = true
				continue
			}
			if st <= s.acked {
				s.absorb(b[s.acked-st:])
				delete(s.held, st)
				s.heldBytes -= uint64(len(b))
				progressed = true
			}
		}
		if !progressed {
			return
		}
	}
}

// evictFurthest drops the held chunk with the highest start offset to make room
// for the frontier. It was never acked, so the client re-sends it.
func (s *uploadSession) evictFurthest() {
	var (
		at    uint64
		found bool
	)
	for st := range s.held {
		if !found || st > at {
			at, found = st, true
		}
	}
	if !found {
		return
	}
	s.heldBytes -= uint64(len(s.held[at]))
	delete(s.held, at)
	if len(s.evicted) < uploadEvictNotices {
		s.evicted = append(s.evicted, at)
	}
	uploadEvicted.Add(1)
}

// takeEvicted drains the pending eviction notices as a header value, called
// with s.mu held. Empty when nothing was dropped, which is the common case —
// the header is then absent and an old client sees exactly what it saw before.
func (s *uploadSession) takeEvicted() string {
	if len(s.evicted) == 0 {
		return ""
	}
	var b strings.Builder
	for i, off := range s.evicted {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatUint(off, 10))
	}
	s.evicted = s.evicted[:0]
	return b.String()
}

// uploadSetEvicted names the dropped chunks on the response being
// built. It is set before the status line is written, so it rides every answer
// the handler makes — an ack, or the 425 the eviction was making room for.
func uploadSetEvicted(w http.ResponseWriter, list string) {
	if list != "" {
		w.Header().Set("X-Upload-Evicted", list)
	}
}

// finish seals the object once the prefix is whole.
func (s *uploadSession) finish() {
	if s.sum != "" || s.acked < s.total {
		return
	}
	s.sum = hex.EncodeToString(s.hash.Sum(nil))
	s.held = map[uint64][]byte{}
	s.heldBytes = 0
}

// uploadAck writes the per-chunk ack. code is 200 when the chunk is
// inside the durable contiguous prefix and 202 when it is only held in the
// reorder window — a held chunk can still be evicted for the frontier, so the
// sender must keep it. X-Upload-Received is the durable contiguous prefix — the
// only offset a sender may resume from. The chunk that completes the object
// answers with the plain POST path's JSON, so the bench's hash check does not
// change.
func uploadAck(w http.ResponseWriter, code int, id, cr string, acked, total uint64, sum string) {
	w.Header().Set("X-Upload-Id", id)
	w.Header().Set("X-Upload-Received", strconv.FormatUint(acked, 10))
	if cr != "" {
		w.Header().Set("Content-Range", cr)
	}
	w.Header().Set("Content-Type", "application/json")
	if sum != "" {
		w.Header().Set("X-Sha256", sum)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"bytes": total, "sha256": sum}) //nolint:errcheck
		return
	}
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"received": acked, "total": total}) //nolint:errcheck
}

// uploadSweep drops the sessions that no longer earn their memory and
// reports how many still occupy a concurrency slot. It is called with
// uploadMu held; keep is the id this request is for, which is never
// reaped out from under it.
//
// Two rules a plain "delete if idle" sweep got wrong:
//
//   - a session with a chunk still being READ is not idle, whatever the clock
//     says. Evicting it mid-read orphans the reader, which then commits into a
//     session nobody can find while the next chunk opens a fresh one at acked=0
//     — the durable prefix walks backwards with no error and no log, and the
//     orphan plus its replacement break the window x sessions ceiling;
//   - a SEALED session holds no buffers and has nothing left to receive, so it
//     must not deny the next upload a slot for a whole --upload-idle: four
//     completed uploads used to refuse the fifth with 503 for two minutes. It
//     stays answerable for a late re-send or a resume probe until the memo fills.
func uploadSweep(keep string) int {
	type sealedSession struct {
		id   string
		last time.Time
	}
	now := nowFn()
	live := 0
	var sealed []sealedSession
	for k, v := range uploads {
		v.mu.Lock()
		idle, busy, done, last := now.Sub(v.last), v.inflight > 0, v.sum != "", v.last
		v.mu.Unlock()
		if !busy && idle > UploadIdle && k != keep {
			delete(uploads, k)
			uploadExpired.Add(1)
			if !done {
				fmt.Fprintf(os.Stderr,
					"loadtest serve: upload %q expired after %s idle; its held chunks are dropped and must be re-sent\n",
					k, idle.Truncate(time.Second))
			}
			continue
		}
		if done {
			sealed = append(sealed, sealedSession{id: k, last: last})
			continue
		}
		live++
	}
	if len(sealed) > uploadSealedMemo {
		sort.Slice(sealed, func(i, j int) bool { return sealed[i].last.Before(sealed[j].last) })
		for _, e := range sealed[:len(sealed)-uploadSealedMemo] {
			if e.id == keep {
				continue
			}
			delete(uploads, e.id)
		}
	}
	return live
}

// uploadSessionFor finds or opens a session, expiring idle ones first so
// a stalled upload's buffers are not what refuses the next one. It returns a
// status code and message instead of a session when it refuses.
func uploadSessionFor(id string, total uint64) (*uploadSession, int, string) {
	uploadMu.Lock()
	defer uploadMu.Unlock()
	live := uploadSweep(id)
	if s, ok := uploads[id]; ok {
		if s.total != total {
			return nil, http.StatusConflict, "id is in use for an object of a different size"
		}
		return s, 0, ""
	}
	if live >= UploadSessions {
		return nil, http.StatusServiceUnavailable, "too many concurrent upload sessions"
	}
	s := &uploadSession{
		total: total,
		hash:  sha256.New(),
		held:  map[uint64][]byte{},
		last:  nowFn(),
	}
	uploads[id] = s
	return s, 0, ""
}

// parseContentRange parses "bytes <start>-<end>/<total>" into an inclusive
// range. ok is false for "*", a suffix form or an out-of-object range.
func parseContentRange(h string) (start, end, total uint64, ok bool) {
	spec, found := strings.CutPrefix(strings.TrimSpace(h), "bytes ")
	if !found {
		return 0, 0, 0, false
	}
	rng, tot, found := strings.Cut(spec, "/")
	if !found {
		return 0, 0, 0, false
	}
	a, b, found := strings.Cut(strings.TrimSpace(rng), "-")
	if !found {
		return 0, 0, 0, false
	}
	n, err := strconv.ParseUint(strings.TrimSpace(tot), 10, 63)
	if err != nil || n == 0 {
		return 0, 0, 0, false
	}
	s, err := strconv.ParseUint(strings.TrimSpace(a), 10, 63)
	if err != nil {
		return 0, 0, 0, false
	}
	e, err := strconv.ParseUint(strings.TrimSpace(b), 10, 63)
	if err != nil || e < s || e >= n {
		return 0, 0, 0, false
	}
	return s, e, n, true
}
