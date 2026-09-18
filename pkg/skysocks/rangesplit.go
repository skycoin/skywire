// Package skysocks pkg/skysocks/rangesplit.go — transparent HTTP range-splitting.
//
// A browser or plain HTTP client pointed at the skysocks SOCKS5 proxy issues an
// ordinary GET. When the origin is reachable over plaintext HTTP (port 80) and
// advertises byte ranges (RFC 7233), the client fetches the body as several
// concurrent byte ranges over SEPARATE exit streams and reassembles it in order
// into a single 200 response — so one unmodified download spreads across the
// mesh's tunnels with no client cooperation. Anything that cannot be split (not
// a GET, an already-ranged request, a non-range origin, a small file) falls back
// to a byte-identical transparent splice.
//
// HTTPS (port 443) is handled in rangesplit_https.go, but only when the operator
// has explicitly enabled it: seeing the GET inside TLS requires terminating it at
// the proxy with a leaf minted by an UNCONSTRAINED local root (the resolver CA is
// name-constrained to .skynet/.dmsg/.skysocks and cannot cover a real origin). That
// root can forge any host, so HTTPS range-splitting is off by default and the
// browser must be told to trust the root; a plain :443 CONNECT splices through
// unchanged until then.
package skysocks

import (
	"bufio"
	"bytes"
	"crypto/x509"
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

	"github.com/skycoin/skywire/pkg/skynetca"
)

// rangeSplitConfig tunes transparent HTTP range-splitting. Zero value is
// disabled; NewClient fills defaults and enables it.
type rangeSplitConfig struct {
	enabled     bool
	concurrency int   // number of concurrent range streams
	chunkSize   int64 // bytes per range request
	// plainPort is the destination port treated as plaintext HTTP for splitting
	// (0 = the default 80). Configurable so a bench origin on another port can
	// be split; production stays on 80.
	plainPort int

	// HTTPS (:443) range-splitting is off unless the operator opts in — it needs a
	// forge-any-host root the browser is told to trust (see rangesplit_https.go).
	httpsEnabled bool
	minter       skynetca.LeafMinter // mints per-host leaves for browser-side TLS termination
	caCert       *x509.Certificate   // the MITM root, for operator export/import
	originRoots  *x509.CertPool      // origin-side verification roots (nil = system); tests inject here

	// uploadProbes remembers which origins advertised X-Chunked-Upload, so an
	// upload to one that did is striped without re-probing (upload_stripe.go).
	// Per-client: a nil cache only means every upload probes.
	uploadProbes *uploadProbeCache
}

const (
	defaultRSConcurrency = 8
	defaultRSChunkSize   = 4 << 20 // 4 MiB
	rsChunkRetries       = 3       // minimum per-chunk attempts before the time budget governs
	// rsChunkRetryBudget keeps retrying a failed chunk this long before giving up.
	// A --tunnels rotation briefly drops every tunnel (pickSession → nil →
	// errAllTunnelsDown); self-heal re-dials within a few seconds, so waiting out
	// that window recovers the chunk instead of truncating the whole download (the
	// old 3 instant retries failed in microseconds and killed it).
	rsChunkRetryBudget = 15 * time.Second
	// rsChunkIdleTimeout is the per-read rolling deadline for a chunk BODY: a
	// read that makes no progress for this long fails the attempt. Idle-based,
	// not total — a slow chunk may take minutes and still complete.
	rsChunkIdleTimeout     = 15 * time.Second
	rsChunkRetryBackoff    = 100 * time.Millisecond
	rsChunkRetryBackoffMax = 2 * time.Second
	rsHeadLimit            = 64 << 10
	rsProbeTimeout         = 20 * time.Second
	rsClassifyTimeout      = 5 * time.Second  // wait for the client's first request bytes
	rsHeadReadTimeout      = 10 * time.Second // finish reading the header block
	// rsFreeRetries bounds how many attempts a tunnel's death may buy a chunk
	// for free (see retryWithBudget). Each free retry needs a tunnel that was
	// live at the pick and closed under the attempt, so the count is naturally
	// bounded by the tunnel set; the cap only stops a pathological churn loop
	// from spinning.
	rsFreeRetries = 8
)

// errSessionClosed marks an attempt that ended because the TUNNEL it ran on
// died, not because the fetch is failing. It says nothing about the chunk — the
// bytes are still at the origin and any other live tunnel can carry them — so
// retryWithBudget refetches AT ONCE, without backing off and without charging
// the death to the chunk's retry budget. Waiting instead (the old behavior)
// burned the budget on a tunnel that was already gone and pushed the download
// into the sequential rescue, which finishes the file on ONE stream at
// single-tunnel speed.
var errSessionClosed = errors.New("skysocks: tunnel closed under the fetch")

// errTunnelSnubbed marks an attempt this client ABORTED because its tunnel was
// snubbed — it held outstanding work and produced no byte and no ack for its
// bound (tunnel_snub.go). Like errSessionClosed it says nothing about the
// chunk, so it refetches at once on another tunnel free of backoff and free of
// budget; unlike it, the tunnel is still alive and is not retired.
var errTunnelSnubbed = errors.New("skysocks: tunnel snubbed under the fetch")

// freeRetry reports whether a failed attempt is one the CLIENT caused by moving
// the chunk off its tunnel — a death or a snub — rather than evidence about the
// chunk. Those refetch immediately on another tunnel and are not charged.
func freeRetry(err error) bool {
	return errors.Is(err, errSessionClosed) || errors.Is(err, errTunnelSnubbed)
}

func defaultRangeSplitConfig() rangeSplitConfig {
	return rangeSplitConfig{
		enabled:      true,
		concurrency:  defaultRSConcurrency,
		chunkSize:    defaultRSChunkSize,
		uploadProbes: &uploadProbeCache{},
	}
}

// rangePlainPort is the port the splitter treats as plaintext HTTP (80 unless
// SetRangeSplitPort chose another).
func (c *Client) rangePlainPort() int {
	if c.rs.plainPort > 0 {
		return c.rs.plainPort
	}
	return 80
}

// isPlainPort reports whether target ("host:port") is on the plaintext HTTP port
// the splitter handles.
func (c *Client) isPlainPort(target string) bool {
	_, port, err := net.SplitHostPort(target)
	if err != nil {
		return false
	}
	return port == strconv.Itoa(c.rangePlainPort())
}

// serveHTTPRangeSplit takes ownership of a freshly-CONNECTed browser conn and the
// exit stream (stream0) already SOCKS5-CONNECTed to a :80 origin. It answers the
// browser's CONNECT, peeks its first HTTP request, and — when it is a
// splittable GET against a range-capable origin — fetches the body as N concurrent
// byte ranges over separate exit streams, reassembling it in order into a single
// synthesized 200 response. Anything not splittable falls back to a byte-identical
// transparent splice. It always closes conn and stream before returning.
func (c *Client) serveHTTPRangeSplit(conn, stream net.Conn) {
	host, doSplice, ok := c.rangeSplitInner(conn, stream)
	if ok {
		return
	}
	// Fall back to a transparent splice, replaying any bytes already read from the
	// browser so the exit sees an identical stream.
	c.splicePrefixed(conn, stream, doSplice)
	_ = host
}

