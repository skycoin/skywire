// Package skysocks pkg/skysocks/upload_stripe.go — striped, resumable uploads.
//
// The download direction survives losing a tunnel because a byte range
// addresses a chunk INDEPENDENTLY: a chunk whose stream dies is re-fetched on a
// live tunnel and the browser's next byte arrives in 0.6-1.2 s. A POST had no
// such addressing — one stream, one tunnel, an unknown prefix consumed at the
// origin — so the same cut that downloads survive lost 3 of 3 uploads
// (bench/2026-09-16/6745065a5-degrade: 30/45/44 MB of 50 delivered, hash_ok
// 0/3, curl seeing only the 100 Continue).
//
// Against a sink that OPTS IN (the `X-Chunked-Upload: bytes` advertisement of
// cmd/skywire-cli/commands/proxy/loadtest.go) the upload gets the download's
// property: the body is cut into offset-addressed chunks, each chunk is its own
// PUT on its own stream spread across the tunnels, a chunk is durable only on
// its 2xx, and a chunk whose tunnel dies is re-sent at once on another one. The
// chunk that completes the object answers with the sink's whole-object hash,
// which is what the browser sees for its original request — so a hash check
// does not care which path carried the bytes.
//
// Opt-in is a single explicit header, probed per origin and cached. It is never
// inferred: sending a partial body to an origin that did not advertise it is
// data corruption. An origin that does not opt in keeps today's behavior, with
// one addition — a body small enough to buffer is replayed whole on a surviving
// tunnel if the one under it dies before the origin committed a response
// (spliceReplayable).
package skysocks

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/0magnet/yamux"
)

// Upload tunables. Defaults ship; they are vars so a test can shrink them.
var (
	// uploadStripeMinBytes is the smallest body worth addressing in chunks. Below
	// it the per-chunk ack round trips cost more than the striping wins.
	uploadStripeMinBytes int64 = 4 << 20
	// uploadChunkBytes is one chunk, and so one buffer. Sized with
	// uploadConcurrency against the router's per-leg send window: 4 MiB x 2 in
	// flight is exactly ecfMaxWindowBytes (pkg/router/route_mux.go), so a third
	// concurrent chunk on one tunnel would only park at the window.
	uploadChunkBytes int64 = 4 << 20
	// uploadMemBytes bounds the chunk buffers alive at once, independent of the
	// body size: the producer reads the browser's body only into a free slot, and
	// a slot is freed only when its chunk is durable (its 2xx). A 2c/4G exit has
	// been OOM-killed for less (#4252) and the client end runs on the same class
	// of hardware.
	uploadMemBytes int64 = 16 << 20
	// uploadConcurrency is how many chunks one tunnel may carry at once.
	uploadConcurrency = 2
	// uploadReplayMaxBytes caps the generic-POST replay buffer: a body this size
	// or smaller is remembered as it flows so the request can be re-sent whole
	// when its tunnel dies before the origin committed a response.
	uploadReplayMaxBytes int64 = 8 << 20
)

const (
	// uploadSendWindowBytes mirrors the router's ecfMaxWindowBytes — the most one
	// leg's send window ever opens to. chunk x per-tunnel-inflight is held at or
	// below it (see uploadStripe.perTunnel).
	uploadSendWindowBytes = 8 << 20
	// uploadProbeTTL is how long an origin's opt-in answer is trusted. Short
	// enough that a sink restarted without the feature stops being striped to
	// within one cache generation.
	uploadProbeTTL = 5 * time.Minute
	// uploadAckTimeout bounds the wait for a chunk's ack once its body is out.
	uploadAckTimeout = 60 * time.Second
	// uploadIdleTimeout is the rolling per-write progress window of a chunk body:
	// a moving tunnel completes however slow it is, a stalled one fails inside one
	// window (the same bytes-are-liveness standard the chunk fetch applies).
	uploadIdleTimeout = 30 * time.Second
	// uploadEarlyTries bounds the 425s one chunk may wait out. The sink answers
	// 425 when the chunk is beyond its reorder window, which our own memory gate
	// already makes rare; the bound only stops a pathological loop.
	uploadEarlyTries = 20
	// uploadEarlyWaitMax caps the Retry-After a 425 may impose.
	uploadEarlyWaitMax = 5 * time.Second
	// uploadBusyBackoff / uploadBusyTries govern a 503: the sink's sessions are
	// all taken, which is not this chunk's fault and not something another tunnel
	// fixes, so it is the one case that waits.
	uploadBusyBackoff = time.Second
	uploadBusyTries   = 3
	// uploadReplayTries is how many times a generic POST may be replayed after
	// the tunnel under it died with no response committed.
	uploadReplayTries = 2
	// uploadDurableWait is how long a chunk that has been ACKED but is not yet in
	// the sink's contiguous prefix waits for the frontier to reach it before
	// assuming the sink dropped it. The sink evicts the furthest chunk it is
	// holding to make room for the frontier, so a 2xx is a receipt, not durability
	// — X-Upload-Received is.
	uploadDurableWait = 5 * time.Second
	// uploadResendPasses bounds how many times one chunk may be sent again
	// because the sink's prefix never reached it.
	uploadResendPasses = 3
	// uploadAckBodyLimit caps the ack body read (it is a small JSON object).
	uploadAckBodyLimit = 64 << 10
)

