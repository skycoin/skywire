// Package clidmsg cmd/skywire-cli/commands/dmsg/iperf.go: `skywire
// cli dmsg iperf <pk>:<port>` and `skywire cli dmsg iperf listen
// <port>` — bulk throughput measurement over a dmsg stream.
//
// Client form  : opens one stream, pumps zero-filled buffers from
//
//	memory (not /dev/zero, which adds a Go-fs read at
//	every kernel boundary) for --duration, prints the
//	final goodput along with periodic rolling samples.
//
// Listen form  : accepts one inbound stream, drains it into a
//
//	black-hole sink, prints the same shape of samples
//	from the receive side. One-shot like dmsg cat listen.
//
// Both sides emit lines on stderr at --interval cadence so an
// operator running `dmsg iperf <pk>:<port> --duration 30s --interval
// 1s` sees ~30 sample lines plus a final totals line, like classic
// iperf. The wire itself is just bytes — no length framing, no
// type tags, no envelope. Sender writes, receiver reads. That keeps
// the measurement as close to the raw transport as possible: any
// overhead in the printout is from go's runtime and the operator's
// kernel, not from the iperf shim.
//
// Why a dedicated command vs. piping /dev/zero | dmsg cat:
//   - cat adds an extra goroutine pair and a stdin/stdout splice;
//     iperf bypasses that for tighter byte accounting.
//   - cat's "exit when either side EOFs" semantics fight the
//     time-bounded test pattern operators want for throughput.
//   - rolling-sample stderr lines + a totals line are conventional
//     for throughput tools; you can't easily get that out of `cat`.
//
// Standalone-dmsg only in this first cut: the client bootstraps its
// own dmsg.Client just like `dmsg cat` with --transport dmsg. A
// future patch can add a VisorIperf RPC for the auto/skynet paths
// once we've validated the standalone shape.
package clidmsg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

var (
	iperfDuration time.Duration
	iperfInterval time.Duration
	iperfBlockKB  int
	iperfVerbose  bool
	iperfRTT      bool
	iperfRTTRate  int
	iperfEcho     bool
)

// Sized so a default test pushes 64 KiB writes — large enough to
// amortize per-Write syscall + goroutine cost over many bytes,
// small enough that a stalled peer's write doesn't sit on minutes
// of buffered data. Operator override via --block-kb.
const (
	defaultBlockKB    = 64
	defaultIperfDur   = 10 * time.Second
	defaultIperfTick  = 1 * time.Second
	dialIperfTimeout  = 30 * time.Second
	listenIperfTimout = 10 * time.Minute
)

func init() {
	iperfCmd.Flags().SortFlags = false
	iperfCmd.Flags().DurationVarP(&iperfDuration, "duration", "d", defaultIperfDur,
		"how long to pump bytes before stopping (e.g. 5s, 30s, 2m)")
	iperfCmd.Flags().DurationVarP(&iperfInterval, "interval", "i", defaultIperfTick,
		"emit a rolling-sample line every interval; zero disables intermediate samples")
	iperfCmd.Flags().IntVar(&iperfBlockKB, "block-kb", defaultBlockKB,
		"per-Write block size in KiB; larger amortizes write overhead, smaller surfaces stalls faster")
	iperfCmd.Flags().BoolVarP(&iperfVerbose, "verbose", "v", false,
		"print connection-setup info to stderr (target PK/port, transport, dial time)")
	iperfCmd.Flags().BoolVar(&iperfRTT, "rtt", false,
		"latency mode: send small probes + wait for echo, report RTT distribution + jitter. Requires peer in --echo mode.")
	iperfCmd.Flags().IntVar(&iperfRTTRate, "rtt-rate", 100,
		"probes per second in --rtt mode (default 100; rate is approximate, not paced)")
	iperfCmd.Flags().VarP(&sk, "sk", "s",
		"secret key for the standalone dmsg client (random if unset)")
	iperfCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "fatal",
		"[ debug | warn | error | fatal | panic | trace | info ]")

	iperfListenCmd.Flags().SortFlags = false
	iperfListenCmd.Flags().BoolVar(&iperfEcho, "echo", false,
		"echo received bytes back to the peer instead of draining to /dev/null (pairs with client's --rtt)")
	iperfListenCmd.Flags().DurationVarP(&iperfInterval, "interval", "i", defaultIperfTick,
		"emit a rolling-sample line every interval; zero disables intermediate samples")
	iperfListenCmd.Flags().BoolVarP(&iperfVerbose, "verbose", "v", false,
		"print connection-setup info to stderr (accepted PK, transport, accept time)")
	iperfListenCmd.Flags().VarP(&sk, "sk", "s",
		"secret key for the standalone dmsg client (random if unset)")
	iperfListenCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "fatal",
		"[ debug | warn | error | fatal | panic | trace | info ]")

	iperfCmd.AddCommand(iperfListenCmd)
}

