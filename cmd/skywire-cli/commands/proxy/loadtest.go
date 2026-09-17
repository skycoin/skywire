// Package skysocksc cmd/skywire-cli/commands/proxy/loadtest.go c4-vis-cli
package skysocksc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/net/proxy"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// loadtest is the controlled-load rig for the WASM-routing-policy gates. The
// per-leg telemetry harness (`proxy mux info --ndjson`) records what each mux
// leg carries; but to attribute throughput changes to the POLICY (a rotating-bw
// leg swap, an elastic-mux grow) rather than to load noise, the offered load
// must be STEADY — a curl-loop restarts every file and leaves ~80s gaps that
// masquerade as policy-induced dips.
//
// This provides both ends, self-contained:
//
//   - `loadtest serve` runs an endless byte source (a /dev/zero over HTTP,
//     chunked, streamed at the reader's line rate via TCP backpressure). Run it
//     on a visor the proxy's exit can reach — the controlled far-end.
//   - `loadtest run` opens ONE persistent stream through a proxy session's SOCKS
//     port and reads continuously, counting bytes IN-PROCESS against a monotonic
//     clock. Each fixed slice it emits {t_ms, bytes, goodput_bps, gap} — a slice
//     that received zero bytes (a read stall) is marked gap=true, so a real
//     throughput dip is unambiguous and never confused with a load gap.
//
// Pair `loadtest run` with `mux info --ndjson` over the same window: the former
// is the exact app-goodput series (gap-marked), the latter the per-leg series
// (gate_state, rotation). Together they prove a preset honors its claim.

var (
	loadtestServeAddr string
	loadtestName      string
	loadtestAddr      string
	loadtestURL       string
	loadtestDuration  time.Duration
	loadtestSlice     time.Duration
	loadtestOutput    string
	loadtestReadKB    int
)

func init() {
	loadtestCmd.AddCommand(loadtestServeCmd, loadtestRunCmd)
	RootCmd.AddCommand(loadtestCmd)

	loadtestServeCmd.Flags().StringVarP(&loadtestServeAddr, "addr", "a", ":9999",
		"listen address for the endless byte source")
	loadtestServeCmd.Flags().Int64Var(&loadtestUploadWindow, "upload-window", loadtestUploadWindow,
		"bytes one chunked upload may hold out of order (the sink never holds the object)")
	loadtestServeCmd.Flags().IntVar(&loadtestUploadSessions, "upload-sessions", loadtestUploadSessions,
		"concurrent chunked uploads; the sink's memory ceiling is this times --upload-window")
	loadtestServeCmd.Flags().DurationVar(&loadtestUploadIdle, "upload-idle", loadtestUploadIdle,
		"expire a chunked upload that has made no progress for this long")

	loadtestRunCmd.Flags().StringVarP(&loadtestName, "name", "n", "", "proxy session name (informational)")
	loadtestRunCmd.Flags().StringVar(&loadtestAddr, "socks", skyenv.SkysocksClientAddr,
		"SOCKS5 address of the proxy session to drive load through")
	loadtestRunCmd.Flags().StringVarP(&loadtestURL, "url", "u", "",
		"URL of a `loadtest serve` endless source, reachable from the proxy exit (required)")
	loadtestRunCmd.Flags().DurationVarP(&loadtestDuration, "duration", "d", 5*time.Minute, "how long to pull")
	loadtestRunCmd.Flags().DurationVar(&loadtestSlice, "slice", 500*time.Millisecond,
		"per-sample accounting slice (a zero-byte slice is a gap)")
	loadtestRunCmd.Flags().StringVarP(&loadtestOutput, "output", "o", "-", "NDJSON sink ('-' = stdout)")
	loadtestRunCmd.Flags().IntVar(&loadtestReadKB, "read-kb", 128, "read buffer size in KB")
}

var loadtestCmd = &cobra.Command{
	Use:   "loadtest",
	Short: "Controlled steady-load rig for routing-policy tests (serve a sink + record exact goodput/gaps)",
}

var loadtestServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run an endless byte source (the controlled far-end sink for load tests)",
	Long: `Serve an endless chunked octet-stream on GET / at the reader's line rate
(TCP backpressure sets the rate; the source itself never gaps). Run this on a
visor the proxy exit can reach; point 'loadtest run --url' at it.

GET /?bytes=N serves exactly N bytes of a deterministic pattern and names its
SHA-256 in the X-Sha256 header, so a transfer can be checked for completeness
AND integrity, not just size. POST or PUT /upload discards the body and
answers {"bytes":N,"sha256":"…"} for the same check in the upload direction.

/upload also takes an upload as acked, offset-addressed chunks, so an upload
survives losing the tunnel under it: HEAD /upload advertises 'X-Chunked-Upload:
bytes', then PUT /upload?id=<id>&bytes=<N> with 'Content-Range: bytes s-e/N'
answers each chunk with 'X-Upload-Received: <durable prefix>' — 200 once the
chunk is inside that prefix, 202 while it only waits out of order (still
evictable, so keep it) — and the chunk that completes the object answers the
same JSON a plain POST does. The body is hashed over the contiguous prefix and
never held: out-of-order chunks wait in a bounded window (--upload-window).`,
	Run: func(cmd *cobra.Command, _ []string) {
		buf := make([]byte, 256*1024) // zeros
		mux := http.NewServeMux()
		mux.HandleFunc("/upload", loadtestUpload)
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if n := r.URL.Query().Get("bytes"); n != "" {
				loadtestFixed(w, r, n)
				return
			}
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
		})
		srv := &http.Server{Addr: loadtestServeAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
			<-sig
			_ = srv.Close() //nolint:errcheck
		}()
		fmt.Fprintf(os.Stderr, "loadtest serve: endless source on %s (ctrl+c to stop)\n", loadtestServeAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("serve: %w", err))
		}
	},
}

var loadtestRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Drive a steady stream through a proxy session and record exact goodput + gaps as NDJSON",
	Run: func(cmd *cobra.Command, _ []string) {
		if strings.TrimSpace(loadtestURL) == "" {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--url is required (a 'loadtest serve' source reachable from the proxy exit)"))
		}
		socks := "127.0.0.1" + loadtestAddr
		if !strings.HasPrefix(loadtestAddr, ":") {
			socks = loadtestAddr
		}
		dialer, err := proxy.SOCKS5("tcp", socks, nil, proxy.Direct)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("SOCKS5 dialer: %w", err))
		}
		client := &http.Client{Transport: &http.Transport{
			DialContext: func(_ context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			},
			DisableCompression: true,
		}}

		sink := os.Stdout
		if loadtestOutput != "-" {
			f, ferr := os.OpenFile(loadtestOutput, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // G304: operator-supplied NDJSON output path
			if ferr != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("open --output: %w", ferr))
			}
			defer f.Close() //nolint:errcheck
			sink = f
		}
		enc := json.NewEncoder(sink)

		ctx, cancel := context.WithTimeout(context.Background(), loadtestDuration)
		defer cancel()
		go func() {
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
			select {
			case <-sig:
				cancel()
			case <-ctx.Done():
			}
		}()

		req, rErr := http.NewRequestWithContext(ctx, http.MethodGet, loadtestURL, nil)
		if rErr != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("build request: %w", rErr))
		}
		resp, err := client.Do(req)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("connect to source through proxy: %w", err))
		}
		defer resp.Body.Close() //nolint:errcheck,gosec
		fmt.Fprintf(os.Stderr, "loadtest run: pulling %s via %s for %s, slice=%s → %s\n",
			loadtestURL, socks, loadtestDuration, loadtestSlice, loadtestOutput)

		// Read goroutine accumulates bytes into an atomic; the main loop snapshots
		// and resets it each slice, so accounting is exact and a stalled read
		// surfaces as a zero-byte (gap) slice rather than skewing a neighbor.
		var acc atomic.Int64
		readErr := make(chan error, 1)
		go func() {
			b := make([]byte, loadtestReadKB*1024)
			for {
				n, rerr := resp.Body.Read(b)
				if n > 0 {
					acc.Add(int64(n))
				}
				if rerr != nil {
					readErr <- rerr
					return
				}
			}
		}()

		start := time.Now()
		ticker := time.NewTicker(loadtestSlice)
		defer ticker.Stop()
		slSec := loadtestSlice.Seconds()
		for {
			select {
			case <-ctx.Done():
				return
			case rerr := <-readErr:
				_ = enc.Encode(loadtestSample(time.Since(start), 0, slSec, true, rerr.Error())) //nolint:errcheck
				return
			case <-ticker.C:
				n := acc.Swap(0)
				_ = enc.Encode(loadtestSample(time.Since(start), n, slSec, n == 0, "")) //nolint:errcheck
			}
		}
	},
}