// rangeSplitInner drives the split. It returns ok=true when it fully served the
// response (split or verbatim relay) and closed both ends. It returns ok=false to
// ask the caller to splice, handing back any browser bytes it had buffered so they
// can be replayed to the exit.
func (c *Client) rangeSplitInner(conn, stream net.Conn) (host string, clientPrefix []byte, ok bool) {
	// 1. Answer the browser's CONNECT at once, from here. A SOCKS5 CONNECT that
	//    succeeds has exactly one reply — VER 5, REP 0, and a BND address a
	//    CONNECT client ignores — so blocking on the exit's copy of it buys
	//    nothing and costs a full exit round trip before the browser will send
	//    the GET that steps 2 and 3 need. The real reply is read at step 4, in
	//    the same round trip as the 206; a refused CONNECT is reported there as
	//    an HTTP 502 instead of relayed, since the browser already has a reply.
	if _, err := conn.Write(socks5OKReply); err != nil {
		conn.Close()   //nolint:errcheck,gosec
		stream.Close() //nolint:errcheck,gosec
		return "", nil, true
	}

	// 2. Classify + read the browser's request head. Non-HTTP traffic on :80 (raw
	//    tunnels, server-speaks-first protocols) is detected the instant the bytes
	//    stop matching an HTTP method prefix and spliced with no meaningful delay.
	reqHead, isHTTP := peekRequestHead(conn, rsHeadLimit)
	clearDeadlines(conn)
	var (
		req *http.Request
		up  *uploadCandidate
	)
	if isHTTP {
		if r, perr := http.ReadRequest(bufio.NewReader(bytes.NewReader(reqHead))); perr == nil {
			h := r.Host
			if hh, _, e := net.SplitHostPort(h); e == nil {
				h = hh
			}
			if splittableRequest(r) {
				req, host = r, h
			} else if up = classifyUpload(r, reqHead, h); up != nil {
				// A POST/PUT with a known length: striped across the tunnels when the
				// origin opts in, replayed on a live tunnel when it is small enough to
				// remember, today's splice otherwise (upload_stripe.go).
				host = h
			}
		}
	}

	// 2b. The upload opt-in probe, started NOW so it overlaps the CONNECT reply
	//     read below rather than adding a round trip of its own. It rides its own
	//     exit stream — stream0 stays pristine, so an origin that does not opt in
	//     is spliced byte-for-byte as before.
	var probe <-chan uploadProbeEntry
	if up != nil && up.stripe {
		probe = c.startUploadProbe(up)
	}

	// 3. chunk0 doubles as the range probe: original request + Range: bytes=0-(N-1).
	//    Only a Range request header is added; a non-range origin ignores it and
	//    returns its normal 200, so that fallback stays byte-identical. It is
	//    written BEFORE the CONNECT reply is read, so it queues behind that reply
	//    on the exit's stream exactly as a chunk fetch queues its GET behind the
	//    handshake (exitConnectPipelined). A write error is not fatal here: the
	//    reply read below says whether the CONNECT itself failed, which is the
	//    more useful thing to tell the browser.
	var injectErr error
	if req != nil {
		if _, err := stream.Write(injectRange(reqHead, c.probeChunkBytes()-1)); err != nil {
			injectErr = err
		}
	}

	// 4. The exit's real CONNECT reply, now overlapped with the browser's request
	//    and the injected GET rather than serialized ahead of them. Its bytes are
	//    dropped rather than forwarded — the browser has a reply already — and a
	//    refusal becomes a 502 for an HTTP client, a bare close for anything else.
	_ = stream.SetReadDeadline(time.Now().Add(rsProbeTimeout)) //nolint:errcheck
	reply, err := readSocks5Reply(stream)
	if err != nil || len(reply) < 2 || reply[1] != 0x00 || injectErr != nil {
		if isHTTP {
			writeBadGateway(conn, connectFailure(host, reply, err, injectErr))
		}
		conn.Close()   //nolint:errcheck,gosec
		stream.Close() //nolint:errcheck,gosec
		return host, nil, true
	}
	clearDeadlines(conn, stream)
	if req == nil {
		if up != nil {
			if probe != nil {
				if e := c.uploadProbeResult(probe); e.ok {
					// The origin takes offset-addressed chunks: the body goes over its own
					// per-chunk streams, so stream0 has nothing left to carry.
					up.window = e.window
					stream.Close() //nolint:errcheck,gosec
					c.serveStripedUpload(conn, up)
					return host, nil, true
				}
			}
			if up.replay {
				c.spliceReplayable(conn, stream, up)
				return host, nil, true
			}
		}
		return host, reqHead, false // let caller splice, replaying what we read
	}

	br := bufio.NewReader(stream)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		conn.Close()   //nolint:errcheck,gosec
		stream.Close() //nolint:errcheck,gosec
		return host, nil, true
	}
	if statusCode(statusLine) != 206 {
		// Origin ignored the Range (200) or returned a redirect/error. Relay it
		// verbatim: the added Range header does not appear in such responses, so the
		// browser sees exactly what its unmodified GET would have produced.
		_, _ = conn.Write([]byte(statusLine)) //nolint:errcheck
		if n := br.Buffered(); n > 0 {
			b, _ := br.Peek(n)   //nolint:errcheck // peeking exactly Buffered() bytes never errors
			_, _ = conn.Write(b) //nolint:errcheck
		}
		_, _ = io.Copy(conn, stream) //nolint:errcheck
		conn.Close()                 //nolint:errcheck,gosec
		stream.Close()               //nolint:errcheck,gosec
		return host, nil, true
	}

	// 4. Parse 206 headers → total size + validator.
	tp := textproto.NewReader(br)
	hdr, err := tp.ReadMIMEHeader()
	if err != nil {
		conn.Close()   //nolint:errcheck,gosec
		stream.Close() //nolint:errcheck,gosec
		return host, nil, true
	}
	total, okTotal := parseContentRangeTotal(hdr.Get("Content-Range"))
	if !okTotal || total <= 0 {
		conn.Close()   //nolint:errcheck,gosec
		stream.Close() //nolint:errcheck,gosec
		return host, nil, true
	}
	validator := ifRangeValidator(hdr)

	// 5. Synthesized 200 header to the browser.
	if err := writeSynth200(conn, hdr, total); err != nil {
		conn.Close()   //nolint:errcheck,gosec
		stream.Close() //nolint:errcheck,gosec
		return host, nil, true
	}

	// 6. Start the remaining chunks NOW, over fresh exit streams — they run
	//    concurrently with chunk0's delivery below. Waiting for chunk0's whole
	//    body to reach the browser first meant the parallel streams only began
	//    paying their setup cost after a full chunk had been delivered.
	//
	//    The chunk size for the remainder follows the OBJECT, not a fixed step:
	//    ~two chunks per active tunnel, floored at 1 MiB and capped by the
	//    configured chunk size (see chunk_plan.go). chunk0 keeps the probe size —
	//    it was requested before the total was known — so it is the only chunk
	//    the plan cannot size.
	chunk0Len := c.probeChunkBytes()
	if total < chunk0Len {
		chunk0Len = total
	}
	// The object's spread ledger. It is kept whatever the knobs say — the
	// `shares=` line below is the client's own account of what each tunnel
	// carried — and it STEERS the chunks only once a spread knob is set.
	// min_routes is applied here, before the first chunk goes out, since
	// promoting a standby after the plan is made cannot change the plan.
	pl := c.newSpreadPlanner(spreadDown)
	pl.ensureRoutes()
	pl.charge(sessionOf(stream), chunk0Len)

	var pending *chunkFetches
	if total > chunk0Len {
		chunkSize := c.planChunkSize(total, chunk0Len)
		chunks := 1 + numChunks(total-chunk0Len, chunkSize)
		if c.appCl != nil {
			c.appCl.Log().Debugf("range-split: %s %d bytes → %d chunks of %d bytes × %d streams",
				host, total, chunks, chunkSize, c.chunkAdmission(pl))
		}
		// Observability counters (surfaced as proxystatus.RangeSplit): this is a
		// committed multi-chunk split, so record it and mark it in flight for the
		// duration of the concurrent fetch.
		c.rsSplits.Add(1)
		c.rsChunks.Add(uint64(chunks)) //nolint:gosec // chunks>=2 here (total>chunk0Len)
		c.rsBytes.Add(uint64(total))   //nolint:gosec // total>0 checked above
		c.rsActive.Add(1)
		pending = c.startChunkFetchesPlanned(chunk0Len, total, chunkSize, pl, func(start, end int64, prog rsProgress, p chunkPlacement) ([]byte, error) {
			return c.fetchChunkRetry(req, host, validator, start, end, prog, p)
		})
	}

	// 7. chunk0 body (bytes 0..chunk0Len-1) straight from stream0.
	if _, err := io.CopyN(conn, br, chunk0Len); err != nil {
		if pending != nil {
			pending.abort()
			c.rsActive.Add(-1)
		}
		conn.Close()   //nolint:errcheck,gosec
		stream.Close() //nolint:errcheck,gosec
		return host, nil, true
	}
	stream.Close() //nolint:errcheck,gosec // stream0 done; remaining chunks use fresh streams
	if pending == nil {
		conn.Close() //nolint:errcheck,gosec
		return host, nil, true
	}

	// 8. Drain the already-running chunk fetches to the browser in order. A chunk
	//    that exhausts its retries degrades to the sequential rescue rather than
	//    truncating the download.
	pending.writeInOrder(conn, func(w net.Conn, start int64) (int64, error) {
		return c.streamTailOnce(w, req, host, validator, start, total, pl)
	})
	c.rsActive.Add(-1)
	c.logSpread("range-split", host, total, pl)
	conn.Close() //nolint:errcheck,gosec
	return host, nil, true
}

// logSpread is the object's completion line: what each tunnel carried, as a
// percentage, keyed by the tunnel's local route-group port — the same name the
// bench's carrier.tsv uses, so a client-side share can be matched against the
// per-transport sent/recv deltas measured at both ends (bench/direction.sh).
func (c *Client) logSpread(what, host string, total int64, pl *spreadPlanner) {
	c.recordSpread(pl)
	if c.appCl == nil || pl == nil {
		return
	}
	c.appCl.Log().Debugf("%s: %s %d bytes complete over %d tunnel(s) %s top=%.0f%%",
		what, host, total, len(pl.shares()), pl.sharesLine(), 100*pl.topShare())
}

// rsChunk is one outstanding byte range: its bytes once fetched, or the error
// that ended its retry budget.
//
// A chunk also carries a STREAMING watermark. The chunk writeInOrder is
// currently waiting on — the frontier — publishes the buffer it is filling and
// how much of it has arrived after every read, so the in-order writer can hand
// the browser that prefix instead of waiting for the whole chunk. The barrier
// was the bulk of the cost of a mid-transfer cut: measured on the rig
// (bench/2026-09-16/03ece1e95-smoke, mux-standby-8, the active tunnel cut 5.2 s
// into a 50 MB download) the group closed at t, retire+promote landed at t+2.0 s
// and the browser's next byte only at t+7.0 s — detection was ~1.8 s of that and
// the remaining ~5 s was the frontier chunk having to refetch its whole
// remainder on a just-promoted tunnel before ANY of it could be written.
// Uploads, which have no such barrier, recover in 1.7–1.8 s on the same rig.
type rsChunk struct {
	start, end int64
	buf        []byte
	err        error
	done       chan struct{}
	// won is closed the instant the chunk is complete, whichever attempt got
	// there. It is the loser's cancel under the endgame (chunkPlacement.lost).
	won chan struct{}
	// runners counts the attempts still going, so a LOSER's failure cannot
	// fail a chunk whose winner is still reading. dup makes the duplicate
	// at-most-once per chunk.
	runners atomic.Int32
	dup     atomic.Bool
	fin     sync.Once

	mu sync.Mutex
	// pbuf is the buffer the fetch is filling; filled is how many of its leading
	// bytes are final. The fetch owns [filled, len) and the writer reads
	// [emitted, filled) — disjoint regions of ONE buffer, which is what keeps a
	// resumed attempt consistent with what the browser has already seen
	// (fetchChunkRetry re-asks for [start+got, end] and fills the same buffer at
	// that offset, so it can never re-emit or skip a byte).
	pbuf   []byte
	filled int64
	// wake is closed on every advance of filled and replaced, so a writer that
	// captured it under mu is woken exactly once per advance.
	wake chan struct{}
}