// uploadHeld is the bytes of upload chunk buffers alive across the process and
// uploadHeldPeak their high-water mark. The mem gate is what bounds them; these
// make the bound observable — the test asserts the peak never passes
// uploadMemBytes however large the body is.
var (
	uploadHeld     atomic.Int64
	uploadHeldPeak atomic.Int64
)

func chargeUpload(n int64) {
	h := uploadHeld.Add(n)
	for {
		p := uploadHeldPeak.Load()
		if h <= p || uploadHeldPeak.CompareAndSwap(p, h) {
			return
		}
	}
}

func releaseUpload(n int64) { uploadHeld.Add(-n) }

// uploadCandidate is a classified POST/PUT on the split path: the request, the
// verbatim head to replay, and which of the two treatments it qualifies for.
type uploadCandidate struct {
	req    *http.Request
	head   []byte
	host   string // origin host, no port
	total  int64  // Content-Length
	stripe bool   // big enough to stripe, if the origin opts in
	replay bool   // small enough to buffer and replay whole
	window int64  // the sink's advertised reorder window (0 = it did not say)
}

// classifyUpload reports what may be done with a request the splitter peeked.
// It returns nil for anything that is not an upload with a KNOWN length: a
// chunked body has no last byte to address and no size to bound a buffer with,
// and a request that already carries a Content-Range is addressing its own
// offsets — re-addressing it would corrupt it.
func classifyUpload(r *http.Request, head []byte, host string) *uploadCandidate {
	if r == nil || (r.Method != http.MethodPost && r.Method != http.MethodPut) {
		return nil
	}
	if r.ProtoMajor != 1 || r.ProtoMinor != 1 {
		return nil
	}
	if r.Header.Get("Upgrade") != "" || r.Header.Get("Content-Range") != "" {
		return nil
	}
	if len(r.TransferEncoding) > 0 || r.ContentLength <= 0 {
		return nil
	}
	u := &uploadCandidate{
		req:    r,
		head:   head,
		host:   host,
		total:  r.ContentLength,
		stripe: r.ContentLength >= uploadStripeMinBytes,
		replay: r.ContentLength <= uploadReplayMaxBytes,
	}
	if !u.stripe && !u.replay {
		return nil
	}
	return u
}

// uploadProbeCache remembers, per origin host:port, whether it advertised
// X-Chunked-Upload — one probe per origin per uploadProbeTTL rather than one per
// upload. It hangs off rangeSplitConfig so it is per-client: two clients in one
// process (the tests) never answer for each other's origin.
type uploadProbeCache struct {
	mu sync.Mutex
	m  map[string]uploadProbeEntry
}

// uploadProbeEntry is one origin's answer: whether it takes chunks, and the
// reorder window it said it would hold for one upload (X-Upload-Window, 0 when
// it did not say).
type uploadProbeEntry struct {
	ok     bool
	window int64
	at     time.Time
}

func (p *uploadProbeCache) get(key string) (uploadProbeEntry, bool) {
	if p == nil {
		return uploadProbeEntry{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.m[key]
	if !ok || time.Since(e.at) > uploadProbeTTL {
		return uploadProbeEntry{}, false
	}
	return e, true
}

func (p *uploadProbeCache) put(key string, e uploadProbeEntry) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = make(map[string]uploadProbeEntry)
	}
	e.at = time.Now()
	p.m[key] = e
}

// originKey names the origin an opt-in answer belongs to.
func (c *Client) originKey(host string) string {
	return net.JoinHostPort(host, strconv.Itoa(c.rangePlainPort()))
}