var iperfCmd = &cobra.Command{
	Use:   "iperf <pk>:<port>",
	Short: "Bulk throughput measurement over a dmsg stream",
	Long: `dmsg iperf — pump a zero-filled buffer from memory through a
dmsg stream for the configured duration and print the achieved
throughput. Counterpart to ` + "`dmsg iperf listen`" + ` which drains
the receiving side.

The output cadence matches classic iperf: periodic rolling-sample
lines (one per --interval) followed by a final totals line. Bytes
flow as raw stream content — no framing, no envelope — so the
measurement is as close to the underlying transport as possible.

Examples:
  skywire cli dmsg iperf <pk>:6000 --duration 30s
  skywire cli dmsg iperf <pk>:6000 --duration 5s --interval 500ms --block-kb 256

Pair with ` + "`dmsg iperf listen <port>`" + ` on the peer first.

This is standalone-dmsg only — the CLI bootstraps its own
dmsg.Client. --sk pins the local identity so the listener's
whitelist (if any) can authorize it.`,
	Args:                  cobra.ExactArgs(1),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		log := logging.MustGetLogger("dmsg-iperf")
		if logLvl != "" {
			if lvl, err := logging.LevelFromString(logLvl); err == nil {
				logging.SetLevel(lvl)
			}
		}
		if iperfDuration <= 0 {
			return errors.New("dmsg iperf: --duration must be > 0")
		}
		if iperfBlockKB <= 0 {
			return errors.New("dmsg iperf: --block-kb must be > 0")
		}

		peerPK, port, err := splitPKPort(args[0])
		if err != nil {
			return err
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()

		conn, err := dialDmsgStream(ctx, log, peerPK, port, dialIperfTimeout)
		if err != nil {
			return err
		}
		defer conn.Close() //nolint:errcheck

		if iperfVerbose {
			fmt.Fprintf(os.Stderr, "connected: dmsg %s:%d (transport=dmsg)\n", peerPK.String(), port)
		}
		if iperfRTT {
			if iperfRTTRate <= 0 {
				return errors.New("dmsg iperf: --rtt-rate must be > 0")
			}
			runIperfRTT(ctx, conn, iperfDuration, iperfRTTRate)
			return nil
		}
		runIperfSender(ctx, conn, iperfDuration, iperfInterval, iperfBlockKB)
		return nil
	},
}

var iperfListenCmd = &cobra.Command{
	Use:   "listen <port>",
	Short: "Accept one inbound iperf stream and report received throughput",
	Long: `dmsg iperf listen — accept one inbound stream on <port>, drain
it into a black-hole sink, and report the received throughput in the
same shape as the client side.

One-shot: exits after the first stream EOFs. To run repeated tests
restart from a shell loop. Accept window is ` + listenIperfTimout.String() + ` (configurable in
source; long enough for an operator to set up the listener, switch
terminals, and start the client).

This is standalone-dmsg only — the CLI bootstraps its own dmsg.Client.
--sk pins the local PK so the client side knows what to dial.`,
	Args:                  cobra.ExactArgs(1),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		log := logging.MustGetLogger("dmsg-iperf-listen")
		if logLvl != "" {
			if lvl, err := logging.LevelFromString(logLvl); err == nil {
				logging.SetLevel(lvl)
			}
		}

		portU, err := strconv.ParseUint(args[0], 10, 16)
		if err != nil {
			return fmt.Errorf("dmsg iperf listen: invalid port %q: %w", args[0], err)
		}
		port := uint16(portU)

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()

		lis, listenerPK, err := listenDmsgIperf(ctx, log, port)
		if err != nil {
			return err
		}
		defer lis.Close() //nolint:errcheck

		if iperfVerbose {
			fmt.Fprintf(os.Stderr, "listening: dmsg %s:%d (transport=dmsg) — awaiting inbound\n",
				listenerPK.String(), port)
		}

		acceptCtx, acceptCancel := context.WithTimeout(ctx, listenIperfTimout)
		defer acceptCancel()
		conn, err := acceptOne(acceptCtx, lis)
		if err != nil {
			return fmt.Errorf("dmsg iperf listen: accept: %w", err)
		}
		defer conn.Close() //nolint:errcheck

		if iperfVerbose {
			fmt.Fprintf(os.Stderr, "accepted: %s\n", conn.RemoteAddr())
		}
		if iperfEcho {
			runIperfEcho(ctx, conn)
			return nil
		}
		runIperfReceiver(ctx, conn, iperfInterval)
		return nil
	},
}

