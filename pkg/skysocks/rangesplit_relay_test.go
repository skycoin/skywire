package skysocks

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
)

// rsStuckOrigin is an origin that ignores Range, answers every request with the
// canned reply, and then NEVER closes. Behind rsGatedExit that is an exit which
// never sends FIN — what every exit before #5123 did on origin EOF — so a relay
// that waits for the stream to end instead of the response never finishes.
func rsStuckOrigin(t *testing.T, reply string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() }) //nolint:errcheck
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close() //nolint:errcheck
				br := bufio.NewReader(c)
				for {
					line, err := br.ReadString('\n')
					if err != nil {
						return
					}
					if line == "\r\n" {
						break
					}
				}
				if _, err := io.WriteString(c, reply); err != nil {
					return
				}
				_, _ = io.Copy(io.Discard, br) //nolint:errcheck // hold until the exit lets go
			}(c)
		}
	}()
	return ln.Addr().String()
}

// rsRelayed sends one GET through the splitter to origin and returns the raw
// bytes the browser received before its conn was closed, and how long that took.
func rsRelayed(t *testing.T, origin string, within time.Duration) ([]byte, time.Duration) {
	t.Helper()
	proxy := newRSGatedClient(t, origin, nil, false)
	c := rsBrowserConnect(t, proxy)
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	if _, err := readSocks5Reply(c); err != nil {
		t.Fatalf("connect reply: %v", err)
	}
	start := time.Now()
	if _, err := fmt.Fprintf(c, "GET /x HTTP/1.1\r\nHost: example.com\r\n\r\n"); err != nil {
		t.Fatalf("get: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(within)) //nolint:errcheck
	got, err := io.ReadAll(c)
	if err != nil {
		t.Fatalf("the browser conn must be closed once the response ends (got %d bytes): %v", len(got), err)
	}
	return got, time.Since(start)
}

func TestRangeSplitRelayEndsAtContentLength(t *testing.T) {
	body := strings.Repeat("0123456789", 100<<10)
	reply := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
	got, _ := rsRelayed(t, rsStuckOrigin(t, reply), 5*time.Second)
	if string(got) != reply {
		t.Fatalf("relayed %d bytes, want the reply's %d byte for byte", len(got), len(reply))
	}
}

func TestRangeSplitRelayEndsAtChunkedTerminator(t *testing.T) {
	reply := "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nTrailer: X-Sum\r\n\r\n" +
		"5\r\nhello\r\n" + "7\r\n, world\r\n" + "0\r\nX-Sum: 12\r\n\r\n"
	got, _ := rsRelayed(t, rsStuckOrigin(t, reply), 5*time.Second)
	if string(got) != reply {
		t.Fatalf("relayed %q, want %q", got, reply)
	}
}

// A reply with no length ends only at EOF, which this exit never sends: the relay
// still delivers it and is ended by chunk.idle_timeout rather than held forever.
func TestRangeSplitRelayWithoutLengthIsIdleBounded(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	skysettings.Apply(map[string]int64{skysettings.ChunkIdleTimeout: int64(300 * time.Millisecond)})
	reply := "HTTP/1.1 200 OK\r\nConnection: close\r\n\r\nread until close"
	got, took := rsRelayed(t, rsStuckOrigin(t, reply), 5*time.Second)
	if string(got) != reply {
		t.Fatalf("relayed %q, want %q", got, reply)
	}
	if took < 300*time.Millisecond {
		t.Fatalf("closed after %v, before the idle deadline", took)
	}
}

// A browser whose GET misses range.classify_timeout (but lands inside the second
// window a refusal grants it) still gets a 502 when the
// exit refuses, not a bare close with no bytes.
func TestRangeSplitLateHeadStillGets502(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	skysettings.Apply(map[string]int64{skysettings.RangeClassifyTimeout: int64(400 * time.Millisecond)})
	proxy := newRSGatedClient(t, "127.0.0.1:1", nil, true)
	c := rsBrowserConnect(t, proxy)
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	if _, err := readSocks5Reply(c); err != nil {
		t.Fatalf("connect reply: %v", err)
	}
	time.Sleep(600 * time.Millisecond)
	if _, err := fmt.Fprintf(c, "GET /x HTTP/1.1\r\nHost: example.com\r\n\r\n"); err != nil {
		t.Fatalf("get: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("a late GET on a refused CONNECT must be answered, not dropped: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
}

// A late GET on an accepted CONNECT is spliced, and its bytes reach the origin.
func TestRangeSplitLateHeadIsSplicedIntact(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	skysettings.Apply(map[string]int64{skysettings.RangeClassifyTimeout: int64(400 * time.Millisecond)})
	reply := "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"
	proxy := newRSGatedClient(t, rsStuckOrigin(t, reply), nil, false)
	c := rsBrowserConnect(t, proxy)
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	if _, err := readSocks5Reply(c); err != nil {
		t.Fatalf("connect reply: %v", err)
	}
	time.Sleep(600 * time.Millisecond)
	if _, err := fmt.Fprintf(c, "GET /x HTTP/1.1\r\nHost: example.com\r\n\r\n"); err != nil {
		t.Fatalf("get: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	b, err := io.ReadAll(resp.Body)
	if err != nil || string(b) != "ok" {
		t.Fatalf("body %q err %v, want \"ok\"", b, err)
	}
}