// loadtestRecord is one NDJSON accounting slice.
type loadtestRecord struct {
	Ts         string  `json:"ts"`
	TMs        int64   `json:"t_ms"`
	Bytes      int64   `json:"bytes"`
	GoodputBps float64 `json:"goodput_bps"`
	Gap        bool    `json:"gap"`
	Err        string  `json:"err,omitempty"`
}

// loadtestSample builds a slice record. goodput = bytes*8/sliceSeconds; a
// zero-byte slice is flagged as a gap. Pure so the accounting is unit-tested.
func loadtestSample(elapsed time.Duration, bytes int64, sliceSeconds float64, gap bool, errStr string) loadtestRecord {
	var bps float64
	if sliceSeconds > 0 {
		bps = float64(bytes) * 8.0 / sliceSeconds
	}
	return loadtestRecord{
		Ts:         elapsed.String(),
		TMs:        elapsed.Milliseconds(),
		Bytes:      bytes,
		GoodputBps: bps,
		Gap:        gap,
		Err:        errStr,
	}
}

// loadtestPattern fills buf with a deterministic byte pattern derived from
// seed and the absolute offset, so the same request always produces the same
// bytes and a corrupted or truncated transfer cannot hash to the expected
// value. An xorshift over the offset is cheap and has no long runs of one
// byte, unlike zeros, which would let a stalled reader look like progress
// under some compressing transports.
func loadtestPattern(buf []byte, seed, offset uint64) {
	x := seed ^ (offset * 0x9E3779B97F4A7C15)
	for i := range buf {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		buf[i] = byte(x & 0xff)
	}
}

// loadtestChunk is the pattern's seeding granularity: the body is generated (and
// hashed) 256 KiB at a time from the ABSOLUTE offset, so any byte range is a
// slice of its aligned chunks and no range needs the bytes before it.
const loadtestChunk = 256 * 1024

// loadtestSumEntry is one cached whole-object hash. ready is closed once sum is
// set, so the concurrent ranged GETs of a single range-split download share the
// one computation instead of each starting their own.
type loadtestSumEntry struct {
	ready chan struct{}
	sum   string
}

// loadtestSumCacheMax bounds the cache. The bench uses a handful of sizes; past
// the cap a size is hashed per request, exactly as before.
const loadtestSumCacheMax = 64

var (
	loadtestSumMu    sync.Mutex
	loadtestSums     = map[uint64]*loadtestSumEntry{}
	loadtestSumCalcs atomic.Uint64 // whole-object hash computations (the test asserts the cache holds)
)

// loadtestSum returns the hex SHA-256 of the whole n-byte body, computing it at
// most once per n. The body is a pure function of n, so the hash is too — but it
// used to be recomputed on EVERY request, including each ranged GET of a
// range-split download: 12 chunks of a 50 MB object hashed 600 MB on a 2-core
// exit, ~1.6 s of CPU that the equivalent single GET never paid, landing in
// time-to-first-byte and taxing the split for being a split.
func loadtestSum(n uint64) string {
	loadtestSumMu.Lock()
	e, hit := loadtestSums[n]
	if hit {
		loadtestSumMu.Unlock()
		<-e.ready
		return e.sum
	}
	e = &loadtestSumEntry{ready: make(chan struct{})}
	if len(loadtestSums) < loadtestSumCacheMax {
		loadtestSums[n] = e
	}
	loadtestSumMu.Unlock()

	loadtestSumCalcs.Add(1)
	buf := make([]byte, loadtestChunk)
	h := sha256.New()
	for off := uint64(0); off < n; off += loadtestChunk {
		m := uint64(loadtestChunk)
		if n-off < m {
			m = n - off
		}
		loadtestPattern(buf[:m], n, off)
		h.Write(buf[:m]) //nolint:errcheck,gosec // hash.Hash never errors
	}
	e.sum = hex.EncodeToString(h.Sum(nil))
	close(e.ready)
	return e.sum
}