// startUploadProbe asks the origin whether it takes chunked uploads, answering
// from the cache when it can. The probe rides its OWN exit stream, pipelined
// behind its own CONNECT the way a chunk fetch is, and runs while the caller is
// still reading stream0's CONNECT reply — so it costs no round trip that was not
// already being waited on, and stream0 stays pristine for the fallback splice.
func (c *Client) startUploadProbe(u *uploadCandidate) <-chan uploadProbeEntry {
	res := make(chan uploadProbeEntry, 1)
	key := c.originKey(u.host)
	if e, cached := c.rs.uploadProbes.get(key); cached {
		res <- e
		return res
	}
	go func() {
		e := c.probeChunkedUpload(u)
		c.rs.uploadProbes.put(key, e)
		res <- e
	}()
	return res
}

// uploadProbeResult waits for a probe answer, bounded by the same window a range
// probe gets. A probe that has not answered by then is a no.
func (c *Client) uploadProbeResult(res <-chan uploadProbeEntry) uploadProbeEntry {
	select {
	case e := <-res:
		return e
	case <-time.After(rsProbeTimeout):
		return uploadProbeEntry{}
	}
}

// probeChunkedUpload issues HEAD against the upload's own request-URI and reads
// the one header that may turn striping on, plus the reorder window the origin
// says it will hold — what keeps us from offering more out-of-order bytes than
// it can take.
func (c *Client) probeChunkedUpload(u *uploadCandidate) (e uploadProbeEntry) {
	sess, st, err := c.openChunkStream()
	if err != nil {
		return e
	}
	defer st.Close() //nolint:errcheck,gosec
	g := guardTunnel(sess, st)
	defer g.stop()

	_ = st.SetDeadline(time.Now().Add(rsProbeTimeout)) //nolint:errcheck
	if err := c.exitConnectPipelined(st, u.host, c.rangePlainPort(), buildUploadHead(u.req, http.MethodHead, u.req.URL.RequestURI(), 0, "")); err != nil {
		return e
	}
	resp, err := http.ReadResponse(bufio.NewReader(st), &http.Request{Method: http.MethodHead})
	if err != nil {
		return e
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode/100 != 2 {
		return e
	}
	e.ok = strings.Contains(strings.ToLower(resp.Header.Get("X-Chunked-Upload")), "bytes")
	if w, err := strconv.ParseInt(resp.Header.Get("X-Upload-Window"), 10, 64); err == nil && w > 0 {
		e.window = w
	}
	return e
}

// buildUploadHead reconstructs the browser's request head for one chunk: the
// original headers minus the hop-by-hop and body-framing ones, plus this
// chunk's Content-Range and length. method/uri/contentRange vary per use; a zero
// length and an empty range make the HEAD probe.
func buildUploadHead(req *http.Request, method, uri string, length int64, contentRange string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s HTTP/1.1\r\n", method, uri)
	fmt.Fprintf(&b, "Host: %s\r\n", req.Host)
	for k, vs := range req.Header {
		switch textproto.CanonicalMIMEHeaderKey(k) {
		case "Host", "Content-Length", "Content-Range", "Transfer-Encoding", "Expect",
			"Connection", "Proxy-Connection", "Keep-Alive", "Range", "If-Range":
			continue
		}
		for _, v := range vs {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	if contentRange != "" {
		fmt.Fprintf(&b, "Content-Range: %s\r\n", contentRange)
		fmt.Fprintf(&b, "Content-Length: %d\r\n", length)
	}
	b.WriteString("Connection: close\r\n\r\n")
	return []byte(b.String())
}

// newUploadID names one upload session at the sink. Opaque and unguessable, so
// two clients — or two uploads of the same object — never share one.
func newUploadID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

// uploadStripe drives one striped upload: read the body sequentially into a
// bounded set of chunk buffers, send each as its own PUT on its own tunnel, and
// relay the ack that completes the object as the browser's response.
type uploadStripe struct {
	c    *Client
	u    *uploadCandidate
	id   string
	body io.Reader

	mu       sync.Mutex
	cond     *sync.Cond
	inflight int
	sending  int       // chunks actually going out, so a waiter knows if anything can still advance
	acked    int64     // the sink's durable contiguous prefix, the highest seen
	ackedAt  time.Time // when it last advanced
	err      error
	final    []byte
}

func newUploadStripe(c *Client, u *uploadCandidate, body io.Reader) *uploadStripe {
	s := &uploadStripe{c: c, u: u, id: newUploadID(), body: body, ackedAt: time.Now()}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// serveStripedUpload takes ownership of the browser conn for an upload to an
// origin that opted in, and answers it with the sink's own completion response.
// The exit stream the browser's CONNECT opened is NOT used — every chunk opens
// its own — so the caller closes it before calling this.
func (c *Client) serveStripedUpload(conn net.Conn, u *uploadCandidate) {
	defer conn.Close() //nolint:errcheck,gosec

	// Answer Expect: 100-continue ourselves. The browser is waiting for it before
	// it sends a byte, and no chunk stream must ever inherit that wait: each PUT
	// is written head-then-body with no Expect at all.
	if strings.Contains(strings.ToLower(u.req.Header.Get("Expect")), "100-continue") {
		if _, err := io.WriteString(conn, "HTTP/1.1 100 Continue\r\n\r\n"); err != nil {
			return
		}
	}

	s := newUploadStripe(c, u, conn)
	final, err := s.run()
	if err != nil {
		if c.appCl != nil {
			c.appCl.Log().Warnf("striped upload: %s %d bytes failed at %d acked: %v", u.host, u.total, s.received(), err)
		}
		writeBadGateway(conn)
		return
	}
	if c.appCl != nil {
		c.appCl.Log().Debugf("striped upload: %s %d bytes in %d chunks completed", u.host, u.total, numChunks(u.total, uploadChunkBytes))
	}
	_, _ = conn.Write(final) //nolint:errcheck
}

// run reads the body into bounded buffers and sends the chunks. It returns the
// serialized response the browser gets — the ack that completed the object.
func (s *uploadStripe) run() ([]byte, error) {
	mem := make(chan struct{}, s.slots())

	var wg sync.WaitGroup
	for start := int64(0); start < s.u.total; start += uploadChunkBytes {
		end := start + uploadChunkBytes - 1
		if end >= s.u.total {
			end = s.u.total - 1
		}
		mem <- struct{}{} // a buffer may exist only in a free slot
		if s.failure() != nil {
			<-mem
			break
		}
		buf := make([]byte, end-start+1)
		chargeUpload(int64(len(buf)))
		if _, err := io.ReadFull(s.body, buf); err != nil {
			releaseUpload(int64(len(buf)))
			<-mem
			s.fail(fmt.Errorf("read body at %d: %w", start, err))
			break
		}
		s.takeSlot()
		wg.Add(1)
		go func(start, end int64, buf []byte) {
			defer wg.Done()
			err := s.sendChunk(start, end, buf)
			releaseUpload(int64(len(buf)))
			s.dropSlot()
			<-mem
			if err != nil {
				s.fail(err)
			}
		}(start, end, buf)
	}
	wg.Wait()

	if err := s.failure(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	final := s.final
	s.mu.Unlock()
	if final != nil {
		return final, nil
	}
	// Every chunk is durable but no ack carried the hash — the completing ack's
	// stream died after the sink absorbed it. The sink's status probe answers the
	// same thing.
	return s.fetchFinal()
}

// slots is how many chunk buffers may exist at once — the memory gate, and with
// it the most bytes the upload ever offers the sink out of order. It is the
// smaller of our own ceiling and the reorder window the sink advertised: going
// past what the sink will hold only earns 425s and a re-send of bytes that were
// already on the wire.
func (s *uploadStripe) slots() int {
	n := uploadMemBytes / uploadChunkBytes
	if w := s.u.window; w > 0 && w/uploadChunkBytes < n {
		n = w / uploadChunkBytes
	}
	if n < 1 {
		n = 1
	}
	return int(n)
}

// perTunnel is how many chunks one tunnel may carry at once, held at or below
// the router's per-leg send window: beyond it a writer only parks.
func (s *uploadStripe) perTunnel() int {
	n := uploadConcurrency
	if max := int(uploadSendWindowBytes / uploadChunkBytes); max >= 1 && n > max {
		n = max
	}
	if n < 1 {
		n = 1
	}
	return n
}

// takeSlot blocks until the chunks in flight are fewer than the tunnels can
// carry. The count is re-read every wait, so losing a tunnel narrows the upload
// to what survives instead of piling its share onto the survivor.
func (s *uploadStripe) takeSlot() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		live := s.c.activeLiveCount()
		if live < 1 {
			live = 1
		}
		if s.inflight < live*s.perTunnel() {
			s.inflight++
			return
		}
		s.cond.Wait()
	}
}

func (s *uploadStripe) dropSlot() {
	s.mu.Lock()
	s.inflight--
	s.cond.Broadcast()
	s.mu.Unlock()
}

func (s *uploadStripe) fail(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.cond.Broadcast()
	s.mu.Unlock()
}

func (s *uploadStripe) failure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *uploadStripe) received() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.acked
}

// note records an ack: the durable prefix the sink reports, and the completion
// response if this is the one that finished the object.
func (s *uploadStripe) note(ack chunkAck) {
	s.mu.Lock()
	if ack.received > s.acked {
		s.acked, s.ackedAt = ack.received, time.Now()
	}
	if ack.final != nil && s.final == nil {
		s.final, s.ackedAt = ack.final, time.Now()
	}
	s.cond.Broadcast()
	s.mu.Unlock()
}

// awaitDurable waits for the sink's contiguous prefix to cover a chunk that has
// already been acked. It returns false when the chunk has to go again.
func (s *uploadStripe) awaitDurable(start, end int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		if s.err != nil || s.final != nil || s.acked > end {
			return true
		}
		// The prefix has reached our offset and our bytes are not in it. A chunk
		// the sink still held would have been drained in under the same lock that
		// moved the prefix, so ours is gone — evicted to make room for a frontier
		// chunk — and only we can put it back. Waiting instead is the deadlock the
		// cut test found: every later chunk waits for a frontier nobody re-sends.
		if s.acked >= start {
			return false
		}
		// Backstop: nothing is going out any more and the prefix has stopped, so
		// no chunk below us is coming to move it.
		if s.sending == 0 && time.Since(s.ackedAt) > uploadDurableWait {
			return false
		}
		s.mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		s.mu.Lock()
	}
}