// complete records one attempt's outcome. The FIRST success completes the
// chunk; an error completes it only when no other attempt is still running.
func (ch *rsChunk) complete(buf []byte, err error) {
	if err == nil {
		ch.fin.Do(func() {
			ch.buf = buf
			close(ch.won)
			close(ch.done)
		})
		ch.runners.Add(-1)

		return
	}
	if ch.runners.Add(-1) == 0 {
		ch.fin.Do(func() {
			ch.err = err
			close(ch.won)
			close(ch.done)
		})
	}
}

// publish records that buf[:filled] holds the chunk's first filled bytes and
// will not be rewritten. Safe on a nil chunk (a fetch with nowhere to report).
func (ch *rsChunk) publish(buf []byte, filled int64) {
	if ch == nil {
		return
	}
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if filled <= ch.filled {
		return
	}
	ch.pbuf, ch.filled = buf, filled
	close(ch.wake)
	ch.wake = make(chan struct{})
}

// release drops the chunk's buffers once it has been written to the browser.
func (ch *rsChunk) release() {
	ch.mu.Lock()
	ch.pbuf, ch.buf = nil, nil
	ch.mu.Unlock()
}

// streamTo writes the chunk to conn as its bytes arrive, returning once the
// chunk is finished (or conn failed). It reports how many of the chunk's bytes
// reached conn, a write error (the browser is gone) and the fetch's own error —
// the byte count is the caller's resume point, since a streamed prefix must
// never be sent twice and nothing may be skipped between it and the rescue.
//
// A chunk that never publishes progress is written WHOLE on completion, exactly
// as before: an unreporting fetch (the HTTPS path, a test's fake fetch) cannot
// promise that a partial result survives into its retry.
func (ch *rsChunk) streamTo(conn net.Conn) (emitted int64, werr, ferr error) {
	for {
		ch.mu.Lock()
		buf, filled, wake := ch.pbuf, ch.filled, ch.wake
		ch.mu.Unlock()
		if filled > emitted {
			n, err := conn.Write(buf[emitted:filled])
			emitted += int64(n)
			if err != nil {
				return emitted, err, nil
			}

			continue
		}
		select {
		case <-ch.done:
			if ch.err != nil {
				return emitted, nil, ch.err
			}
			// The fetch's returned buffer is the authority on the chunk's
			// bytes; whatever the streamed prefix did not cover goes out here.
			if int64(len(ch.buf)) > emitted {
				n, err := conn.Write(ch.buf[emitted:])
				emitted += int64(n)
				if err != nil {
					return emitted, err, nil
				}
			}

			return emitted, nil, nil
		case <-wake:
		}
	}
}

// rsProgress is how a fetch publishes its partial result: buf[:filled] holds the
// range's first filled bytes and will not be rewritten.
type rsProgress func(buf []byte, filled int64)

// rsFetchFunc fetches one [start, end] byte range, reporting into prog as the
// bytes land. A fetch that cannot resume from a partial result simply never
// calls prog and is delivered whole, as before.
type rsFetchFunc func(start, end int64, prog rsProgress) ([]byte, error)

// rsPlannedFetchFunc is rsFetchFunc under a spread planner: the attempt is also
// handed its placement — the object's ledger, and for an endgame duplicate the
// tunnel it is pinned to and the channel that tells it it lost.
type rsPlannedFetchFunc func(start, end int64, prog rsProgress, p chunkPlacement) ([]byte, error)

// chunkFetches is a running set of concurrent range fetches, started BEFORE the
// consumer needs the bytes (chunk0 is still draining to the browser) and drained
// in order by writeInOrder. Splitting start from drain is what lets the parallel
// streams — and the exit's origin dials behind them — overlap chunk0's delivery
// instead of beginning a full chunk later.
type chunkFetches struct {
	c      *Client
	total  int64
	chunks []*rsChunk
	// sem bounds concurrent FETCHES. It is released the moment a chunk's bytes
	// are in memory, so a slow in-order write to the browser never delays
	// admission of the next fetch (it used to: with concurrency 8, chunk 9
	// could not open its stream until chunk 1 had reached the browser, which
	// forced a whole second wave of per-chunk setup round trips).
	sem chan struct{}
	// mem bounds the TOTAL outstanding buffers (in flight + fetched but not yet
	// written), so decoupling admission from delivery cannot grow an unbounded
	// queue behind a stalled browser.
	mem chan struct{}
	// stop halts the producer when the consumer loop returns early (rescue or
	// write error) — otherwise it would block on a gate forever once nobody
	// drains slots. Closed on every exit path; in-flight fetches finish and
	// exit on their own (their chunk results are simply never read).
	stop     chan struct{}
	stopOnce sync.Once
}

// rsOutstandingFactor caps outstanding chunk buffers at this multiple of the
// configured concurrency: `concurrency` fetches may be in flight while the same
// number of completed chunks wait their turn to be written in order.
const rsOutstandingFactor = 2

// startChunkFetches launches the concurrent fetches for [chunkSize, total) at
// the configured chunk size. Kept for callers with nothing to plan against (no
// known total split point); startChunkFetchesFrom is the planned form.
func (c *Client) startChunkFetches(total int64, fetch func(start, end int64) ([]byte, error)) *chunkFetches {
	return c.startChunkFetchesFrom(c.rsChunkSize(), total, c.rsChunkSize(),
		func(start, end int64, _ rsProgress) ([]byte, error) { return fetch(start, end) })
}

// startChunkFetchesFrom launches the concurrent fetches for [from, total) in
// steps of chunkSize and returns immediately. The caller must eventually call
// writeInOrder (or abort).
func (c *Client) startChunkFetchesFrom(from, total, chunkSize int64, fetch rsFetchFunc) *chunkFetches {
	return c.startChunkFetchesPlanned(from, total, chunkSize, nil,
		func(start, end int64, prog rsProgress, _ chunkPlacement) ([]byte, error) {
			return fetch(start, end, prog)
		})
}

// chunkAdmission is the object-wide in-flight budget for a split download —
// how many chunk fetches may be open at once.
//
// Unset, it is chunk.concurrency exactly as before: ONE budget for the object,
// whatever width it is spread over. That gate is width-BLIND, and it is why the
// first live spread run downloaded at 5.13 MB/s against an 8.23 MB/s single-route
// reference (0.62x) while the uploads of the same run reached 0.91x: three
// tunnels shared the same 8 streams, ~2.7 each, so the object finished near one
// tunnel's solo rate no matter how well the shares were balanced. The striped
// upload never had the cap — it gates on `inflight < live*perTunnel` — which is
// the rule mirrored here.
//
// So once the spread policy STEERS, the budget is sized per active tunnel:
// chunk.tunnel_concurrency × the active width, measured after the planner's
// ensureRoutes has promoted standbys to min_routes. A width of one is not a
// spread at all and keeps the object-wide value, so nothing narrows below
// today's. With no knob set the policy does not steer and this returns
// chunk.concurrency, unchanged.
//
// Never 0: an unbuffered admission gate is a producer that can never admit the
// first chunk, since the release only happens inside the fetch it is waiting to
// start.
func (c *Client) chunkAdmission(pl *spreadPlanner) int {
	conc := c.rsConcurrency()
	if conc < 1 {
		conc = defaultRSConcurrency
	}
	if pl == nil || !pl.pol.steers() {
		return conc
	}
	active := c.activeLiveCount()
	if active < 2 {
		return conc
	}
	per := setChunkTunnelConcurrency()
	if per < 1 {
		per = 1
	}
	return per * active
}