// loadtestFixed serves exactly n bytes of the pattern with the whole object's
// SHA-256 in X-Sha256 (cached per n by loadtestSum, so a ranged GET generates
// only the bytes it serves).
func loadtestFixed(w http.ResponseWriter, r *http.Request, nStr string) {
	n, err := strconv.ParseUint(nStr, 10, 63)
	if err != nil || n == 0 {
		http.Error(w, "bytes: want a positive integer", http.StatusBadRequest)
		return
	}
	const chunk = loadtestChunk
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
	w.Header().Set("X-Sha256", loadtestSum(n))
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
		loadtestPattern(buf[:m], n, off)
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
// in a bounded reorder window (--upload-window, 16 MiB) and are absorbed when the
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
	// loadtestUploadWindow bounds the bytes ONE session may buffer: chunks held
	// ahead of the frontier plus the chunks being read. A frontier chunk is
	// always admitted (held chunks are evicted for it if need be, and re-sent by
	// the client, which got 202 and not 200 for them), so the window can never
	// deadlock a session.
	loadtestUploadWindow int64 = 16 << 20
	// loadtestUploadSessions caps concurrent sessions, so the sink's ceiling is
	// window x sessions (64 MiB by default) on an exit that has been OOM-killed
	// for less (#4252).
	loadtestUploadSessions = 4
	// loadtestUploadIdle expires a session that stopped making progress; its
	// buffers go with it.
	loadtestUploadIdle = 2 * time.Minute
)

// loadtestNow is the sink's clock, so the idle expiry is testable.
var loadtestNow = time.Now

const (
	// loadtestUploadReadStep is how much of a chunk is read between progress
	// stamps. A chunk read slower than --upload-idle is exactly what the degrade
	// bench produces, and a session evicted mid-read would commit into an orphan
	// and walk its prefix backwards, so liveness is measured on BYTES ARRIVING,
	// not on when the chunk was admitted.
	loadtestUploadReadStep = 256 << 10
	// loadtestUploadSealedMemo is how many completed objects stay answerable for
	// a late re-send or a resume probe. Sealed sessions hold no chunk buffers and
	// do not occupy a concurrency slot; past this many, the oldest is dropped.
	loadtestUploadSealedMemo = 8
)

var (
	// loadtestUploadExpired counts sessions dropped by the idle sweep, and
	// loadtestUploadEvicted counts held chunks dropped to admit a frontier chunk.
	// Both lose bytes a sender must re-send, so neither may be silent.
	loadtestUploadExpired atomic.Uint64
	loadtestUploadEvicted atomic.Uint64
)

// loadtestWindow is the reorder window as an unsigned bound, so all the offset
// arithmetic stays in one signedness. A non-positive flag value means no room
// for anything out of order, not an enormous window.
func loadtestWindow() uint64 {
	if loadtestUploadWindow <= 0 {
		return 0
	}
	return uint64(loadtestUploadWindow)
}

// loadtestUploadSession is one object in flight. hash rolls over the contiguous
// prefix only — the object itself is never held — and held keeps the chunks that
// arrived ahead of acked, keyed by their start offset.
type loadtestUploadSession struct {
	mu        sync.Mutex
	total     uint64 // immutable after creation
	acked     uint64 // contiguous prefix absorbed into hash
	hash      hash.Hash
	sum       string // set once acked == total
	held      map[uint64][]byte
	heldBytes uint64
	inflight  uint64 // admitted chunks still being read off the wire
	last      time.Time
}

var (
	loadtestUploadMu sync.Mutex
	loadtestUploads  = map[string]*loadtestUploadSession{}
)

