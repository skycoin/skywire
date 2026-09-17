// Package skysocks first-stream (browser-facing) exit-handshake pipelining tests.
package skysocks

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// socks5ConnectRequest builds the CONNECT the browser sends for a domain target,
// byte-for-byte as sniffSOCKS5Status buffers and replays it.
func socks5ConnectRequest(host string, port uint16) []byte {
	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))} //nolint:gosec // test hosts are short
	req = append(req, host...)
	return append(req, byte(port>>8), byte(port)) //nolint:gosec
}

// TestSniff_FirstStreamHandshakeIsPipelined is the measured prelude cost: a
// range-split download paid ~1.1 s before its first parallel chunk fetch began,
// three serial exit round trips on a 150 ms leg — the method reply, the CONNECT
// reply, and the 206. The first of those is free: the browser's CONNECT is
// already buffered when the exit is opened, so it can ride in the same write as
// the greeting, exactly as a chunk fetch does (exitConnectPipelined).
//
// The fake exit here is scripted like rangesplit_pipeline_test.go's: it reads the
// WHOLE greeting+CONNECT block before writing a single reply byte. On unmodified
// develop the client writes the greeting alone and then blocks on the method
// reply, so the read below starves and fails on its deadline.
func TestSniff_FirstStreamHandshakeIsPipelined(t *testing.T) {
	c, _ := newTestClient(t)
	defer c.Close() //nolint:errcheck
	browser, exit, res := runSniff(t, c)
	defer browser.Close() //nolint:errcheck
	defer exit.Close()    //nolint:errcheck

	greeting := []byte{0x05, 0x01, 0x00}
	req := socks5ConnectRequest("example.com", 80)

	_, err := browser.Write(greeting)
	require.NoError(t, err)
	method := make([]byte, 2)
	_, err = io.ReadFull(browser, method)
	require.NoError(t, err)
	require.Equal(t, []byte{0x05, 0x00}, method, "method selection is still answered locally")
	_, err = browser.Write(req)
	require.NoError(t, err)

	// Nothing has been written to the exit yet: it must now receive the greeting
	// AND the CONNECT without having answered anything.
	want := append(append([]byte{}, greeting...), req...)
	got := make([]byte, len(want))
	require.NoError(t, exit.SetReadDeadline(time.Now().Add(3*time.Second)))
	_, err = io.ReadFull(exit, got)
	require.NoError(t, err, "the exit handshake blocked on the method reply before sending CONNECT — not pipelined")
	require.Equal(t, want, got, "the exit must see the same bytes in the same order")
	require.NoError(t, exit.SetReadDeadline(time.Time{}))

	// Only now the method reply, as the exit's SOCKS5 server emits it.
	_, err = exit.Write([]byte{0x05, 0x00})
	require.NoError(t, err)

	select {
	case proceed := <-res:
		require.True(t, proceed, "a non-status target proceeds to the splice")
	case <-time.After(3 * time.Second):
		t.Fatal("sniff did not return after the pipelined handshake")
	}
}

// TestFirstStream_RejectedConnectStillReachesTheBrowser pins the semantics the
// pipelining must NOT change: the exit's CONNECT reply is relayed byte-for-byte,
// so a refused target surfaces to the browser as the same SOCKS5 error as before
// (:443 with the TLS split off is the plain splice path).
//
// Unlike the test above this one is written to pass either way — the exit drains
// whatever arrives and answers in its own order — so it is evidence that the
// error path is untouched, not that the handshake got faster.
func TestFirstStream_RejectedConnectStillReachesTheBrowser(t *testing.T) {
	c, _ := newTestClient(t)
	defer c.Close() //nolint:errcheck

	connSniff, browser := net.Pipe()
	streamSniff, exit := net.Pipe()
	defer browser.Close() //nolint:errcheck
	defer exit.Close()    //nolint:errcheck
	go c.handleStream(connSniff, streamSniff)

	// Drain the exit side continuously: whether the client writes the greeting and
	// the CONNECT together or one after the other, the bytes are simply consumed.
	var mu sync.Mutex
	var seenBytes int
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := exit.Read(buf)
			mu.Lock()
			seenBytes += n
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	awaitExit := func(n int, what string) {
		require.Eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return seenBytes >= n
		}, 3*time.Second, 5*time.Millisecond, what)
	}

	greeting := []byte{0x05, 0x01, 0x00}
	req := socks5ConnectRequest("refused.example", 443)
	_, err := browser.Write(greeting)
	require.NoError(t, err)
	_, err = io.ReadFull(browser, make([]byte, 2))
	require.NoError(t, err)
	_, err = browser.Write(req)
	require.NoError(t, err)

	awaitExit(len(greeting), "the exit never received the greeting")
	_, err = exit.Write([]byte{0x05, 0x00})
	require.NoError(t, err)
	awaitExit(len(greeting)+len(req), "the exit never received the CONNECT")

	// Refusal (REP=0x05, connection refused) — the browser must see it verbatim.
	refusal := []byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
	_, err = exit.Write(refusal)
	require.NoError(t, err)

	seen := make([]byte, len(refusal))
	require.NoError(t, browser.SetReadDeadline(time.Now().Add(3*time.Second)))
	_, err = io.ReadFull(browser, seen)
	require.NoError(t, err, "the browser must still receive the exit's CONNECT reply")
	require.Equal(t, refusal, seen, "a refused CONNECT reaches the browser unchanged")
}