// startChunkFetchesPlanned is startChunkFetchesFrom under a spread planner: the
// fetch is handed the placement for its attempt (the ledger, and for an endgame
// duplicate the tunnel it is pinned to and the channel that tells it it lost),
// and the producer arms the endgame on the tail.
func (c *Client) startChunkFetchesPlanned(from, total, chunkSize int64, pl *spreadPlanner, fetch rsPlannedFetchFunc) *chunkFetches {
	if chunkSize < 1 {
		chunkSize = defaultRSChunkSize
	}
	// The admission budget is read ONCE per download: a value that moved
	// mid-flight would resize an admission gate the producer is already using.
	conc := c.chunkAdmission(pl)
	f := &chunkFetches{
		c:     c,
		total: total,
		sem:   make(chan struct{}, conc),
		mem:   make(chan struct{}, setChunkOutstandingFactor()*conc),
		stop:  make(chan struct{}),
	}
	for start := from; start < total; start += chunkSize {
		end := start + chunkSize - 1
		if end >= total {
			end = total - 1
		}
		f.chunks = append(f.chunks, &rsChunk{
			start: start, end: end,
			done: make(chan struct{}),
			won:  make(chan struct{}),
			wake: make(chan struct{}),
		})
	}

	go func() {
		for i, ch := range f.chunks {
			if !f.acquire(f.mem) || !f.acquire(f.sem) {
				return
			}
			ch.runners.Add(1)
			go func(ch *rsChunk) {
				buf, err := fetch(ch.start, ch.end, ch.publish, chunkPlacement{pl: pl, lost: ch.won})
				<-f.sem // admission released on FETCH completion, not on delivery
				ch.complete(buf, err)
			}(ch)
			// The endgame: once fewer chunks remain than there are tunnels to
			// carry them, the tail is also asked for on the fastest IDLE
			// tunnel and the first answer wins. At most one duplicate per
			// chunk, and only onto a tunnel that would otherwise sit out the
			// rest of the object — so the wire cost is bounded by the tail,
			// not by the object.
			f.maybeDuplicate(ch, len(f.chunks)-i, pl, fetch)
		}
	}()
	return f
}

// maybeDuplicate arms one endgame duplicate for ch when the policy asks for it
// and a tunnel is idle to take it. The duplicate takes no admission slot: it is
// the tail, there is no next chunk whose admission it could delay.
func (f *chunkFetches) maybeDuplicate(ch *rsChunk, remaining int, pl *spreadPlanner, fetch rsPlannedFetchFunc) {
	if pl == nil || !spreadDuplicates(remaining, f.c.activeLiveCount(), pl.pol) {
		return
	}
	idle := pl.pickIdle()
	if idle == nil || !ch.dup.CompareAndSwap(false, true) {
		return
	}
	ch.runners.Add(1)
	go func() {
		// The duplicate publishes into the same watermark: both attempts hold
		// the SAME absolute byte range, so whichever is further ahead may feed
		// the in-order writer.
		buf, err := fetch(ch.start, ch.end, ch.publish, chunkPlacement{pl: pl, pin: idle, lost: ch.won})
		ch.complete(buf, err)
	}()
}

// acquire takes one slot of gate, or reports false once the consumer has stopped.
func (f *chunkFetches) acquire(gate chan struct{}) bool {
	select {
	case gate <- struct{}{}:
		return true
	case <-f.stop:
		return false
	}
}

// abort halts the producer without draining anything (the caller failed before
// it could deliver a byte). Safe to call more than once.
func (f *chunkFetches) abort() { f.stopOnce.Do(func() { close(f.stop) }) }

// writeInOrder writes the fetched chunks to conn in order, STREAMING the
// frontier chunk: the chunk the browser is waiting on is written as its bytes
// arrive rather than after it completes, so a chunk that loses its tunnel
// mid-body costs the browser one detection instead of one whole chunk (see
// rsChunk). Chunks behind the frontier still buffer — nothing may overtake the
// byte order — and stream their remainder once their turn comes.
//
// When a chunk exhausts its retry budget, the split does NOT truncate the
// download: it degrades to the SEQUENTIAL rescue path — rescue streams
// [start, total) as one ranged request straight to conn, resumed from the byte
// offset actually delivered across attempts, so a degraded-but-alive session
// completes the download slowly instead of emitting a clean-looking short body
// (observed live: a 50MB download "succeeding" with exactly the first 4MiB
// chunk). Only when the rescue itself makes no progress does the download end
// short — and then the browser still detects it (Content-Length mismatch).
// rescue may be nil (a caller without a sequential path keeps the old
// truncating behavior).
func (f *chunkFetches) writeInOrder(conn net.Conn, rescue func(w net.Conn, start int64) (int64, error)) {
	defer f.abort()
	for _, ch := range f.chunks {
		emitted, werr, ferr := ch.streamTo(conn)
		if werr != nil {
			break // the browser is gone; caller closes conn
		}
		if ferr != nil {
			if f.c.appCl != nil {
				f.c.appCl.Log().Debugf("range-split: chunk %d-%d failed after %d streamed bytes: %v", ch.start, ch.end, emitted, ferr)
			}
			if rescue != nil {
				// Resume at the first byte the browser has NOT seen. The
				// streamed prefix must not be sent twice and no byte may be
				// skipped between it and the rescue.
				f.c.rescueTail(conn, ch.start+emitted, f.total, rescue)
			}
			break // rescued (or truncated with no rescue); caller closes conn
		}
		ch.release()
		<-f.mem // the buffer is gone; let the producer queue another chunk
	}
}

// streamRemainingChunks fetches [chunkSize, total) as concurrent ranges and writes
// them to conn in order, using fetch to retrieve one [start,end] byte range (the
// caller supplies the plaintext or TLS-over-exit fetch). It is startChunkFetches
// followed immediately by writeInOrder, for callers with nothing to overlap.
func (c *Client) streamRemainingChunks(conn net.Conn, total int64, fetch func(start, end int64) ([]byte, error), rescue func(w net.Conn, start int64) (int64, error)) {
	c.startChunkFetches(total, fetch).writeInOrder(conn, rescue)
}

// rsRescueAttempts is how many consecutive ZERO-PROGRESS rescue attempts end the
// download. An attempt that delivers any bytes resets the count — the rescue
// resumes from the delivered offset, so a slow-but-alive session keeps making
// progress until the download completes, however long that takes.
const rsRescueAttempts = 3

// rescueTail sequentially streams [start, total) to conn via rescue, resuming
// from the delivered offset after a failed attempt. It gives up only after
// rsRescueAttempts consecutive attempts deliver nothing.
func (c *Client) rescueTail(conn net.Conn, start, total int64, rescue func(w net.Conn, start int64) (int64, error)) {
	if c.appCl != nil {
		c.appCl.Log().Warnf("range-split: parallel fetch failed at byte %d/%d — degrading to sequential streaming for the remainder", start, total)
	}
	cur := start
	zero := 0
	for cur < total && zero < rsRescueAttempts {
		n, err := rescue(conn, cur)
		cur += n
		if n > 0 {
			zero = 0
		} else {
			zero++
		}
		if err == nil && cur >= total {
			break
		}
	}
	if c.appCl != nil {
		if cur >= total {
			c.appCl.Log().Infof("range-split: sequential rescue completed the download (%d bytes)", total)
		} else {
			c.appCl.Log().Warnf("range-split: sequential rescue gave up at byte %d/%d — download ends short (client detects via Content-Length)", cur, total)
		}
	}
}

// fetchChunkRetry fetches one byte range, redialing a fresh stream on failure and
// retrying over a time budget so a transient all-tunnels-down window (a --tunnels
// rotation) is waited out rather than truncating the download.
// A failed attempt keeps the bytes it did deliver: the retry asks for
// [start+got, end] and fills the REST of the same buffer, rather than starting
// the chunk over. A tunnel dies mid-body — that is the whole point of the
// errSessionClosed free retry — and re-pulling the megabytes that already
// arrived is what made the head chunk's recovery cost a full chunk time on the
// survivor (measured on the rig 2026-09-17: 19.7 s to the first byte after a
// first-hop cut, against ~1 s when the writer happened to be inside a chunk
// that was not on the cut tunnel). Ranges are absolute byte offsets at the
// origin, so a resume is just the next ranged GET; nothing about the chunk plan
// changes.
// The same single buffer is what makes the frontier chunk STREAMABLE: every
// read publishes buf[:got+n] to prog, and a resumed attempt appends past that
// watermark, so bytes already handed to the browser are never re-fetched and
// never re-emitted.
func (c *Client) fetchChunkRetry(req *http.Request, host, validator string, start, end int64, prog rsProgress, p chunkPlacement) ([]byte, error) {
	buf := make([]byte, end-start+1)
	var got int64
	return retryWithBudget(func() ([]byte, error) {
		// An endgame duplicate whose twin already delivered the chunk stops
		// here rather than opening another stream for bytes that have arrived.
		if p.raceLost() {
			return nil, errChunkRaceLost
		}
		n, err := c.fetchChunk(req, host, validator, start+got, end, buf[got:], func(k int) {
			if prog != nil {
				prog(buf, got+int64(k))
			}
		}, p)
		got += n
		if err != nil {
			return nil, err
		}
		return buf, nil
	}, setChunkRetryBudget(), rsChunkRetryBackoff, rsChunkRetryBackoffMax)
}

// retryWithBudget calls fetch until it succeeds, or until at least rsChunkRetries
// tries have been made AND the time budget has elapsed, backing off (capped at
// backoffMax) between attempts. Factored out of fetchChunkRetry so the retry
// policy is unit-testable with small durations. On persistent failure it returns
// the last error.
func retryWithBudget(fetch func() ([]byte, error), budget, backoff, backoffMax time.Duration) ([]byte, error) {
	deadline := time.Now().Add(budget)
	var err error
	free := 0
	for attempt := 1; ; attempt++ {
		var buf []byte
		if buf, err = fetch(); err == nil {
			return buf, nil
		}
		// The chunk is already delivered by the attempt that won the endgame
		// race. There is nothing left to retry.
		if errors.Is(err, errChunkRaceLost) {
			return nil, err
		}
		// The tunnel died — or was SNUBBED — under the attempt: refetch
		// immediately on another one. No backoff (there is nothing to wait for —
		// the next pick skips both) and no attempt charged, so a mid-download
		// tunnel loss costs the chunk one round trip instead of a sleep plus a
		// slice of its budget. Bounded by rsFreeRetries.
		if freeRetry(err) && free < setChunkFreeRetries() {
			free++
			attempt--

			continue
		}
		if attempt >= rsChunkRetries && time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(backoff)
		if backoff < backoffMax {
			if backoff *= 2; backoff > backoffMax {
				backoff = backoffMax
			}
		}
	}
}