// loadtestUpload is the upload-direction sink. A plain POST (no id, no
// Content-Range) is the unchanged whole-body path; an id with a Content-Range is
// a chunk of a resumable object.
func loadtestUpload(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodHead, http.MethodOptions:
		loadtestUploadAdvertise(w)
		return
	case http.MethodGet:
		loadtestUploadStatus(w, r)
		return
	case http.MethodPost, http.MethodPut:
	default:
		loadtestUploadRefuse(w, r, http.StatusMethodNotAllowed, "POST or PUT a body")
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	cr := strings.TrimSpace(r.Header.Get("Content-Range"))
	if id == "" && cr == "" {
		loadtestUploadWhole(w, r)
		return
	}
	loadtestUploadChunk(w, r, id, cr)
}

// loadtestUploadRefuse answers a request whose body will not be absorbed. Go's
// server gives up on the connection once more than 256 KiB of a declared body is
// left unread, so a refusal that just returns closes the socket under a sender
// that is still writing its chunk: it takes EPIPE and never reads the
// X-Next-Offset/Retry-After the refusal was carrying, which is the whole
// back-pressure protocol. Reading the chunk out of the way first costs no memory
// (it goes to io.Discard) and keeps the connection reusable; a body too large to
// be any chunk of ours gets a deliberate, documented close instead.
func loadtestUploadRefuse(w http.ResponseWriter, r *http.Request, code int, msg string) {
	if !loadtestUploadDrain(r) {
		w.Header().Set("Connection", "close")
	}
	http.Error(w, msg, code)
}

// loadtestUploadDrain discards up to one window's worth of the body — the
// largest chunk the sink would ever have admitted. It reports whether the body
// was fully consumed.
func loadtestUploadDrain(r *http.Request) bool {
	if r.Body == nil {
		return true
	}
	limit := int64(loadtestWindow()) //nolint:gosec // G115: loadtestWindow is bounded by the flag
	if limit < 1<<20 {
		limit = 1 << 20
	}
	n, _ := io.CopyN(io.Discard, r.Body, limit+1) //nolint:errcheck // a read error is a dead conn either way
	return n <= limit
}

// loadtestUploadWhole reads and discards the body and answers with the byte
// count and SHA-256 it saw, so the sender can check both against what it sent.
func loadtestUploadWhole(w http.ResponseWriter, r *http.Request) {
	h := sha256.New()
	n, err := io.Copy(h, r.Body)
	if err != nil {
		http.Error(w, "read: "+err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"bytes": n, "sha256": hex.EncodeToString(h.Sum(nil))}) //nolint:errcheck
}

// loadtestUploadAdvertise is the opt-in signal. X-Chunked-Upload is the ONLY
// thing a client may key the chunked path on: sending a partial body to an
// origin that did not advertise it is data corruption.
func loadtestUploadAdvertise(w http.ResponseWriter) {
	w.Header().Set("X-Chunked-Upload", "bytes")
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("X-Upload-Window", strconv.FormatInt(loadtestUploadWindow, 10))
	w.Header().Set("X-Upload-Sessions", strconv.Itoa(loadtestUploadSessions))
	w.Header().Set("X-Upload-Idle", loadtestUploadIdle.String())
	w.Header().Set("Allow", "GET, HEAD, OPTIONS, POST, PUT")
	w.WriteHeader(http.StatusOK)
}

// loadtestUploadStatus answers a resume probe: how much of the object is
// durable, and the hash once it is whole.
func loadtestUploadStatus(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "id: want the upload session id", http.StatusBadRequest)
		return
	}
	loadtestUploadMu.Lock()
	s := loadtestUploads[id]
	loadtestUploadMu.Unlock()
	if s == nil {
		http.Error(w, "no such upload session", http.StatusNotFound)
		return
	}
	s.mu.Lock()
	s.last = loadtestNow()
	acked, sum, total := s.acked, s.sum, s.total
	s.mu.Unlock()
	loadtestUploadAck(w, http.StatusOK, id, "", acked, total, sum)
}