// chunkAck is what one PUT came back with.
type chunkAck struct {
	status     int
	received   int64         // X-Upload-Received: the sink's durable contiguous prefix
	nextOffset int64         // X-Next-Offset on a 425: where the sink's frontier is
	retryAfter time.Duration // Retry-After
	final      []byte        // the serialized response, when it carries the object's hash
}

// sendChunk delivers one chunk and does not return until the SINK says it is
// durable — its offset inside the contiguous prefix of X-Upload-Received.
//
// A 2xx alone is only a receipt. A chunk that arrived ahead of the frontier sits
// in the sink's bounded reorder window, and the sink evicts the furthest of
// those to admit a frontier chunk when it must (loadtest.go evictFurthest); the
// object would then never complete and nobody would know. So the buffer is kept
// until the prefix passes it, and a prefix that has stopped moving means the
// chunk is gone and goes again.
func (s *uploadStripe) sendChunk(start, end int64, buf []byte) error {
	for pass := 1; ; pass++ {
		s.mu.Lock()
		s.sending++
		s.mu.Unlock()
		err := s.deliverChunk(start, end, buf)
		s.mu.Lock()
		s.sending--
		s.mu.Unlock()
		if err != nil {
			return err
		}
		if s.awaitDurable(start, end) {
			return nil
		}
		if pass >= uploadResendPasses {
			return fmt.Errorf("chunk %d-%d: acked %d time(s) but the sink's prefix never reached it (%d/%d)",
				start, end, pass, s.received(), s.u.total)
		}
		if s.c != nil && s.c.appCl != nil {
			s.c.appCl.Log().Debugf("striped upload: chunk %d-%d was acked but dropped from the sink's window — sending it again", start, end)
		}
	}
}

