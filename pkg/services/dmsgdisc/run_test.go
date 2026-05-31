// Package dmsgdisc run_test.go: covers Run's http-mode startup path by
// pointing openStore at a tiny in-process fake that speaks just enough of the
// redis wire protocol (RESP2) for go-redis's connection PING to succeed. This
// lets Run get past openStore without a real redis and exercise the
// API-build / mode-resolve / listener-launch path before ctx cancels it.
package dmsgdisc

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeRedis accepts connections and answers each RESP command: PING -> +PONG,
// HELLO -> error (forces the client to RESP2), everything else -> +OK. That's
// all go-redis needs to consider the connection healthy.
func fakeRedis(t *testing.T) (addr string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go serveFakeRedis(conn)
		}
	}()
	return ln.Addr().String()
}

func serveFakeRedis(conn net.Conn) {
	defer conn.Close() //nolint:errcheck
	r := bufio.NewReader(conn)
	for {
		cmd, err := readRESPCommand(r)
		if err != nil {
			return
		}
		if len(cmd) == 0 {
			continue
		}
		switch strings.ToUpper(cmd[0]) {
		case "PING":
			_, _ = conn.Write([]byte("+PONG\r\n"))
		case "HELLO":
			_, _ = conn.Write([]byte("-ERR unknown command 'HELLO'\r\n"))
		default:
			_, _ = conn.Write([]byte("+OK\r\n"))
		}
	}
}

// readRESPCommand reads one RESP array-of-bulk-strings request.
func readRESPCommand(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" || line[0] != '*' {
		return nil, nil
	}
	n, err := strconv.Atoi(line[1:])
	if err != nil || n < 0 {
		return nil, fmt.Errorf("bad array header %q", line)
	}
	out := make([]string, 0, n)
	for range n {
		hdr, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		hdr = strings.TrimRight(hdr, "\r\n")
		if hdr == "" || hdr[0] != '$' {
			return nil, fmt.Errorf("bad bulk header %q", hdr)
		}
		l, err := strconv.Atoi(hdr[1:])
		if err != nil {
			return nil, err
		}
		buf := make([]byte, l+2) // payload + CRLF
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		out = append(out, string(buf[:l]))
	}
	return out, nil
}

func TestRun_HTTPModeStartup(t *testing.T) {
	redisAddr := fakeRedis(t)

	// Free port for the discovery HTTP listener.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	apiPort := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())

	cfg := &Config{
		Addr:  fmt.Sprintf("127.0.0.1:%d", apiPort),
		Redis: "redis://" + redisAddr,
		Mode:  "http",
		// No SecKey -> no dmsg surfaces; pure HTTP startup path.
	}
	svc := New(cfg, testLog())

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()

	// Give Run time to open the store, build the API, and launch the
	// listener, then cancel so it returns from <-runCtx.Done().
	time.Sleep(500 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		// http-mode Run returns nil when ctx cancels (no fatal startup error).
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}