// loadtestUploadChunk absorbs one offset-addressed chunk.
func loadtestUploadChunk(w http.ResponseWriter, r *http.Request, id, cr string) {
	if id == "" {
		loadtestUploadRefuse(w, r, http.StatusBadRequest, "id: required with Content-Range")
		return
	}
	if len(id) > 128 {
		loadtestUploadRefuse(w, r, http.StatusBadRequest, "id: too long")
		return
	}
	start, end, total, ok := parseContentRange(cr)
	if !ok {
		loadtestUploadRefuse(w, r, http.StatusBadRequest, "Content-Range: want 'bytes <start>-<end>/<total>'")
		return
	}
	if q := r.URL.Query().Get("bytes"); q != "" {
		n, err := strconv.ParseUint(q, 10, 63)
		if err != nil || n != total {
			loadtestUploadRefuse(w, r, http.StatusBadRequest, "bytes: disagrees with Content-Range")
			return
		}
	}
	length := end - start + 1
	if length > loadtestWindow() {
		loadtestUploadRefuse(w, r, http.StatusRequestEntityTooLarge, "chunk larger than the reorder window")
		return
	}
	if r.ContentLength >= 0 && uint64(r.ContentLength) != length { //nolint:gosec // G115: guarded non-negative
		loadtestUploadRefuse(w, r, http.StatusBadRequest, "Content-Length disagrees with Content-Range")
		return
	}

	s, code, msg := loadtestUploadSessionFor(id, total)
	if s == nil {
		if code == http.StatusServiceUnavailable {
			w.Header().Set("Retry-After", "1")
		}
		loadtestUploadRefuse(w, r, code, msg)
		return
	}

	// Admission, then the read, then the commit — the read is NOT done under the
	// session lock, so the concurrent chunks of one object do not serialize. The
	// admitted length is reserved across the read, so the window bounds what is
	// actually in memory.
	s.mu.Lock()
	s.last = loadtestNow()
	if s.sum != "" || end < s.acked { // already durable: absorb the resend, do not re-hash
		acked, sum := s.acked, s.sum
		s.mu.Unlock()
		_, _ = io.Copy(io.Discard, r.Body) //nolint:errcheck
		loadtestUploadAck(w, http.StatusOK, id, cr, acked, total, sum)
		return
	}
	if start > s.acked {
		// Ahead of the frontier: it must fit the window, or the client backs off
		// and re-sends it later.
		if end >= s.acked+loadtestWindow() || s.inflight+s.heldBytes+length > loadtestWindow() {
			acked := s.acked
			s.mu.Unlock()
			w.Header().Set("X-Next-Offset", strconv.FormatUint(acked, 10))
			w.Header().Set("X-Upload-Received", strconv.FormatUint(acked, 10))
			w.Header().Set("Retry-After", "1")
			loadtestUploadRefuse(w, r, http.StatusTooEarly, "reorder window full")
			return
		}
	} else {
		// The frontier chunk always wins: held chunks are not durable (they were
		// answered 202, not 200), so evicting the furthest of them only costs a
		// re-send, while refusing the frontier would stall the object forever.
		for s.inflight+s.heldBytes+length > loadtestWindow() && len(s.held) > 0 {
			s.evictFurthest()
		}
		if s.inflight+s.heldBytes+length > loadtestWindow() {
			acked := s.acked
			s.mu.Unlock()
			w.Header().Set("X-Next-Offset", strconv.FormatUint(acked, 10))
			w.Header().Set("X-Upload-Received", strconv.FormatUint(acked, 10))
			w.Header().Set("Retry-After", "1")
			loadtestUploadRefuse(w, r, http.StatusTooEarly, "reorder window full")
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
		loadtestUploadRefuse(w, r, http.StatusBadRequest, "short chunk: "+err.Error())
		return
	}

	ackCode := http.StatusOK
	s.mu.Lock()
	s.inflight -= length
	s.last = loadtestNow()
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
	acked, sum := s.acked, s.sum
	s.mu.Unlock()
	loadtestUploadAck(w, ackCode, id, cr, acked, total, sum)
}

// readChunk reads one admitted chunk, stamping the session's liveness as the
// bytes arrive. Stamping only at admission would let the idle sweep evict a
// session whose chunk is merely slow — the degrade bench's whole shape — and the
// slow reader would then commit into an orphaned session while the next chunk
// opened a fresh one, walking the durable prefix backwards with no error and no
// log.
func (s *loadtestUploadSession) readChunk(body io.Reader, buf []byte) error {
	for off := 0; off < len(buf); {
		end := off + loadtestUploadReadStep
		if end > len(buf) {
			end = len(buf)
		}
		n, err := io.ReadFull(body, buf[off:end])
		off += n
		if n > 0 {
			s.mu.Lock()
			s.last = loadtestNow()
			s.mu.Unlock()
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// absorb folds bytes that start exactly at the frontier into the rolling hash.
func (s *loadtestUploadSession) absorb(b []byte) {
	if len(b) == 0 {
		return
	}
	s.hash.Write(b) //nolint:errcheck,gosec // hash.Hash never errors
	s.acked += uint64(len(b))
}

// drain absorbs every held chunk the frontier has reached, repeatedly, since
// absorbing one can make the next contiguous.
func (s *loadtestUploadSession) drain() {
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
func (s *loadtestUploadSession) evictFurthest() {
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
	loadtestUploadEvicted.Add(1)
}

// finish seals the object once the prefix is whole.
func (s *loadtestUploadSession) finish() {
	if s.sum != "" || s.acked < s.total {
		return
	}
	s.sum = hex.EncodeToString(s.hash.Sum(nil))
	s.held = map[uint64][]byte{}
	s.heldBytes = 0
}

// loadtestUploadAck writes the per-chunk ack. code is 200 when the chunk is
// inside the durable contiguous prefix and 202 when it is only held in the
// reorder window — a held chunk can still be evicted for the frontier, so the
// sender must keep it. X-Upload-Received is the durable contiguous prefix — the
// only offset a sender may resume from. The chunk that completes the object
// answers with the plain POST path's JSON, so the bench's hash check does not
// change.
func loadtestUploadAck(w http.ResponseWriter, code int, id, cr string, acked, total uint64, sum string) {
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

// loadtestUploadSweep drops the sessions that no longer earn their memory and
// reports how many still occupy a concurrency slot. It is called with
// loadtestUploadMu held; keep is the id this request is for, which is never
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
func loadtestUploadSweep(keep string) int {
	type sealedSession struct {
		id   string
		last time.Time
	}
	now := loadtestNow()
	live := 0
	var sealed []sealedSession
	for k, v := range loadtestUploads {
		v.mu.Lock()
		idle, busy, done, last := now.Sub(v.last), v.inflight > 0, v.sum != "", v.last
		v.mu.Unlock()
		if !busy && idle > loadtestUploadIdle && k != keep {
			delete(loadtestUploads, k)
			loadtestUploadExpired.Add(1)
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
	if len(sealed) > loadtestUploadSealedMemo {
		sort.Slice(sealed, func(i, j int) bool { return sealed[i].last.Before(sealed[j].last) })
		for _, e := range sealed[:len(sealed)-loadtestUploadSealedMemo] {
			if e.id == keep {
				continue
			}
			delete(loadtestUploads, e.id)
		}
	}
	return live
}

// loadtestUploadSessionFor finds or opens a session, expiring idle ones first so
// a stalled upload's buffers are not what refuses the next one. It returns a
// status code and message instead of a session when it refuses.
func loadtestUploadSessionFor(id string, total uint64) (*loadtestUploadSession, int, string) {
	loadtestUploadMu.Lock()
	defer loadtestUploadMu.Unlock()
	live := loadtestUploadSweep(id)
	if s, ok := loadtestUploads[id]; ok {
		if s.total != total {
			return nil, http.StatusConflict, "id is in use for an object of a different size"
		}
		return s, 0, ""
	}
	if live >= loadtestUploadSessions {
		return nil, http.StatusServiceUnavailable, "too many concurrent upload sessions"
	}
	s := &loadtestUploadSession{
		total: total,
		hash:  sha256.New(),
		held:  map[uint64][]byte{},
		last:  loadtestNow(),
	}
	loadtestUploads[id] = s
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