// deliverChunk puts one chunk, retrying it wherever the mesh can carry it.
//
// A chunk is durable only on its 2xx, so every failure mode short of the sink
// refusing the object outright is a re-send: the tunnel dying under the write is
// a FREE retry on another tunnel (no backoff, no budget charged — the bytes are
// still in our hand and any live tunnel can carry them, exactly as a range chunk
// is refetched), a 425 waits for the sink's reorder window and comes back, and a
// 503 backs off since no other tunnel can make the sink's sessions free up.
func (s *uploadStripe) deliverChunk(start, end int64, buf []byte) error {
	var (
		free     int
		busy     int
		early    int
		deadline = time.Now().Add(rsChunkRetryBudget)
		backoff  = rsChunkRetryBackoff
	)
	for attempt := 1; ; attempt++ {
		if err := s.failure(); err != nil {
			return nil // another chunk already failed the upload; do not pile on
		}
		ack, err := s.putChunk(start, end, buf)
		switch {
		case err == nil && ack.status/100 == 2:
			s.note(ack)
			return nil
		case err == nil && ack.status == http.StatusTooEarly:
			if early++; early > uploadEarlyTries {
				return fmt.Errorf("chunk %d-%d: sink held it off %d times (frontier %d)", start, end, early-1, ack.nextOffset)
			}
			s.note(ack)
			time.Sleep(uploadRetryWait(ack.retryAfter))
			attempt--

			continue
		case err == nil && ack.status == http.StatusServiceUnavailable:
			if busy++; busy >= uploadBusyTries {
				return fmt.Errorf("chunk %d-%d: sink out of upload sessions after %d tries", start, end, busy)
			}
			time.Sleep(uploadBusyBackoff)
			attempt--

			continue
		case err == nil:
			return fmt.Errorf("chunk %d-%d: sink answered %d", start, end, ack.status)
		}
		// The tunnel died under the chunk: re-send at once on another one. Nothing
		// to wait for (the next pick skips the dead tunnel) and nothing to charge.
		if errors.Is(err, errSessionClosed) && free < rsFreeRetries {
			free++
			attempt--

			continue
		}
		if attempt >= rsChunkRetries && time.Now().After(deadline) {
			return fmt.Errorf("chunk %d-%d: %w", start, end, err)
		}
		time.Sleep(backoff)
		if backoff < rsChunkRetryBackoffMax {
			if backoff *= 2; backoff > rsChunkRetryBackoffMax {
				backoff = rsChunkRetryBackoffMax
			}
		}
	}
}