// openChunkStream picks a live tunnel for a receive-heavy range stream and opens
// a stream on it, returning the tunnel alongside the stream so the caller can
// watch it die. A tunnel that is closed — or that closes between the pick and
// the open — yields errSessionClosed rather than a generic error, so the caller
// refetches at once on another tunnel instead of backing off.
//
// kind says whether this stream is one of SEVERAL parallel chunk streams
// (pickSibling) or the only stream its transfer has (pickLone) — a rescue tail,
// an upload probe, an upload's completion fetch. Only a sibling may audition a
// standby tunnel; see pickKind.
func (c *Client) openChunkStream(kind pickKind) (*yamux.Session, net.Conn, error) {
	return c.openChunkStreamFor(chunkPlacement{}, 0, kind)
}

// chunkPlacement is where ONE chunk attempt goes and how its bytes are
// accounted. The zero value is today's behavior: no ledger, no pin, no race.
type chunkPlacement struct {
	// pl is the object's ledger. nil, or a policy that steers nothing, leaves
	// the choice to pickSessionFor.
	pl *spreadPlanner
	// pin is the ONE tunnel this attempt must use — set only for an endgame
	// duplicate, whose whole purpose is the tunnel it rides. A pinned attempt
	// never falls back to the picker: with the pin gone there is nothing to
	// duplicate onto and the original attempt stands alone.
	pin *yamux.Session
	// prefer is the tunnel the OBJECT's burst plan named for this chunk
	// (uploadStripe.burst). Unlike a pin it is a preference and not a
	// requirement: a burst plan is written before any chunk has reported back,
	// so a tunnel that has gone away since falls through to the picker rather
	// than failing the attempt.
	prefer *yamux.Session
	// lost is closed when another attempt won this chunk. The loser stops
	// retrying and its stream is closed under it, so a duplicate cannot go on
	// spending a tunnel on bytes that have already arrived.
	lost <-chan struct{}
}

// pickDir is the direction the picker must weigh for this chunk, taken from
// the object's planner: a striped upload's chunk is scored on the tunnels'
// UPLOAD capacity, a range chunk on their download capacity. With no planner
// (an upload probe, a replay, a rescue tail) it is the download direction,
// which is what every caller got before the upload direction existed.
func (p chunkPlacement) pickDir() pickDir {
	if p.pl != nil && p.pl.dir == spreadUp {
		return pickSend
	}
	return pickRecv
}

// raceLost reports whether another attempt already won this chunk.
func (p chunkPlacement) raceLost() bool {
	if p.lost == nil {
		return false
	}
	select {
	case <-p.lost:
		return true
	default:
		return false
	}
}

// errChunkRaceLost ends a duplicate attempt whose twin already delivered the
// bytes. It is never surfaced: the chunk is complete.
var errChunkRaceLost = errors.New("skysocks: another attempt delivered the chunk")

// openChunkStreamFor is openChunkStream under a spread planner: the planner
// chooses the tunnel from the object's byte ledger rather than the picker
// choosing it from the tunnels' stream counts, and the chunk's size is BOOKED
// against the tunnel at admission, so concurrent picks see the reservation
// rather than all landing on the tunnel that has not moved a byte yet.
// kind is the picker's fallback kind — see openChunkStream — and applies only
// when the planner does not steer this attempt.
func (c *Client) openChunkStreamFor(p chunkPlacement, size int64, kind pickKind) (*yamux.Session, net.Conn, error) {
	sess := p.pin
	// booked says the reservation for this chunk is already on the ledger, so
	// the error paths below know whether they owe it back.
	booked := false
	if sess == nil {
		// pick() chooses AND books in one critical section. Booking after the
		// Open() below instead was the 2026-09-16 bug: with an admission gate
		// of chunk.tunnel_concurrency × active tunnels every chunk of the
		// object is admitted at once, every pick resolved against the same
		// pre-charge ledger, and all 12 chunks of a 50 MB download landed on
		// the one tunnel that had not moved a byte yet.
		sess = p.pl.pick(size)
		booked = sess != nil
	}
	if sess == nil && p.prefer != nil && !p.prefer.IsClosed() {
		// The object's burst plan named this tunnel. It is consulted only after
		// the spread policy, which is the operator's instruction and outranks
		// it, and only when the plan's tunnel is still there.
		sess = p.prefer
	}
	if sess == nil || sess.IsClosed() {
		if booked {
			p.pl.uncharge(sess, size)
			booked = false
		}
		if p.pin != nil {
			return nil, nil, fmt.Errorf("%w: the pinned tunnel is gone", errSessionClosed)
		}
		sess = c.pickSessionKind(p.pickDir(), kind)
	}
	if sess == nil {
		return nil, nil, errAllTunnelsDown
	}
	if !booked {
		// The pinned tunnel and the pickSessionFor fallback are booked here:
		// they are not the planner's choice, but their bytes are the object's.
		p.pl.charge(sess, size)
	}
	if sess.IsClosed() {
		p.pl.uncharge(sess, size)
		return nil, nil, fmt.Errorf("%w: closed between the pick and the open", errSessionClosed)
	}
	st, err := sess.Open()
	if err != nil {
		// Nothing went out: the reservation must not steer the rest of the
		// object away from a tunnel that carried no bytes for it.
		p.pl.uncharge(sess, size)
		if sess.IsClosed() {
			return nil, nil, fmt.Errorf("%w: %v", errSessionClosed, err)
		}

		return nil, nil, err
	}

	return sess, st, nil
}

// lostWatcher closes an in-flight chunk stream the moment ANOTHER attempt wins
// the chunk — the endgame's counterpart to tunnelGuard. Without it the loser of
// a race reads out a full duplicate body nobody will use, occupying the one
// idle tunnel the duplicate was supposed to make use of.
type lostWatcher struct {
	done chan struct{}
	once sync.Once
}

// watchLost arms the watcher. The caller must stop() it when its attempt
// returns; with no race to lose (lost == nil) it starts no goroutine.
func watchLost(st net.Conn, lost <-chan struct{}) *lostWatcher {
	w := &lostWatcher{done: make(chan struct{})}
	if lost == nil {
		return w
	}
	go func() {
		select {
		case <-lost:
			st.Close() //nolint:errcheck,gosec
		case <-w.done:
		}
	}()

	return w
}

func (w *lostWatcher) stop() { w.once.Do(func() { close(w.done) }) }

// tunnelGuard closes an in-flight range stream the moment its tunnel dies, and
// remembers that it did so.
//
// A chunk read on a dead tunnel must not wait out rsProbeTimeout or
// rsChunkIdleTimeout: those deadlines exist to fail a SLOW tunnel, and a tunnel
// whose route group is gone is not slow, it is gone. Closing the stream unblocks
// whatever the attempt is parked on — the SOCKS5 handshake, the response header,
// the body read, or a TLS handshake on the HTTPS path, which sits above the
// stream and so cannot see yamux's own shutdown error — and fired() then tells
// the caller the failure was the tunnel, not the fetch.
type tunnelGuard struct {
	c     *Client
	sess  *yamux.Session
	fired atomic.Bool
	done  chan struct{}
	once  sync.Once

	// m is set only by guardChunkTunnel: the meter of the tunnel this attempt
	// is OUTSTANDING WORK on, for the per-tunnel snub. nil on the paths that
	// must never be snubbed — the sequential rescue, the upload probe and the
	// generic POST relay — because those are the liveness fallbacks and
	// aborting them re-issues nothing.
	m       *tunnelMeter
	snubbed atomic.Bool
}

// guardTunnel starts watching sess for st. The caller must call err (or stop) to
// release the watcher.
//
// The watcher also RETIRES the tunnel on the spot. A chunk's read is the first
// thing in the client to learn that a route group is gone — yamux unblocks it
// the moment the group's conn errors — while the retire that replaces the
// tunnel from the standby pool hangs off the keepalive loop's own sighting of
// s.IsClosed(), which is up to tunnelRTTProbeInterval (5 s) later. Measured on
// the rig 2026-09-17 (bench/2026-09-16/e0d9e0430-smoke, mux-standby-8): the cut
// tunnel's group closed 3.65 s after the cut and tunnel_retired/tunnel_promoted
// only landed at 8.6 s, and until that promote every refetched chunk piled onto
// the ONE surviving active tunnel — the pool's standby tunnels sit out of
// pickSessionFor by design until one is promoted. Retiring here collapses that
// window to the close itself: the failover promote happens in the same instant
// the chunk fails, so the refetch has the promoted tunnel to land on.
func (c *Client) guardTunnel(sess *yamux.Session, st net.Conn) *tunnelGuard {
	return c.guard(sess, st, nil)
}