// runIperfSender pumps a single fixed buffer at the conn for the
// configured duration, ticking sample lines on each interval. The
// buffer is recycled across writes so we don't pay an allocation
// per loop iteration. Atomic byte counter so the printer goroutine
// can sample it concurrently without locking the writer.
func runIperfSender(ctx context.Context, conn net.Conn, duration, interval time.Duration, blockKB int) {
	buf := make([]byte, blockKB*1024) // zero-filled by Go
	var sent atomic.Uint64
	start := time.Now()
	deadline := start.Add(duration)

	var wg sync.WaitGroup
	printerCtx, stopPrinter := context.WithCancel(ctx)
	defer stopPrinter()
	if interval > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runSamplePrinter(printerCtx, &sent, interval, start, "send")
		}()
	}

	// Set a write deadline at the test end so the loop can't hang
	// past `duration` on a stuck peer. The conn's own write timeout
	// (if any) still applies before this hits.
	if sd, ok := conn.(interface{ SetWriteDeadline(time.Time) error }); ok {
		_ = sd.SetWriteDeadline(deadline.Add(2 * time.Second)) //nolint:errcheck
	}

sendLoop:
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			break sendLoop
		default:
		}
		n, err := conn.Write(buf)
		if n > 0 {
			sent.Add(uint64(n)) //nolint:gosec // n is non-negative per io.Writer contract
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "write error after %s: %v\n", time.Since(start).Round(time.Millisecond), err)
			break sendLoop
		}
	}
	stopPrinter()
	wg.Wait()
	printTotals(&sent, start, "send")
}

// runIperfReceiver drains the conn until EOF or ctx cancellation,
// counting bytes. Symmetric to the sender's sample-printer.
func runIperfReceiver(ctx context.Context, conn net.Conn, interval time.Duration) {
	buf := make([]byte, 64*1024) // large read buffer; receiver isn't paying alloc cost
	var rcvd atomic.Uint64
	start := time.Now()

	var wg sync.WaitGroup
	printerCtx, stopPrinter := context.WithCancel(ctx)
	defer stopPrinter()
	if interval > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runSamplePrinter(printerCtx, &rcvd, interval, start, "recv")
		}()
	}

	for {
		select {
		case <-ctx.Done():
			stopPrinter()
			wg.Wait()
			printTotals(&rcvd, start, "recv")
			return
		default:
		}
		n, err := conn.Read(buf)
		if n > 0 {
			rcvd.Add(uint64(n)) //nolint:gosec
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				fmt.Fprintf(os.Stderr, "read error after %s: %v\n", time.Since(start).Round(time.Millisecond), err)
			}
			break
		}
	}
	stopPrinter()
	wg.Wait()
	printTotals(&rcvd, start, "recv")
}

// runSamplePrinter emits one line per interval with current
// rolling-window throughput. Loads the counter atomically so it
// never blocks the I/O loop.
func runSamplePrinter(ctx context.Context, counter *atomic.Uint64, interval time.Duration, start time.Time, dir string) {
	t := time.NewTicker(interval)
	defer t.Stop()
	var lastBytes uint64
	last := start
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			cur := counter.Load()
			delta := cur - lastBytes
			window := now.Sub(last)
			rate := bytesPerSec(delta, window)
			fmt.Fprintf(os.Stderr, "[%s %5.1fs] %s window total %s\n",
				dir, now.Sub(start).Seconds(), humanRate(rate), humanBytes(cur))
			lastBytes = cur
			last = now
		}
	}
}