// uploadRetryWait bounds a sink-supplied Retry-After.
func uploadRetryWait(d time.Duration) time.Duration {
	if d <= 0 {
		return uploadBusyBackoff
	}
	if d > uploadEarlyWaitMax {
		return uploadEarlyWaitMax
	}
	return d
}

// putChunk makes ONE attempt: a fresh stream on a tunnel the picker chooses, the
// SOCKS5 handshake and the PUT head in one write (one round trip, as a range
// chunk's GET is), the body under a rolling write deadline, then the ack.
func (s *uploadStripe) putChunk(start, end int64, buf []byte) (ack chunkAck, err error) {
	sess, st, err := s.c.openChunkStream()
	if err != nil {
		return ack, err
	}
	defer st.Close() //nolint:errcheck,gosec
	// A tunnel that dies under this PUT fails it AT ONCE — the deadlines are for a
	// slow tunnel, not a gone one — labeled errSessionClosed so the chunk goes to
	// a live tunnel without backing off. guardTunnel unblocks a parked WRITE as
	// readily as a parked read, which is what an upload spends its time in.
	g := guardTunnel(sess, st)
	defer func() { err = g.err(err) }()

	_ = st.SetDeadline(time.Now().Add(rsProbeTimeout)) //nolint:errcheck
	head := buildUploadHead(s.u.req, http.MethodPut, s.chunkURI(), int64(len(buf)),
		fmt.Sprintf("bytes %d-%d/%d", start, end, s.u.total))
	if err := s.c.exitConnectPipelined(st, s.u.host, s.c.rangePlainPort(), head); err != nil {
		return ack, err
	}

	// The body goes out on its own goroutine and the answer is read WHILE it
	// does. A sink that refuses a chunk — 425 when it is beyond the reorder
	// window — answers without draining the body, deliberately: refusing to read
	// is its backpressure. Writing the whole chunk first and only then looking
	// for the answer turns that refusal into a broken pipe, which reads as a
	// transport fault and burns the chunk's retry budget instead of waiting the
	// Retry-After out (measured: a 502 to the browser, and 60 s for a 12 MiB
	// object against a one-chunk window).
	_ = st.SetReadDeadline(time.Now().Add(uploadAckTimeout)) //nolint:errcheck
	werr := make(chan error, 1)
	go func() {
		e := writeChunkBody(st, buf, uploadIdleTimeout)
		if e == nil {
			// The chunk is out; the ack's own window starts now.
			_ = st.SetReadDeadline(time.Now().Add(uploadAckTimeout)) //nolint:errcheck
		}
		werr <- e
	}()

	resp, rerr := http.ReadResponse(bufio.NewReader(st), &http.Request{Method: http.MethodPut})
	if rerr != nil {
		// A write that failed first is the better explanation of the read that
		// failed after it — and it is the one that carries the tunnel's death.
		select {
		case e := <-werr:
			if e != nil {
				return ack, e
			}
		default:
		}
		return ack, rerr
	}
	defer resp.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(io.LimitReader(resp.Body, uploadAckBodyLimit))
	if err != nil {
		return ack, err
	}
	ack = readChunkAck(resp, body)
	if ack.status/100 == 2 {
		// A 2xx means the sink read the whole chunk, so the writer is done; an
		// error from it now would mean the ack is not to be trusted.
		if e := <-werr; e != nil {
			return chunkAck{}, e
		}
	}
	return ack, nil
}

// chunkURI is the browser's own request-URI with the session id and object size
// added, so an origin serving several endpoints still gets the right one.
func (s *uploadStripe) chunkURI() string {
	u := *s.u.req.URL
	q := u.Query()
	q.Set("id", s.id)
	q.Set("bytes", strconv.FormatInt(s.u.total, 10))
	u.RawQuery = q.Encode()
	return u.RequestURI()
}

