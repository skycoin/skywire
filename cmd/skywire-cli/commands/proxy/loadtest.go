// Package skysocksc cmd/skywire-cli/commands/proxy/loadtest.go c4-vis-cli
package skysocksc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
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
answers {"bytes":N,"sha256":"…"} for the same check in the upload direction.`,
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

// loadtestUpload is the upload-direction sink: it reads and discards the body
// and answers with the byte count and SHA-256 it saw, so the sender can check
// both against what it sent.
func loadtestUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		http.Error(w, "POST or PUT a body", http.StatusMethodNotAllowed)
		return
	}
	h := sha256.New()
	n, err := io.Copy(h, r.Body)
	if err != nil {
		http.Error(w, "read: "+err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"bytes": n, "sha256": hex.EncodeToString(h.Sum(nil))}) //nolint:errcheck
}