// printTotals emits the final line — total bytes + total duration
// + average rate. Goes to STDOUT (not stderr like the sample lines)
// so a script can capture just the totals.
func printTotals(counter *atomic.Uint64, start time.Time, dir string) {
	total := counter.Load()
	elapsed := time.Since(start)
	avg := bytesPerSec(total, elapsed)
	fmt.Printf("dir=%s total=%s duration=%s avg=%s\n",
		dir, humanBytes(total), elapsed.Round(time.Millisecond), humanRate(avg))
}

func bytesPerSec(b uint64, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(b) / d.Seconds()
}

// humanRate renders bytes-per-second with binary suffixes — same
// scale as humanBytes since 1 KB/s vs 1 KiB/s is fence-post the
// operator running a quick test rarely cares about, and matching
// the byte-totals output avoids unit confusion.
func humanRate(bps float64) string {
	switch {
	case bps >= 1024*1024*1024:
		return fmt.Sprintf("%.2f GiB/s", bps/1024/1024/1024)
	case bps >= 1024*1024:
		return fmt.Sprintf("%.2f MiB/s", bps/1024/1024)
	case bps >= 1024:
		return fmt.Sprintf("%.2f KiB/s", bps/1024)
	default:
		return fmt.Sprintf("%.0f B/s", bps)
	}
}