// readChunkAck reads the sink's per-chunk bookkeeping off a response.
func readChunkAck(resp *http.Response, body []byte) chunkAck {
	ack := chunkAck{status: resp.StatusCode}
	if v, err := strconv.ParseInt(resp.Header.Get("X-Upload-Received"), 10, 64); err == nil {
		ack.received = v
	}
	if v, err := strconv.ParseInt(resp.Header.Get("X-Next-Offset"), 10, 64); err == nil {
		ack.nextOffset = v
	}
	if v, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && v > 0 {
		ack.retryAfter = time.Duration(v) * time.Second
	}
	// The ack that carries the whole object's hash is the one the browser sees.
	if resp.StatusCode/100 == 2 && resp.Header.Get("X-Sha256") != "" {
		ack.final = serializeResponse(resp, body)
	}
	return ack
}

// fetchFinal asks the sink for the object's completion record, for the case
// where the completing chunk's ack was lost with its tunnel.
func (s *uploadStripe) fetchFinal() ([]byte, error) {
	sess, st, err := s.c.openChunkStream()
	if err != nil {
		return nil, err
	}
	defer st.Close() //nolint:errcheck,gosec
	g := guardTunnel(sess, st)
	defer g.stop()

	_ = st.SetDeadline(time.Now().Add(rsProbeTimeout)) //nolint:errcheck
	u := *s.u.req.URL
	q := u.Query()
	q.Set("id", s.id)
	u.RawQuery = q.Encode()
	if err := s.c.exitConnectPipelined(st, s.u.host, s.c.rangePlainPort(), buildUploadHead(s.u.req, http.MethodGet, u.RequestURI(), 0, "")); err != nil {
		return nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(st), &http.Request{Method: http.MethodGet})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(io.LimitReader(resp.Body, uploadAckBodyLimit))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 || resp.Header.Get("X-Sha256") == "" {
		return nil, fmt.Errorf("upload status: %d with no completion record", resp.StatusCode)
	}
	return serializeResponse(resp, body), nil
}

