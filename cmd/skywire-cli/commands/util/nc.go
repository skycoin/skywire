// Package cliutil cmd/skywire-cli/commands/util/nc.go: `skywire cli
// util nc` — a pure-Go netcat clone. Plain TCP / UDP over the
// kernel's stack; nothing in here touches a skywire transport. Lives
// under `util` for the same reason `serve` does: it's a developer
// convenience, not part of the visor RPC surface.
//
// Supported flags follow the classic OpenBSD-nc letter set so muscle
// memory transfers:
//
//	-l   listen mode (default is dial)
//	-u   UDP (default TCP)
//	-w   timeout for dial / read-stall
//	-k   keep listening after the first conn EOFs (TCP listen only)
//	-z   zero-IO probe — TCP dial, exit 0/1 by success
//	-v   verbose status to stderr
//
// Intentionally absent: `-e/--exec`. Cross-host command execution is
// dmsgpty's job; nc's `-e` is a well-known footgun and we'd rather
// users compose with shell pipes.
package cliutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/spf13/cobra"
)

var (
	ncListen        bool
	ncUDP           bool
	ncTimeout       time.Duration
	ncKeepListening bool
	ncZeroIO        bool
	ncVerbose       bool
)

func init() {
	ncCmd.Flags().SortFlags = false
	ncCmd.Flags().BoolVarP(&ncListen, "listen", "l", false, "listen for an inbound connection")
	ncCmd.Flags().BoolVarP(&ncUDP, "udp", "u", false, "UDP mode (default TCP)")
	ncCmd.Flags().DurationVarP(&ncTimeout, "timeout", "w", 0,
		"dial / read-stall timeout (e.g. 5s, 1m). Zero = no timeout.")
	ncCmd.Flags().BoolVarP(&ncKeepListening, "keep-listening", "k", false,
		"keep listening after the current connection EOFs (TCP listen only; one-conn-at-a-time)")
	ncCmd.Flags().BoolVarP(&ncZeroIO, "zero-io", "z", false,
		"zero-IO probe — TCP dial then close, exit 0 on success, 1 on failure")
	ncCmd.Flags().BoolVarP(&ncVerbose, "verbose", "v", false, "print status to stderr")
	RootCmd.AddCommand(ncCmd)
}

var ncCmd = &cobra.Command{
	Use:   "nc [flags] [host] [port]",
	Short: "Pure-Go netcat (plain TCP/UDP, no skywire transport)",
	Long: `nc — pure-Go netcat clone for plain TCP/UDP. Useful for
quick reachability checks, listening for a single inbound
connection, or piping bytes between two hosts.

Modes (mutually exclusive):
  - dial  (default)     nc <host> <port>
  - listen (-l)         nc -l <port>             (binds on all addrs)
                        nc -l <host> <port>      (binds on host)
  - probe (-z)          nc -z <host> <port>      (TCP only)

Examples:
  echo hi | skywire cli util nc 127.0.0.1 9000           # dial + send
  skywire cli util nc -l 9000 > out.bin                  # one-shot listen
  skywire cli util nc -lk 9000 > log.bin                 # keep listening
  skywire cli util nc -u 10.0.0.1 514 < syslog.txt       # UDP send
  skywire cli util nc -zw 1s 10.0.0.1 22                 # TCP probe

This is a pure-stdlib implementation — no skywire transports are
involved. For dmsg / skynet streams use ` + "`skywire cli dmsg cat`" + `.`,
	Args:                  cobra.RangeArgs(0, 2),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	RunE: func(_ *cobra.Command, args []string) error {
		host, port, err := parseNCArgs(args, ncListen)
		if err != nil {
			return err
		}

		switch {
		case ncZeroIO:
			if ncUDP {
				return errors.New("nc: -z requires TCP (drop -u)")
			}
			if ncListen {
				return errors.New("nc: -z and -l are mutually exclusive")
			}
			return ncProbeTCP(host, port)
		case ncListen && ncUDP:
			return ncListenUDP(host, port)
		case ncListen:
			return ncListenTCP(host, port)
		case ncUDP:
			return ncDialUDP(host, port)
		default:
			return ncDialTCP(host, port)
		}
	},
}

// parseNCArgs returns (host, port). For listen mode a single arg is
// treated as the port (bind on all addresses); two args are
// host+port. Dial mode always needs host+port.
func parseNCArgs(args []string, listen bool) (string, string, error) {
	switch len(args) {
	case 1:
		if !listen {
			return "", "", errors.New("nc: dial mode needs <host> <port>")
		}
		if _, err := strconv.ParseUint(args[0], 10, 16); err != nil {
			return "", "", fmt.Errorf("nc: bad port %q: %w", args[0], err)
		}
		return "", args[0], nil
	case 2:
		if _, err := strconv.ParseUint(args[1], 10, 16); err != nil {
			return "", "", fmt.Errorf("nc: bad port %q: %w", args[1], err)
		}
		return args[0], args[1], nil
	default:
		return "", "", errors.New("nc: expected [host] <port>")
	}
}

// ncDialTCP opens a TCP connection and splices stdio.
func ncDialTCP(host, port string) error {
	addr := net.JoinHostPort(host, port)
	d := net.Dialer{Timeout: ncTimeout}
	conn, err := d.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("nc: dial %s: %w", addr, err)
	}
	defer conn.Close() //nolint:errcheck
	if ncVerbose {
		fmt.Fprintf(os.Stderr, "Connection to %s %s tcp succeeded!\n", host, port)
	}
	return ncSpliceStdio(conn)
}