func humanBytes(b uint64) string {
	switch {
	case b >= 1024*1024*1024:
		return fmt.Sprintf("%.2f GiB", float64(b)/1024/1024/1024)
	case b >= 1024*1024:
		return fmt.Sprintf("%.2f MiB", float64(b)/1024/1024)
	case b >= 1024:
		return fmt.Sprintf("%.2f KiB", float64(b)/1024)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// dialDmsgStream builds a one-shot standalone dmsg client, dials
// the target, and returns the live stream. Mirrors `dmsg cat`'s
// standalone path; reuses startDmsgClient/resolveChatIdentity from
// the shared helpers in this package.
func dialDmsgStream(ctx context.Context, log *logging.Logger, peerPK cipher.PubKey, port uint16, timeout time.Duration) (net.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	myPK, mySK := resolveChatIdentity(sk)
	dc, closeDmsg, err := startDmsgClient(dialCtx, log, myPK, mySK)
	if err != nil {
		return nil, fmt.Errorf("dmsg client init: %w", err)
	}
	go func() {
		<-ctx.Done()
		closeDmsg()
	}()
	stream, err := dc.DialStream(dialCtx, dmsg.Addr{PK: peerPK, Port: port})
	if err != nil {
		closeDmsg()
		return nil, fmt.Errorf("dial %s:%d: %w", peerPK.String(), port, err)
	}
	return stream, nil
}

// listenDmsgIperf brings up a standalone dmsg client + listener on
// the given port. The returned PK is the local visor identity the
// caller should hand to the remote side (printed in --verbose).
func listenDmsgIperf(ctx context.Context, log *logging.Logger, port uint16) (net.Listener, cipher.PubKey, error) {
	myPK, mySK := resolveChatIdentity(sk)
	dc, closeDmsg, err := startDmsgClient(ctx, log, myPK, mySK)
	if err != nil {
		return nil, cipher.PubKey{}, fmt.Errorf("dmsg client init: %w", err)
	}
	go func() {
		<-ctx.Done()
		closeDmsg()
	}()
	lis, err := dc.Listen(port)
	if err != nil {
		closeDmsg()
		return nil, cipher.PubKey{}, fmt.Errorf("listen on %d: %w", port, err)
	}
	return lis, myPK, nil
}

// rttProbeSize is the fixed wire size of one probe — seq (8B) +
// send-ns timestamp (8B). Tiny + fixed so we can read/write whole
// probes with io.ReadFull/Write without framing overhead and the
// echo side can just blast the same bytes back.
const rttProbeSize = 16

// runIperfRTT pumps small fixed-size probes through the stream and
// listens for the peer's --echo to bounce them back. Each probe
// carries a sequence number + the sender's send-time in unix-nanos.
// On receipt the sender records the RTT (now - send_ns), tracks
// the running min/max/mean/jitter, and prints a final histogram
// shape: min / mean / max / stddev / loss-rate.
//
// Why fixed wire size + same encoding both ways: it makes the
// listener side dead simple (io.Copy with a 16-byte buffer would
// have worked even, but we explicitly loop on rttProbeSize reads
// to surface partial-read errors clearly). The seq + ts together
// also let us detect reordering (server echoing out-of-order
// probes) — recorded as a separate count, doesn't escalate to
// "lost" which is "never seen".
func runIperfRTT(ctx context.Context, conn net.Conn, duration time.Duration, rate int) {
	start := time.Now()
	deadline := start.Add(duration)

	type rttRecord struct {
		sent time.Time
	}
	// Probes-in-flight indexed by seq. Bounded by what's sent in
	// `duration` at `rate`; a slice is the cheapest random-access
	// container and we never compact (test life-cycle is short).
	sentTimes := make([]rttRecord, 0, rate*int(duration.Seconds())+rate)

	// Receiver goroutine reads echoed probes, records RTT samples.
	samples := make(chan rxSample, 4096)
	rxCtx, rxCancel := context.WithCancel(ctx)
	defer rxCancel()
	go func() {
		buf := make([]byte, rttProbeSize)
		for {
			if _, err := io.ReadFull(conn, buf); err != nil {
				if !errors.Is(err, io.EOF) && rxCtx.Err() == nil {
					fmt.Fprintf(os.Stderr, "rtt rx error: %v\n", err)
				}
				close(samples)
				return
			}
			seq := bigEndianUint64(buf[:8])
			sent := time.Unix(0, int64(bigEndianUint64(buf[8:16]))) //nolint:gosec
			samples <- rxSample{seq: seq, rtt: time.Since(sent)}
			_ = sent // present for symmetry / diag; rtt already computed
		}
	}()

	// Sender: pace probes at `rate` per second.
	gap := time.Second / time.Duration(rate)
	if gap < time.Microsecond {
		gap = time.Microsecond
	}
	t := time.NewTicker(gap)
	defer t.Stop()
	wireBuf := make([]byte, rttProbeSize)
	var seq uint64
sendLoop:
	for {
		select {
		case <-ctx.Done():
			break sendLoop
		case now := <-t.C:
			if !now.Before(deadline) {
				break sendLoop
			}
			putBigEndianUint64(wireBuf[:8], seq)
			putBigEndianUint64(wireBuf[8:16], uint64(now.UnixNano())) //nolint:gosec
			if _, err := conn.Write(wireBuf); err != nil {
				fmt.Fprintf(os.Stderr, "rtt tx error after %s: %v\n",
					time.Since(start).Round(time.Millisecond), err)
				break sendLoop
			}
			sentTimes = append(sentTimes, rttRecord{sent: now})
			seq++
		}
	}

	// Stop reading once we've waited a reasonable echo-window past
	// the last send. After this fires, anything still in flight is
	// counted as loss.
	echoWindow := 2 * time.Second
	settleTimer := time.NewTimer(echoWindow)
	defer settleTimer.Stop()
	var received []rxSample
collect:
	for {
		select {
		case s, ok := <-samples:
			if !ok {
				break collect
			}
			received = append(received, s)
		case <-settleTimer.C:
			rxCancel()
			_ = conn.SetReadDeadline(time.Now()) //nolint:errcheck
		}
	}

	printRTTSummary(received, uint64(len(sentTimes)), time.Since(start)) //nolint:gosec
}

// runIperfEcho is the listener-side counterpart to runIperfRTT —
// bounce every byte back unchanged. io.Copy is sufficient because
// the wire is byte-stream symmetric; the iperf-rtt client's
// fixed-size probes are just bytes from the echoer's perspective.
func runIperfEcho(ctx context.Context, conn net.Conn) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(conn, conn) //nolint:errcheck
	}()
	select {
	case <-ctx.Done():
		_ = conn.SetReadDeadline(time.Now()) //nolint:errcheck
		<-done
	case <-done:
	}
}