// serializeResponse renders an origin response back onto the browser's
// connection: its status and headers verbatim, minus the framing ones, with the
// body's own length.
func serializeResponse(resp *http.Response, body []byte) []byte {
	var b strings.Builder
	status := resp.Status
	if status == "" {
		status = strconv.Itoa(resp.StatusCode)
	}
	fmt.Fprintf(&b, "HTTP/1.1 %s\r\n", status)
	for k, vs := range resp.Header {
		switch textproto.CanonicalMIMEHeaderKey(k) {
		case "Content-Length", "Connection", "Keep-Alive", "Transfer-Encoding":
			continue
		}
		for _, v := range vs {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	fmt.Fprintf(&b, "Content-Length: %d\r\n", len(body))
	b.WriteString("Connection: close\r\n\r\n")
	return append([]byte(b.String()), body...)
}

// writeChunkBody writes a chunk in slices under a ROLLING write deadline: a slow
// but moving tunnel finishes however long it takes, a stalled one fails inside
// one window. A single deadline over a whole 4 MiB chunk would fail every chunk
// on a tunnel below chunk/idle bytes per second.
func writeChunkBody(st net.Conn, buf []byte, idle time.Duration) error {
	const slice = 256 << 10
	for off := 0; off < len(buf); {
		end := off + slice
		if end > len(buf) {
			end = len(buf)
		}
		_ = st.SetWriteDeadline(time.Now().Add(idle)) //nolint:errcheck
		n, err := st.Write(buf[off:end])
		off += n
		if err != nil {
			return err
		}
	}
	_ = st.SetWriteDeadline(time.Time{}) //nolint:errcheck
	return nil
}

// --- the generic POST: replay, not resume ---------------------------------

// spliceReplayable is splicePrefixed for an upload to an origin that did NOT opt
// in, small enough to remember: the body is buffered as it flows, and if the
// TUNNEL dies before the origin has committed a response the whole request is
// re-sent on a surviving tunnel. Nothing else changes — the origin sees one
// ordinary request, and a response byte (past a 1xx, which commits nothing)
// makes the attempt final.
//
// A replayed POST CAN double-apply, if the origin consumed the body and acted on
// it before its tunnel died without answering. That is the price of surviving a
// generic POST at all; it is bounded to uploadReplayMaxBytes, to the case where
// the origin has said nothing, and to uploadReplayTries.
func (c *Client) spliceReplayable(conn, stream net.Conn, u *uploadCandidate) {
	defer conn.Close() //nolint:errcheck,gosec

	body := new(bytes.Buffer)
	sess := sessionOf(stream)
	interim := false
	for attempt := 0; ; attempt++ {
		committed, sawInterim, err := c.relayUploadOnce(conn, sess, stream, u, body, attempt > 0, interim)
		interim = interim || sawInterim
		stream.Close() //nolint:errcheck,gosec
		if err == nil || committed || attempt >= uploadReplayTries {
			return
		}
		// Only a dead TUNNEL earns a replay, and only with the whole body in hand.
		if !errors.Is(err, errSessionClosed) || int64(body.Len()) < u.total {
			return
		}
		var st net.Conn
		if sess, st, err = c.openChunkStream(); err != nil {
			return
		}
		if err := c.exitConnectPipelined(st, u.host, c.rangePlainPort(), u.head); err != nil {
			st.Close() //nolint:errcheck,gosec
			return
		}
		if c.appCl != nil {
			c.appCl.Log().Warnf("upload replay: the tunnel died with no response from %s — re-sending %d bytes (attempt %d)", u.host, u.total, attempt+2)
		}
		stream = st
	}
}

// relayUploadOnce runs one attempt of the replayable splice. It returns whether
// the origin committed a final response (after which no replay may happen) and
// whether it sent a 1xx interim.
func (c *Client) relayUploadOnce(conn net.Conn, sess *yamux.Session, stream net.Conn, u *uploadCandidate, body *bytes.Buffer, replay, interimSent bool) (committed, sawInterim bool, err error) {
	if sess != nil {
		g := guardTunnel(sess, stream)
		defer func() { err = g.err(err) }()
	}
	if !replay {
		if _, werr := stream.Write(u.head); werr != nil {
			return false, false, werr
		}
	}

	// The body goes up on its own goroutine: what we already hold first (a
	// replay), then the rest of the browser's. It keeps FILLING the buffer even
	// after the exit stream dies — that buffer is the only thing that makes a
	// replay possible, and abandoning it at the first write error would mean the
	// one case this exists for never had the body.
	up := make(chan struct{})
	go func() {
		defer close(up)
		dead := false
		if replay && body.Len() > 0 {
			if _, werr := stream.Write(body.Bytes()); werr != nil {
				dead = true
			}
		}
		buf := make([]byte, 64<<10)
		for int64(body.Len()) < u.total {
			room := u.total - int64(body.Len())
			if room > int64(len(buf)) {
				room = int64(len(buf))
			}
			_ = conn.SetReadDeadline(time.Now().Add(uploadIdleTimeout)) //nolint:errcheck
			n, rerr := conn.Read(buf[:room])
			if n > 0 {
				body.Write(buf[:n]) //nolint:errcheck // bytes.Buffer never errors
				if !dead {
					if _, werr := stream.Write(buf[:n]); werr != nil {
						dead = true
					}
				}
			}
			if rerr != nil {
				return
			}
		}
	}()
	// The upstream copy must be finished before the caller may look at the buffer
	// to decide on a replay, and it cannot outlive this attempt.
	defer func() {
		if committed {
			return // the caller closes both ends; the copier ends with them
		}
		stream.Close() //nolint:errcheck,gosec
		<-up
		_ = conn.SetReadDeadline(time.Time{}) //nolint:errcheck
	}()

	br := bufio.NewReader(stream)
	for {
		line, rerr := br.ReadString('\n')
		if rerr != nil {
			return false, sawInterim, rerr
		}
		code := statusCode(line)
		if code >= 100 && code < 200 {
			// Interim: it commits nothing, so a tunnel death after it is still
			// replayable. Forward the first one only — the browser asked once.
			sawInterim = true
			forward := !interimSent
			if forward {
				_, _ = io.WriteString(conn, line) //nolint:errcheck
			}
			for {
				l, e := br.ReadString('\n')
				if e != nil {
					return false, sawInterim, e
				}
				if forward {
					_, _ = io.WriteString(conn, l) //nolint:errcheck
				}
				if strings.TrimRight(l, "\r\n") == "" {
					break
				}
			}
			interimSent = true

			continue
		}
		// A final status line: the origin has committed, so this attempt is the
		// answer whatever happens next.
		if _, werr := io.WriteString(conn, line); werr != nil {
			return true, sawInterim, werr
		}
		_, cerr := io.Copy(conn, br)
		return true, sawInterim, cerr
	}
}

// sessionOf digs the yamux session out of an exit stream, through whatever
// metering wrappers the splice put around it. nil when the conn is not one of
// ours (an in-memory pipe in a test), which simply means no tunnel-death
// labeling and so no replay.
func sessionOf(stream net.Conn) *yamux.Session {
	for i := 0; i < 8 && stream != nil; i++ {
		if sr, ok := stream.(interface{ Session() *yamux.Session }); ok {
			return sr.Session()
		}
		un, ok := stream.(interface{ Unwrap() net.Conn })
		if !ok {
			return nil
		}
		stream = un.Unwrap()
	}
	return nil
}
