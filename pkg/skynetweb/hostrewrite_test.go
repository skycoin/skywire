package skynetweb

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// fakeConn is the in-memory pair used to drive the wrapper. Reads/
// writes go through bytes.Buffer; addresses are stubs.
type fakeConn struct {
	r *io.PipeReader
	w *io.PipeWriter
	// out is what was received on the "backend" side. Tests assert
	// against this.
	out bytes.Buffer
}

func (c *fakeConn) Read(p []byte) (int, error)         { return c.r.Read(p) }
func (c *fakeConn) Write(p []byte) (int, error)        { n, err := c.out.Write(p); return n, err }
func (c *fakeConn) Close() error {
	_ = c.r.Close() //nolint:errcheck,gosec
	_ = c.w.Close() //nolint:errcheck,gosec
	return nil
}
func (c *fakeConn) LocalAddr() net.Addr                { return &net.TCPAddr{} }
func (c *fakeConn) RemoteAddr() net.Addr               { return &net.TCPAddr{} }
func (c *fakeConn) SetDeadline(t time.Time) error      { return nil }
func (c *fakeConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *fakeConn) SetWriteDeadline(t time.Time) error { return nil }

func newFakeConn() *fakeConn {
	pr, pw := io.Pipe()
	return &fakeConn{r: pr, w: pw}
}

func TestSplitHostSubdomain(t *testing.T) {
	const suffix = ".skynet"
	const pk = "0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c"

	cases := []struct {
		host          string
		wantPK        string
		wantSubdomain string
		wantPort      uint16
		wantErr       bool
	}{
		{pk + ".skynet", pk, "", 80, false},
		{pk + ".skynet:443", pk, "", 443, false},
		{"blog." + pk + ".skynet", pk, "blog", 80, false},
		{"example.com." + pk + ".skynet", pk, "example.com", 80, false},
		{"a.b.c." + pk + ".skynet:8080", pk, "a.b.c", 8080, false},
		{"no-suffix-here", "", "", 0, true},
		{pk + ".skynet:notanum", "", "", 0, true},
	}
	for _, c := range cases {
		gotPK, gotSub, gotPort, err := splitHostSubdomain(c.host, suffix)
		if c.wantErr {
			if err == nil {
				t.Errorf("splitHostSubdomain(%q): expected err, got nil", c.host)
			}
			continue
		}
		if err != nil {
			t.Errorf("splitHostSubdomain(%q): unexpected err %v", c.host, err)
			continue
		}
		if gotPK != c.wantPK || gotSub != c.wantSubdomain || gotPort != c.wantPort {
			t.Errorf("splitHostSubdomain(%q): got (%q, %q, %d), want (%q, %q, %d)",
				c.host, gotPK, gotSub, gotPort, c.wantPK, c.wantSubdomain, c.wantPort)
		}
	}
}

// TestHostRewriteConn_RewritesHostHeader feeds a complete HTTP/1.1
// request into the wrapper's Write side and asserts the backend
// received the request with Host: rewritten.
func TestHostRewriteConn_RewritesHostHeader(t *testing.T) {
	backend := newFakeConn()
	hr := newHostRewriteConn(backend, "example.com")
	defer hr.Close() //nolint:errcheck,gosec

	// Browser request. Host header should be the .skynet-flavor URL;
	// after the wrapper, the backend sees Host: example.com.
	const browserRequest = "GET /path?q=1 HTTP/1.1\r\n" +
		"Host: example.com.0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c.skynet\r\n" +
		"User-Agent: testclient\r\n" +
		"Accept: */*\r\n" +
		"\r\n"

	if _, err := hr.Write([]byte(browserRequest)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// The parser goroutine pumps async; give it a moment to flush.
	// In production this is the SOCKS5 splice's poll loop; here we
	// just poll the buffer.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && backend.out.Len() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if backend.out.Len() == 0 {
		t.Fatal("backend received nothing within deadline")
	}

	// Parse what the backend got and verify Host.
	got, err := http.ReadRequest(bufio.NewReader(&backend.out))
	if err != nil {
		t.Fatalf("backend parse: %v", err)
	}
	if got.Host != "example.com" {
		t.Errorf("Host = %q, want %q", got.Host, "example.com")
	}
	if got.URL.Path != "/path" {
		t.Errorf("Path = %q, want /path", got.URL.Path)
	}
	if got.URL.RawQuery != "q=1" {
		t.Errorf("RawQuery = %q, want q=1", got.URL.RawQuery)
	}
}

// TestHostRewriteConn_RewritesTwoSequentialRequests verifies that
// HTTP/1.1 keep-alive works — multiple requests on one connection
// each get Host rewritten.
func TestHostRewriteConn_RewritesTwoSequentialRequests(t *testing.T) {
	backend := newFakeConn()
	hr := newHostRewriteConn(backend, "example.com")
	defer hr.Close() //nolint:errcheck,gosec

	body := "GET /a HTTP/1.1\r\nHost: example.com.<pk>.skynet\r\n\r\n" +
		"GET /b HTTP/1.1\r\nHost: example.com.<pk>.skynet\r\n\r\n"
	if _, err := hr.Write([]byte(body)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if bytes.Count(backend.out.Bytes(), []byte("\r\n\r\n")) >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	br := bufio.NewReader(&backend.out)
	for i, wantPath := range []string{"/a", "/b"} {
		req, err := http.ReadRequest(br)
		if err != nil {
			t.Fatalf("req %d parse: %v", i, err)
		}
		if req.Host != "example.com" {
			t.Errorf("req %d Host = %q, want example.com", i, req.Host)
		}
		if req.URL.Path != wantPath {
			t.Errorf("req %d Path = %q, want %q", i, req.URL.Path, wantPath)
		}
	}
}
