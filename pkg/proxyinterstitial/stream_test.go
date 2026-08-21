package proxyinterstitial

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// readStreamBody drives one request/response over the streaming conn and returns
// the fully de-chunked body.
func readStreamBody(t *testing.T, c net.Conn, reqLine string) string {
	t.Helper()
	go func() { _, _ = c.Write([]byte(reqLine)) }()
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}

func TestStreamConnReloadsOnReady(t *testing.T) {
	var n int32
	cfg := StreamConfig{
		Target: "host.skynet", Mechanism: "skynet",
		Interval: 2 * time.Millisecond, Deadline: 2 * time.Second,
		Probe: func(context.Context) error {
			if atomic.AddInt32(&n, 1) < 3 { // fail twice, then succeed
				return errors.New("no route to host")
			}
			return nil
		},
	}
	c := StreamConn(context.Background(), cfg)
	body := readStreamBody(t, c, "GET / HTTP/1.1\r\nHost: host.skynet\r\n\r\n")

	for _, want := range []string{"skywire", "host.skynet", "Building a route", "Route group up", "location.replace(location.href)"} {
		if !strings.Contains(body, want) {
			t.Errorf("streamed body missing %q\n---\n%s", want, body)
		}
	}
	if strings.Contains(body, "Retry") {
		t.Error("success stream must not offer a manual retry")
	}
	if got := atomic.LoadInt32(&n); got < 3 {
		t.Errorf("probe called %d times, expected >=3", got)
	}
}

func TestStreamConnHardError(t *testing.T) {
	cfg := StreamConfig{
		Target: "host.dmsg", Mechanism: "dmsg",
		Interval: 2 * time.Millisecond, Deadline: 2 * time.Second,
		Probe: func(context.Context) error {
			return errors.New("permanently broken widget") // not transient
		},
	}
	c := StreamConn(context.Background(), cfg)
	body := readStreamBody(t, c, "GET / HTTP/1.1\r\nHost: host.dmsg\r\n\r\n")

	if !strings.Contains(body, "Retry") {
		t.Errorf("hard-error stream should offer a manual retry\n%s", body)
	}
	if strings.Contains(body, "location.replace") {
		t.Error("hard-error stream must not auto-reload")
	}
}

func TestStreamConnHTTP10Fallback(t *testing.T) {
	cfg := StreamConfig{
		Target: "host.skynet", Mechanism: "skynet",
		Probe: func(context.Context) error { return nil },
	}
	c := StreamConn(context.Background(), cfg)
	body := readStreamBody(t, c, "GET / HTTP/1.0\r\nHost: host.skynet\r\n\r\n")

	// The one-shot page (auto-refresh), not the streamed one.
	if !strings.Contains(body, `http-equiv="refresh"`) {
		t.Errorf("HTTP/1.0 client should get the one-shot meta-refresh page\n%s", body)
	}
	if strings.Contains(body, "location.replace") {
		t.Error("fallback page should not carry the stream reload script")
	}
}

func TestStreamConnEscapesTarget(t *testing.T) {
	cfg := StreamConfig{
		Target: "<script>evil()</script>", Mechanism: "skynet",
		Interval: time.Millisecond, Deadline: time.Second,
		Probe: func(context.Context) error { return nil },
	}
	c := StreamConn(context.Background(), cfg)
	body := readStreamBody(t, c, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
	if strings.Contains(body, "<script>evil()") {
		t.Error("target host was not HTML-escaped in the stream")
	}
}
