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
	iperfCmd.Flags().VarP(&sk, "sk", "s",
		"secret key for the standalone dmsg client (random if unset)")
	iperfCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "fatal",
		"[ debug | warn | error | fatal | panic | trace | info ]")

	iperfListenCmd.Flags().SortFlags = false
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

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			break
		default:
		}
		n, err := conn.Write(buf)
		if n > 0 {
			sent.Add(uint64(n)) //nolint:gosec // n is non-negative per io.Writer contract
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "write error after %s: %v\n", time.Since(start).Round(time.Millisecond), err)
			break
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