// ncDialUDP "connects" a UDP socket (sets default destination) and
// splices stdio. UDP has no connect handshake, so the verbose line
// is best-effort — a wrong port silently drops.
func ncDialUDP(host, port string) error {
	addr := net.JoinHostPort(host, port)
	d := net.Dialer{Timeout: ncTimeout}
	conn, err := d.Dial("udp", addr)
	if err != nil {
		return fmt.Errorf("nc: dial udp %s: %w", addr, err)
	}
	defer conn.Close() //nolint:errcheck
	if ncVerbose {
		fmt.Fprintf(os.Stderr, "Connection to %s %s udp succeeded!\n", host, port)
	}
	return ncSpliceStdio(conn)
}

// ncListenTCP binds and accepts. One-conn-at-a-time even with -k —
// multi-conn into the same stdout would interleave bytes.
func ncListenTCP(host, port string) error {
	addr := net.JoinHostPort(host, port)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("nc: listen tcp %s: %w", addr, err)
	}
	defer lis.Close() //nolint:errcheck
	if ncVerbose {
		fmt.Fprintf(os.Stderr, "Listening on %s\n", lis.Addr())
	}
	for {
		conn, err := lis.Accept()
		if err != nil {
			return fmt.Errorf("nc: accept: %w", err)
		}
		if ncVerbose {
			fmt.Fprintf(os.Stderr, "Connection received from %s\n", conn.RemoteAddr())
		}
		if err := ncSpliceStdio(conn); err != nil {
			_ = conn.Close() //nolint:errcheck
			if !ncKeepListening {
				return err
			}
			if ncVerbose {
				fmt.Fprintf(os.Stderr, "nc: conn error (continuing): %v\n", err)
			}
			continue
		}
		_ = conn.Close() //nolint:errcheck
		if !ncKeepListening {
			return nil
		}
	}
}

// ncListenUDP binds a UDP socket and forwards datagrams to stdout.
// stdin is sent back to whoever last sent us a datagram — same
// behavior as OpenBSD nc.
func ncListenUDP(host, port string) error {
	addr := net.JoinHostPort(host, port)
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("nc: listen udp %s: %w", addr, err)
	}
	defer pc.Close() //nolint:errcheck
	if ncVerbose {
		fmt.Fprintf(os.Stderr, "Listening on %s (udp)\n", pc.LocalAddr())
	}

	var (
		mu       sync.Mutex
		peerAddr net.Addr
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// stdin → most-recent peer.
	go func() {
		buf := make([]byte, 64*1024)
		for {
			n, rErr := os.Stdin.Read(buf)
			if n > 0 {
				mu.Lock()
				dst := peerAddr
				mu.Unlock()
				if dst != nil {
					if _, wErr := pc.WriteTo(buf[:n], dst); wErr != nil {
						return
					}
				}
				// Otherwise drop — no peer to send to yet. Matches nc.
			}
			if rErr != nil {
				cancel()
				return
			}
		}
	}()

	buf := make([]byte, 64*1024)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if ncTimeout > 0 {
			_ = pc.SetReadDeadline(time.Now().Add(ncTimeout)) //nolint:errcheck
		}
		n, raddr, err := pc.ReadFrom(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() && !ncKeepListening {
				return fmt.Errorf("nc: read timeout")
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("nc: udp read: %w", err)
		}
		mu.Lock()
		peerAddr = raddr
		mu.Unlock()
		if _, err := os.Stdout.Write(buf[:n]); err != nil {
			return err
		}
		if !ncKeepListening {
			// Drain stdin → peer in the goroutine; give it a moment
			// to flush, then return. Without -k, single-datagram is
			// the simple expected behavior.
			//
			// For interactive use callers want -k anyway.
			return nil
		}
	}
}

// ncProbeTCP is the -z TCP probe: dial, close, exit by status.
func ncProbeTCP(host, port string) error {
	addr := net.JoinHostPort(host, port)
	d := net.Dialer{Timeout: ncTimeout}
	conn, err := d.Dial("tcp", addr)
	if err != nil {
		if ncVerbose {
			fmt.Fprintf(os.Stderr, "nc: connect to %s port %s (tcp) failed: %v\n", host, port, err)
		}
		// Match nc's exit-1 convention without surfacing a Go error
		// trace to stderr.
		os.Exit(1)
	}
	_ = conn.Close() //nolint:errcheck
	if ncVerbose {
		fmt.Fprintf(os.Stderr, "Connection to %s %s port [tcp] succeeded!\n", host, port)
	}
	return nil
}

// ncSpliceStdio runs bidirectional io.Copy between conn and stdio.
// Returns when either side EOFs or errors; closes the conn on first
// EOF so the peer sees our close.
func ncSpliceStdio(conn net.Conn) error {
	var (
		once    sync.Once
		closeFn = func() { _ = conn.Close() } //nolint:errcheck
	)
	done := make(chan error, 2)
	go func() {
		_, err := io.Copy(conn, os.Stdin)
		once.Do(closeFn)
		done <- err
	}()
	go func() {
		_, err := io.Copy(os.Stdout, conn)
		once.Do(closeFn)
		done <- err
	}()
	first := <-done
	<-done
	if first == nil || errors.Is(first, io.EOF) || errors.Is(first, net.ErrClosed) {
		return nil
	}
	return first
}
