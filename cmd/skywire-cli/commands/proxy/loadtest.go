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

// loadtestFixed serves exactly n bytes of the pattern with its SHA-256 in
// X-Sha256 (the hash is computed in a first pass, which is fast next to the
// transfer it certifies).
func loadtestFixed(w http.ResponseWriter, r *http.Request, nStr string) {
	n, err := strconv.ParseUint(nStr, 10, 63)
	if err != nil || n == 0 {
		http.Error(w, "bytes: want a positive integer", http.StatusBadRequest)
		return
	}
	const chunk = 256 * 1024
	buf := make([]byte, chunk)
	h := sha256.New()
	for off := uint64(0); off < n; off += chunk {
		m := uint64(chunk)
		if n-off < m {
			m = n - off
		}
		loadtestPattern(buf[:m], n, off)
		h.Write(buf[:m]) //nolint:errcheck,gosec // hash.Hash never errors
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.FormatUint(n, 10))
	w.Header().Set("X-Sha256", hex.EncodeToString(h.Sum(nil)))
	if r.Method == http.MethodHead {
		return
	}
	fl, _ := w.(http.Flusher)
	for off := uint64(0); off < n; off += chunk {
		m := uint64(chunk)
		if n-off < m {
			m = n - off
		}
		loadtestPattern(buf[:m], n, off)
		if _, err := w.Write(buf[:m]); err != nil {
			return
		}
		if fl != nil {
			fl.Flush()
		}
	}
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