// guardChunkTunnel is guardTunnel for an attempt that counts as OUTSTANDING
// WORK on its tunnel — a range chunk, a striped-upload chunk. Beyond the death
// watch it books the attempt on the tunnel.s meter (so the snub evaluator can
// see that the tunnel is holding work) and aborts the attempt the moment the
// tunnel is snubbed, which is what re-issues the chunk elsewhere.
//
// The paths that stay on plain guardTunnel are the ones that must not be
// snubbed: the sequential rescue is the download's liveness fallback, and the
// upload probe and the generic POST relay are single attempts with nowhere to
// be re-issued to.
func (c *Client) guardChunkTunnel(sess *yamux.Session, st net.Conn) *tunnelGuard {
	return c.guard(sess, st, c.meterOf(sess))
}

func (c *Client) guard(sess *yamux.Session, st net.Conn, m *tunnelMeter) *tunnelGuard {
	g := &tunnelGuard{c: c, sess: sess, m: m, done: make(chan struct{})}
	m.startWork(time.Now())
	var snubC <-chan struct{}
	if m != nil {
		snubC = m.snubChan()
	}
	go func() {
		select {
		case <-sess.CloseChan():
			g.fired.Store(true)
			st.Close() //nolint:errcheck,gosec
			if g.c != nil {
				g.c.retireTunnel(sess, "tunnel closed under a range fetch")
			}
		case <-snubC:
			// The tunnel went silent with this chunk on it. Unblock the
			// attempt exactly as a death would — the chunk is refetched at
			// once on another tunnel — but do NOT retire anything: a snubbed
			// tunnel is alive, and it gets its one-chunk probe back after the
			// hold.
			g.snubbed.Store(true)
			st.Close() //nolint:errcheck,gosec
		case <-g.done:
		}
	}()

	return g
}

// meterOf returns the meter of a tunnel in the current set, or nil.
func (c *Client) meterOf(sess *yamux.Session) *tunnelMeter {
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()
	return c.recvStamp[sess]
}

// stop releases the watcher and, with it, the tunnel.s outstanding-work count.
// Both under the same sync.Once, so a chunk is booked off its tunnel exactly
// once however many times the caller releases the guard.
func (g *tunnelGuard) stop() {
	g.once.Do(func() {
		close(g.done)
		g.m.endWork()
	})
}

// abandoned reports that THIS CLIENT took the attempt's stream away — the
// tunnel was snubbed, or it died and the watcher closed the stream under the
// body. It is the same test err() classifies by, asked WITHOUT releasing the
// watcher, because an attempt can end with a STATUS rather than an error: yamux
// serves reads after a local close (0magnet/yamux stream.go, "LocalClose only
// prohibits further local writes"), so an origin that answers our own
// truncation — the sink's short-chunk 400 — has that answer reach us on the
// stream we just closed. Such an answer is not a verdict on the chunk.
func (g *tunnelGuard) abandoned() bool {
	if g == nil {
		return false
	}
	if g.snubbed.Load() || (g.m != nil && g.m.isSnubbed()) {
		return true
	}
	return g.fired.Load() || g.sess.IsClosed()
}

// err releases the watcher and classifies the attempt's outcome: an error on a
// tunnel that is gone becomes errSessionClosed, which retries free of backoff
// and free of budget. The session is re-checked here rather than trusting the
// watcher alone — yamux usually unblocks the read with its own shutdown error
// before the watcher goroutine gets to run, so fired() on its own would miss the
// common case and charge the death to the chunk.
func (g *tunnelGuard) err(err error) error {
	g.stop()
	if err == nil {
		return err
	}
	// A SNUB is checked before the death: both can be true at once (the snub
	// closed the stream, and the tunnel then died anyway), and the snub is the
	// classification that must not retire a live tunnel.
	if g.snubbed.Load() || (g.m != nil && g.m.isSnubbed()) {
		return fmt.Errorf("%w: %v", errTunnelSnubbed, err)
	}
	if !g.fired.Load() && !g.sess.IsClosed() {
		return err
	}
	// Retire here too, not only in the watcher: stop() and the close can race in
	// the watcher's select (both channels ready), and this path is reached
	// whenever the attempt itself noticed the death first. retireTunnel is
	// once-only per tunnel, so the two callers cannot double-promote.
	if g.c != nil {
		g.c.retireTunnel(g.sess, "tunnel closed under a range fetch")
	}

	return fmt.Errorf("%w: %v", errSessionClosed, err)
}

// rsRescueIdleTimeout is the per-read progress window of a sequential rescue
// stream: each successful read refreshes it, so a slow-but-moving tail runs to
// completion while a genuinely silent stream fails within one window (the same
// bytes-are-liveness standard the tunnel keepalive applies).
const rsRescueIdleTimeout = 60 * time.Second

// streamTailOnce makes ONE attempt to stream [start, total) to w over a fresh
// exit stream: ranged GET like fetchChunk, but the body is copied straight
// through (never buffered — the tail can be most of the file) under a
// progress-refreshed idle timeout. Returns the bytes actually written, so the
// caller resumes from start+written on failure.
func (c *Client) streamTailOnce(w net.Conn, req *http.Request, host, validator string, start, total int64, pl *spreadPlanner) (n int64, err error) {
	sess, st, err := c.openChunkStreamFor(chunkPlacement{pl: pl}, 0, pickLone)
	if err != nil {
		return 0, err
	}
	// The rescue's bytes count toward the object's shares like any others; it
	// is one stream, so they are booked as they are delivered.
	defer func() { pl.charge(sess, n) }()
	defer st.Close() //nolint:errcheck,gosec
	g := c.guardTunnel(sess, st)
	defer func() { err = g.err(err) }()

	_ = st.SetDeadline(time.Now().Add(rsProbeTimeout)) //nolint:errcheck
	if err := c.exitConnect(st, host, c.rangePlainPort()); err != nil {
		return 0, err
	}
	if _, err := st.Write(buildRangedGet(req, host, validator, start, total-1)); err != nil {
		return 0, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(st), req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("tail %d-%d: status %d (validator changed?)", start, total-1, resp.StatusCode)
	}
	return copyWithIdleTimeout(w, resp.Body, st, total-start, rsRescueIdleTimeout)
}

// copyWithIdleTimeout copies up to limit bytes from body to dst, refreshing a
// read deadline on the underlying conn before every read — progress keeps the
// copy alive indefinitely; idle silence fails it within one window. Returns the
// bytes written (the caller's resume offset).
func copyWithIdleTimeout(dst io.Writer, body io.Reader, under net.Conn, limit int64, idle time.Duration) (int64, error) {
	var written int64
	// zeroReads guards the io.Reader contract's discouraged-but-legal (0, nil)
	// return: a reader stuck returning it would otherwise turn this loop into a
	// core-pegging spin (no bytes, no error, no block). A handful in a row is
	// tolerated; a streak means the reader is broken — fail the attempt and let
	// the caller's resume/retry logic decide.
	zeroReads := 0
	const maxZeroReads = 64
	buf := make([]byte, 32<<10)
	for written < limit {
		_ = under.SetReadDeadline(time.Now().Add(idle)) //nolint:errcheck
		room := int64(len(buf))
		if rem := limit - written; rem < room {
			room = rem
		}
		n, err := body.Read(buf[:room])
		if n > 0 {
			zeroReads = 0
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return written, werr
			}
			written += int64(n)
		} else if err == nil {
			if zeroReads++; zeroReads >= maxZeroReads {
				return written, fmt.Errorf("reader stuck: %d consecutive zero-byte reads with no error", zeroReads)
			}
		}
		if err != nil {
			if err == io.EOF && written >= limit {
				return written, nil
			}
			return written, err
		}
	}
	return written, nil
}

// fetchChunk opens a new exit stream, SOCKS5-CONNECTs to host:80, issues a ranged
// GET carrying the original request's headers plus If-Range, and fills buf with
// exactly the requested bytes [start, end].
//
// It returns how many bytes it DID deliver even on failure, so the caller can
// resume the chunk from start+n instead of re-fetching what already arrived.
// buf must be end-start+1 long. onRead (may be nil) is called with the bytes of
// buf filled SO FAR after every read, so the in-order writer can stream the
// frontier chunk out as it arrives.
func (c *Client) fetchChunk(req *http.Request, host, validator string, start, end int64, buf []byte, onRead func(filled int), p chunkPlacement) (n int64, err error) {
	size := end - start + 1
	sess, st, err := c.openChunkStreamFor(p, size, pickSibling)
	if err != nil {
		return 0, err
	}
	// The ledger booked the whole chunk at admission; correct it to what this
	// attempt actually carried, so a chunk that failed and went elsewhere does
	// not leave its first tunnel charged for bytes it never moved.
	defer func() { p.pl.settle(sess, size, n) }()
	defer st.Close() //nolint:errcheck,gosec
	// A tunnel that dies under this fetch fails it AT ONCE — the deadlines below
	// are for a slow tunnel, not a gone one — and the failure is labeled
	// errSessionClosed so the chunk is refetched immediately on a live tunnel.
	// guardChunkTunnel also books the chunk as outstanding work on the tunnel
	// and aborts it the same way if the tunnel is SNUBBED.
	g := c.guardChunkTunnel(sess, st)
	defer func() { err = g.err(err) }()
	// A duplicate that loses the endgame race has its stream closed under it.
	w := watchLost(st, p.lost)
	defer w.stop()

	_ = st.SetDeadline(time.Now().Add(rsProbeTimeout)) //nolint:errcheck
	// ONE round trip: the SOCKS5 greeting, the CONNECT and the ranged GET go out
	// back-to-back and only then are the three replies read in sequence. Serially
	// blocking on each reply cost three round trips per chunk — at a 250ms RTT,
	// three quarters of a second of dead time before the first byte of every
	// 4 MiB chunk.
	if err := c.exitConnectPipelined(st, host, c.rangePlainPort(), buildRangedGet(req, host, validator, start, end)); err != nil {
		return 0, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(st), req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("chunk %d-%d: status %d (validator changed?)", start, end, resp.StatusCode)
	}
	// Body read under a ROLLING deadline: the single rsProbeTimeout set above
	// (right for failing a dead stream's handshake fast) also covered the whole
	// multi-MB body, making completion mathematically impossible below
	// ~chunkSize/rsProbeTimeout per stream — every chunk read a few MB, timed
	// out, was discarded and refetched, and the sequential rescue ended up
	// carrying the file (measured live: 48.5MB received for a 20MB download,
	// 2.4x waste). Rolling the deadline forward on every read means only a
	// genuinely STALLED stream fails; a slow-but-moving one completes.
	// The body is read through the tunnel's meter: every byte stamps lastByteAt,
	// which is the download half of the snub's progress evidence. It is stamped
	// per TUNNEL and not per chunk on purpose — a chunk queued behind two others
	// is not a silent tunnel, and the bytes those two are delivering say so.
	got, rerr := readChunkBodyProgress(st, meteredBody{r: resp.Body, m: c.meterOf(sess)}, buf[:end-start+1], setChunkIdleTimeout(), onRead)
	return int64(got), rerr
}