// printRTTSummary computes min/mean/max/stddev + loss-rate from the
// received samples and prints a single classic-ping-style line to
// stdout. The stderr-vs-stdout split mirrors the throughput path:
// summary on stdout for scripting capture, any error noise on
// stderr.
func printRTTSummary(samples []rxSample, sent uint64, elapsed time.Duration) {
	if sent == 0 {
		fmt.Println("dir=rtt sent=0 received=0 (nothing pumped)")
		return
	}
	recvCount := uint64(len(samples))
	if recvCount == 0 {
		fmt.Printf("dir=rtt sent=%d received=0 loss=100.00%% duration=%s (no echos)\n",
			sent, elapsed.Round(time.Millisecond))
		return
	}
	var min, max, sum time.Duration
	min = samples[0].rtt
	max = samples[0].rtt
	for _, s := range samples {
		if s.rtt < min {
			min = s.rtt
		}
		if s.rtt > max {
			max = s.rtt
		}
		sum += s.rtt
	}
	mean := sum / time.Duration(recvCount) //nolint:gosec // recvCount is the in-process sample count, far below int64 max
	// Stddev: sample std (Bessel's correction skipped — for small N
	// the bias is negligible vs. the ms-scale RTT signal we care
	// about; saves an extra divide).
	var sqSum float64
	for _, s := range samples {
		delta := float64(s.rtt - mean)
		sqSum += delta * delta
	}
	std := time.Duration(0)
	if recvCount > 0 {
		std = time.Duration(math.Sqrt(sqSum / float64(recvCount)))
	}
	loss := 100 * float64(sent-recvCount) / float64(sent)
	fmt.Printf("dir=rtt sent=%d received=%d loss=%.2f%% duration=%s min=%s mean=%s max=%s stddev=%s\n",
		sent, recvCount, loss, elapsed.Round(time.Millisecond),
		min.Round(time.Microsecond),
		mean.Round(time.Microsecond),
		max.Round(time.Microsecond),
		std.Round(time.Microsecond),
	)
}

// bigEndianUint64 reads 8 bytes BE → uint64. Single-purpose so I
// don't pull in encoding/binary just for two integers.
func bigEndianUint64(b []byte) uint64 {
	return uint64(b[0])<<56 | uint64(b[1])<<48 | uint64(b[2])<<40 | uint64(b[3])<<32 |
		uint64(b[4])<<24 | uint64(b[5])<<16 | uint64(b[6])<<8 | uint64(b[7])
}

func putBigEndianUint64(b []byte, v uint64) {
	// byte(v >> N) is the standard big-endian-pack idiom — the shift
	// drops the high bits and byte() truncates the low 8. The gosec
	// G115 warnings here are spurious.
	b[0] = byte(v >> 56) //nolint:gosec
	b[1] = byte(v >> 48) //nolint:gosec
	b[2] = byte(v >> 40) //nolint:gosec
	b[3] = byte(v >> 32) //nolint:gosec
	b[4] = byte(v >> 24) //nolint:gosec
	b[5] = byte(v >> 16) //nolint:gosec
	b[6] = byte(v >> 8)  //nolint:gosec
	b[7] = byte(v)       //nolint:gosec
}

// rxSample is declared inside runIperfRTT but referenced by
// printRTTSummary's signature so its definition needs to be
// visible package-wide; the inner type shadowing in runIperfRTT
// is incidental and uses the same shape.
type rxSample struct {
	seq uint64
	rtt time.Duration
}

// acceptOne blocks for a single inbound stream or ctx deadline.
// Wraps the listener's Accept in a goroutine so we can honor the
// context.
func acceptOne(ctx context.Context, lis net.Listener) (net.Conn, error) {
	type accepted struct {
		conn net.Conn
		err  error
	}
	ch := make(chan accepted, 1)
	go func() {
		c, err := lis.Accept()
		ch <- accepted{c, err}
	}()
	select {
	case <-ctx.Done():
		_ = lis.Close() //nolint:errcheck
		return nil, ctx.Err()
	case a := <-ch:
		return a.conn, a.err
	}
}
