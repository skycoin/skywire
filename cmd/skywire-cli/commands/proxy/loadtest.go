// Package skysocksc cmd/skywire-cli/commands/proxy/loadtest.go c4-vis-cli
package skysocksc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/net/proxy"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/loadtest"
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
	loadtestServeCmd.Flags().Int64Var(&loadtest.UploadWindow, "upload-window", loadtest.UploadWindow,
		"bytes one chunked upload may hold out of order (the sink never holds the object)")
	loadtestServeCmd.Flags().IntVar(&loadtest.UploadSessions, "upload-sessions", loadtest.UploadSessions,
		"concurrent chunked uploads; the sink's memory ceiling is this times --upload-window")
	loadtestServeCmd.Flags().DurationVar(&loadtest.UploadIdle, "upload-idle", loadtest.UploadIdle,
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
		srv := &http.Server{Addr: loadtestServeAddr, Handler: loadtest.Handler(), ReadHeaderTimeout: 10 * time.Second}
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