// readDeadliner is the one thing readChunkBody needs of the exit stream: a
// rolling read deadline. Narrowed to an interface so the loop is testable
// without a live yamux session.
type readDeadliner interface {
	SetReadDeadline(t time.Time) error
}

// readChunkBody fills buf from body, refreshing st's read deadline before every
// read so only a genuinely stalled stream fails. It returns the bytes it did
// fill alongside the error, so a failed attempt can be RESUMED from that offset
// (fetchChunkRetry re-asks for [start+got, end]) instead of starting over.
//
// The zero-read guard is copyWithIdleTimeout's, for the same reason: a body
// stuck returning the io.Reader contract's discouraged-but-legal (0, nil) pegs
// this loop at 100 % with no read ever blocking, so the rolling deadline never
// fires, retryWithBudget is never reached, and writeInOrder waits on this chunk
// forever while it holds its memory permits — a silent all-paths stall. A
// streak means a broken reader: fail the attempt and let the retry decide.
func readChunkBody(st readDeadliner, body io.Reader, buf []byte, idle time.Duration) (int, error) {
	return readChunkBodyProgress(st, body, buf, idle, nil)
}

// readChunkBodyProgress is readChunkBody reporting its watermark: onRead (nil to
// skip) is called with the bytes filled so far after every read that delivered
// any, which is what lets writeInOrder stream the frontier chunk.
func readChunkBodyProgress(st readDeadliner, body io.Reader, buf []byte, idle time.Duration, onRead func(filled int)) (int, error) {
	got := 0
	zeroReads := 0
	const maxZeroReads = 64
	for got < len(buf) {
		_ = st.SetReadDeadline(time.Now().Add(idle)) //nolint:errcheck
		n, rerr := body.Read(buf[got:])
		got += n
		if n > 0 {
			zeroReads = 0
			if onRead != nil {
				onRead(got)
			}
		} else if rerr == nil {
			if zeroReads++; zeroReads >= maxZeroReads {
				return got, fmt.Errorf("chunk body stuck: %d consecutive zero-byte reads with no error", zeroReads)
			}
		}
		if rerr != nil {
			if got == len(buf) && rerr == io.EOF {
				break
			}
			return got, rerr
		}
	}
	return got, nil
}

// socks5Greeting is the no-auth method-selection greeting; buildSocks5Connect
// returns it followed by the CONNECT request, so the two can be written together
// or one at a time.
var socks5Greeting = []byte{0x05, 0x01, 0x00}

// buildSocks5Connect returns the SOCKS5 greeting followed by a CONNECT to
// host:port with ATYP=domain, so the EXIT resolves the name (matching how the
// browser's original request reached it).
func buildSocks5Connect(host string, port int) ([]byte, error) {
	if len(host) > 255 {
		return nil, fmt.Errorf("host too long: %d", len(host))
	}
	out := make([]byte, 0, len(socks5Greeting)+5+len(host)+2)
	out = append(out, socks5Greeting...)
	out = append(out, 0x05, 0x01, 0x00, 0x03, byte(len(host))) //nolint:gosec // len(host)<=255 checked above
	out = append(out, host...)
	out = append(out, byte(port>>8), byte(port&0xff)) //nolint:gosec // port is 80 or 443, well within a byte pair
	return out, nil
}

// readSocks5Handshake reads the method-selection reply and then the CONNECT
// reply, in the order the exit's SOCKS5 server emits them.
func readSocks5Handshake(st net.Conn) error {
	method := make([]byte, 2)
	if _, err := io.ReadFull(st, method); err != nil {
		return err
	}
	if method[0] != 0x05 || method[1] != 0x00 {
		return fmt.Errorf("exit selected non-no-auth method %v", method)
	}
	_, err := readSocks5Reply(st)
	return err
}

// exitConnect performs the SOCKS5 client handshake to the exit's proxy server and
// a CONNECT to host:port, blocking on the method reply before sending CONNECT.
// Used where there is nothing to pipeline behind the CONNECT (the sequential
// rescue, which must handshake before wrapping the stream in TLS or before its
// single long-lived ranged GET).
func (c *Client) exitConnect(st net.Conn, host string, port int) error {
	head, err := buildSocks5Connect(host, port)
	if err != nil {
		return err
	}
	greeting, connect := head[:len(socks5Greeting)], head[len(socks5Greeting):]
	if _, err := st.Write(greeting); err != nil {
		return err
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(st, method); err != nil {
		return err
	}
	if method[0] != 0x05 || method[1] != 0x00 {
		return fmt.Errorf("exit selected non-no-auth method %v", method)
	}
	if _, err := st.Write(connect); err != nil {
		return err
	}
	_, err = readSocks5Reply(st)
	return err
}

// exitConnectPipelined writes the SOCKS5 greeting, the CONNECT request and
// payload back-to-back in ONE write, and only then reads the method reply and
// the CONNECT reply. The exit's SOCKS5 server reads its handshake sequentially
// from the stream, so the queued payload is simply the next thing it reads once
// it starts splicing to the origin. A failed greeting or CONNECT is handled
// exactly as before — the caller closes the stream and the queued payload is
// never acted on. payload may be nil (handshake only).
func (c *Client) exitConnectPipelined(st net.Conn, host string, port int, payload []byte) error {
	if err := c.exitWriteConnect(st, host, port, payload); err != nil {
		return err
	}
	return readSocks5Handshake(st)
}

// exitWriteConnect is exitConnectPipelined's write half, split out for the
// striped upload: it queues the greeting, the CONNECT and the head, and the
// caller then starts writing MORE payload (the chunk body) while it reads the
// handshake replies. The exit reads its handshake sequentially off the stream
// and only then splices, so everything queued behind it is simply the next thing
// it reads — the same property the injected GET of rangeSplitInner relies on.
func (c *Client) exitWriteConnect(st net.Conn, host string, port int, payload []byte) error {
	head, err := buildSocks5Connect(host, port)
	if err != nil {
		return err
	}
	_, err = st.Write(append(head, payload...))
	return err
}

// splicePrefixed is the original two-way splice, optionally replaying bytes already
// read from the browser to the exit first (so the exit sees an identical stream).
func (c *Client) splicePrefixed(conn, stream net.Conn, clientPrefix []byte) {
	const errorCount = 2
	errCh := make(chan error, errorCount)
	go func() {
		var src io.Reader = conn
		if len(clientPrefix) > 0 {
			src = io.MultiReader(bytes.NewReader(clientPrefix), conn)
		}
		_, err := io.Copy(stream, src)
		errCh <- err
	}()
	go func() {
		_, err := io.Copy(conn, stream)
		errCh <- err
	}()
	for i := 0; i < errorCount; i++ {
		if err := <-errCh; err != nil && c.appCl != nil {
			c.appCl.Log().Debugf("Copy error: %v", err)
		}
		if i == 0 {
			conn.Close()   //nolint:errcheck,gosec
			stream.Close() //nolint:errcheck,gosec
		}
	}
}

// --- small HTTP/SOCKS5 helpers ---

// socks5OKReply is the one reply a successful CONNECT can have: VER 5, REP 0
// (succeeded), RSV 0, ATYP IPv4, BND.ADDR 0.0.0.0, BND.PORT 0. BND names the
// address the proxy bound toward the origin and is only meaningful to BIND, so
// a CONNECT client ignores it — which is what lets the splitter answer the
// browser before the exit has spoken (rangeSplitInner step 1).
var socks5OKReply = []byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}

// rsBadGatewayBody is the reason of last resort: a 502 whose caller had no
// error to name.
const rsBadGatewayBody = "skysocks: the exit could not reach the origin"

// rsReasonMax caps the reason line. It is a header value and a body, and an
// error carrying a whole sink response would make it neither readable nor safe.
const rsReasonMax = 512

// badGatewayReason flattens err into one header-safe line: control characters
// (a CR or LF above all, which would end the header early) become spaces, runs
// of blanks collapse, and the result is capped at rsReasonMax.
func badGatewayReason(err error) string {
	if err == nil {
		return ""
	}
	flat := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, err.Error())
	flat = strings.TrimSpace(strings.Join(strings.Fields(flat), " "))
	if len(flat) > rsReasonMax {
		flat = flat[:rsReasonMax] + "…"
	}
	return flat
}

// writeBadGateway answers an HTTP request the splitter has already read with a
// minimal 502 THAT SAYS WHY. It is what a failure becomes once the browser has
// been told CONNECT succeeded: the refusal cannot be relayed as SOCKS any more,
// and closing silently is indistinguishable from a network fault.
//
// err is the reason, and it is written twice — as the X-Upload-Error header, so
// a bench runner or a script can read it off a failed row without a log, and as
// the body, so a person sees it. A nil err falls back to rsBadGatewayBody.
func writeBadGateway(conn net.Conn, err error) {
	reason := badGatewayReason(err)
	if reason == "" {
		reason = rsBadGatewayBody
	}
	body := reason + "\n"
	_, _ = fmt.Fprintf(conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Type: text/plain; charset=utf-8\r\nX-Upload-Error: %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", //nolint:errcheck
		reason, len(body), body)
}

// connectFailure names why step 4 of rangeSplitInner gave up: the reply could
// not be read, the exit refused with a SOCKS5 status, or the pipelined GET that
// rode ahead of it never got out.
func connectFailure(host string, reply []byte, readErr, injectErr error) error {
	switch {
	case readErr != nil:
		return fmt.Errorf("connect %s: the exit's reply could not be read: %w", host, readErr)
	case len(reply) >= 2 && reply[1] != 0x00:
		return fmt.Errorf("connect %s: the exit refused it (socks5 reply %d)", host, reply[1])
	case injectErr != nil:
		return fmt.Errorf("connect %s: the pipelined request could not be written: %w", host, injectErr)
	default:
		return fmt.Errorf("connect %s: the exit's reply was malformed (%d bytes)", host, len(reply))
	}
}

// readSocks5Reply reads one SOCKS5 reply (VER REP RSV ATYP ADDR PORT) and returns
// its raw bytes.
func readSocks5Reply(r io.Reader) ([]byte, error) {
	h := make([]byte, 4)
	if _, err := io.ReadFull(r, h); err != nil {
		return nil, err
	}
	if h[0] != 0x05 {
		return nil, fmt.Errorf("bad socks5 reply ver %d", h[0])
	}
	var addrLen int
	switch h[3] {
	case 0x01:
		addrLen = 4
	case 0x03:
		l := make([]byte, 1)
		if _, err := io.ReadFull(r, l); err != nil {
			return nil, err
		}
		out := append(h, l...)
		rest := make([]byte, int(l[0])+2)
		if _, err := io.ReadFull(r, rest); err != nil {
			return nil, err
		}
		return append(out, rest...), nil
	case 0x04:
		addrLen = 16
	default:
		return nil, fmt.Errorf("bad socks5 atyp %d", h[3])
	}
	rest := make([]byte, addrLen+2)
	if _, err := io.ReadFull(r, rest); err != nil {
		return nil, err
	}
	return append(h, rest...), nil
}

// httpMethodTokens are the request-method prefixes (with the trailing space) used
// to decide whether a :80 stream carries HTTP, without waiting for a full line.
var httpMethodTokens = []string{"GET ", "POST ", "PUT ", "HEAD ", "DELETE ", "OPTIONS ", "PATCH ", "TRACE ", "CONNECT "}

// classifyHTTP reports whether buf is the start of an HTTP request. decided=false
// means buf is still a strict prefix of some method token — read more before
// deciding. An empty buf is always undecided.
func classifyHTTP(buf []byte) (decided, isHTTP bool) {
	s := string(buf)
	prefixOfSome := false
	for _, m := range httpMethodTokens {
		if strings.HasPrefix(s, m) {
			return true, true
		}
		if len(s) < len(m) && strings.HasPrefix(m, s) {
			prefixOfSome = true
		}
	}
	if prefixOfSome {
		return false, false
	}
	return true, false
}

// peekRequestHead reads from conn just far enough to decide whether it carries an
// HTTP request and, if so, to capture the whole header block (up to limit). It
// returns isHTTP=false — with everything it read, for verbatim replay — the instant
// the bytes cannot be an HTTP method, when the head exceeds the cap, when extra
// bytes trail the header terminator (pipelining we won't split), or on any error.
func peekRequestHead(conn net.Conn, limit int) (head []byte, isHTTP bool) {
	buf := make([]byte, 0, 1024)
	tmp := make([]byte, 1024)

	_ = conn.SetReadDeadline(time.Now().Add(rsClassifyTimeout)) //nolint:errcheck
	for {
		if decided, ok := classifyHTTP(buf); decided {
			if !ok {
				return buf, false
			}
			break
		}
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			return buf, false
		}
	}

	_ = conn.SetReadDeadline(time.Now().Add(rsHeadReadTimeout)) //nolint:errcheck
	term := []byte("\r\n\r\n")
	for !bytes.Contains(buf, term) {
		if len(buf) >= limit {
			return buf, false
		}
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			return buf, false
		}
	}
	if bytes.Index(buf, term)+4 < len(buf) {
		return buf, false // bytes trail the header block (pipelined) — do not split
	}
	return buf, true
}

// splittableRequest reports whether req is a plain GET we can range-split.
func splittableRequest(req *http.Request) bool {
	if req == nil || req.Method != http.MethodGet {
		return false
	}
	if req.ProtoMajor != 1 {
		return false
	}
	if req.Header.Get("Range") != "" {
		return false
	}
	if req.Header.Get("Upgrade") != "" {
		return false
	}
	return true
}

// injectRange inserts a single Range header (bytes=0-end, the probe for the
// first chunk) before the blank line of an HTTP head.
func injectRange(head []byte, end int64) []byte {
	if !bytes.HasSuffix(head, []byte("\r\n\r\n")) {
		return head
	}
	prefix := head[:len(head)-2] // drop the terminating blank line's CRLF
	line := fmt.Sprintf("Range: bytes=0-%d\r\n\r\n", end)
	out := make([]byte, 0, len(prefix)+len(line))
	out = append(out, prefix...)
	out = append(out, line...)
	return out
}

// buildRangedGet reconstructs a GET carrying the original request's headers (minus
// hop-by-hop and range headers) plus Range, If-Range and Connection: close.
func buildRangedGet(req *http.Request, host, validator string, start, end int64) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "GET %s HTTP/1.1\r\n", req.URL.RequestURI())
	fmt.Fprintf(&b, "Host: %s\r\n", req.Host)
	for k, vs := range req.Header {
		switch textproto.CanonicalMIMEHeaderKey(k) {
		case "Range", "Connection", "Proxy-Connection", "Keep-Alive", "If-Range", "Content-Length", "Transfer-Encoding":
			continue
		}
		for _, v := range vs {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	fmt.Fprintf(&b, "Range: bytes=%d-%d\r\n", start, end)
	if validator != "" {
		fmt.Fprintf(&b, "If-Range: %s\r\n", validator)
	}
	b.WriteString("Connection: close\r\n\r\n")
	_ = host
	return []byte(b.String())
}

// writeSynth200 writes a 200 response header derived from stream0's 206 headers,
// with the full Content-Length and Content-Range dropped.
func writeSynth200(conn net.Conn, hdr textproto.MIMEHeader, total int64) error {
	var b strings.Builder
	b.WriteString("HTTP/1.1 200 OK\r\n")
	for k, vs := range hdr {
		switch textproto.CanonicalMIMEHeaderKey(k) {
		case "Content-Range", "Content-Length", "Connection", "Keep-Alive", "Transfer-Encoding":
			continue
		}
		for _, v := range vs {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	fmt.Fprintf(&b, "Content-Length: %d\r\n", total)
	b.WriteString("Connection: close\r\n\r\n")
	_, err := conn.Write([]byte(b.String()))
	return err
}

// ifRangeValidator returns a strong ETag if present, else Last-Modified, for use
// as an If-Range value so a resource that changes mid-download aborts cleanly.
func ifRangeValidator(hdr textproto.MIMEHeader) string {
	if et := hdr.Get("Etag"); et != "" && !strings.HasPrefix(et, "W/") {
		return et
	}
	return hdr.Get("Last-Modified")
}

// statusCode parses the numeric code from an HTTP status line.
func statusCode(line string) int {
	f := strings.Fields(line)
	if len(f) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(f[1]) //nolint:errcheck // a non-numeric status yields 0, handled by callers
	return n
}

// parseContentRangeTotal extracts the total size from "bytes start-end/total".
func parseContentRangeTotal(cr string) (int64, bool) {
	i := strings.LastIndex(cr, "/")
	if i < 0 {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(cr[i+1:]), 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func numChunks(total, chunkSize int64) int64 {
	return (total + chunkSize - 1) / chunkSize
}

// meteredBody stamps the tunnel's byte clock as a chunk body arrives. The stamp
// is what the per-tunnel snub reads: a tunnel delivering ANY chunk's bytes is
// making progress, whatever any individual chunk is waiting for.
type meteredBody struct {
	r io.Reader
	m *tunnelMeter
}

func (b meteredBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if n > 0 && b.m != nil {
		b.m.noteChunkByte(time.Now())
	}
	return n, err
}
