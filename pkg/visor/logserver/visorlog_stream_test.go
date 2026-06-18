package logserver

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// TestRenderVisorLogHTMLStreams verifies the colorized log view tails the file:
// it renders the existing backlog AND a line appended after the response has
// started, then returns when the client (request context) disconnects. Body is
// read only after the handler goroutine has returned, so there's no concurrent
// access to the recorder buffer.
func TestRenderVisorLogHTMLStreams(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	logFile := filepath.Join(dir, "skywire.log")
	if err := os.WriteFile(logFile, []byte("[2026-06-18T10:00:00-05:00] INFO [test]: backlog line\n"), 0o600); err != nil {
		t.Fatalf("seed log: %v", err)
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Request = httptest.NewRequest("GET", "/skywire.log", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		renderVisorLogHTML(c, logFile)
		close(done)
	}()

	// Give it time to render the backlog and enter the follow loop, then append
	// a line; the tail should pick it up within a couple of poll intervals.
	time.Sleep(2 * logFollowPollInterval)
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec
	if err != nil {
		t.Fatalf("reopen log for append: %v", err)
	}
	if _, err := f.WriteString("[2026-06-18T10:00:01-05:00] WARN [test]: appended line\n"); err != nil {
		t.Fatalf("append: %v", err)
	}
	_ = f.Close() //nolint:errcheck
	time.Sleep(3 * logFollowPollInterval)

	// Disconnect → handler must return promptly.
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("renderVisorLogHTML did not return after client disconnect")
	}

	body := rec.Body.String()
	for _, want := range []string{
		"<pre>",         // preamble written
		"backlog line",  // existing content rendered
		"appended line", // proves it streamed an append after start
		"color:",        // colorized (per-element spans)
	} {
		if !strings.Contains(body, want) {
			t.Errorf("streamed body missing %q\nbody:\n%s", want, body)
		}
	}
	// A live tail must NOT terminate the document — the </pre></body></html>
	// only ever follows a finite render, which this no longer is.
	if strings.Contains(body, "</body></html>") {
		t.Errorf("streaming view should not emit a closing document; body:\n%s", body)
	}
}

// TestRenderVisorLogHTMLStreamsOverHTTP proves the view streams over a REAL
// http.Server (chunked transfer-encoding over a TCP conn) — the same transport
// the dmsg-HTTP server and the in-process self-loopback use. It confirms the
// response has no Content-Length (so Go chunks it) and that a line appended
// AFTER the response started reaches the client before the request ends.
func TestRenderVisorLogHTMLStreamsOverHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	logFile := filepath.Join(dir, "skywire.log")
	if err := os.WriteFile(logFile, []byte("[2026-06-18T10:00:00-05:00] INFO [test]: backlog over http\n"), 0o600); err != nil {
		t.Fatalf("seed log: %v", err)
	}

	r := gin.New()
	r.GET("/skywire.log", func(c *gin.Context) { renderVisorLogHTML(c, logFile) })
	srv := httptest.NewServer(r)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/skywire.log", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck

	// A streamed response carries no Content-Length — that's what makes Go use
	// chunked transfer-encoding and flush incrementally.
	if resp.ContentLength != -1 {
		t.Errorf("expected unbounded (chunked) response, got Content-Length=%d", resp.ContentLength)
	}

	// Drain the body in the background into a mutex-guarded buffer; the test
	// polls it for markers so a blocking Read never stalls the test.
	var (
		mu  sync.Mutex
		buf strings.Builder
	)
	go func() {
		br := bufio.NewReader(resp.Body)
		p := make([]byte, 4096)
		for {
			n, rerr := br.Read(p)
			if n > 0 {
				mu.Lock()
				buf.Write(p[:n])
				mu.Unlock()
			}
			if rerr != nil {
				return
			}
		}
	}()

	contains := func(s string) bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(buf.String(), s)
	}
	waitFor := func(s string, d time.Duration) bool {
		deadline := time.Now().Add(d)
		for time.Now().Before(deadline) {
			if contains(s) {
				return true
			}
			time.Sleep(20 * time.Millisecond)
		}
		return false
	}

	if !waitFor("backlog over http", 3*time.Second) {
		t.Fatal("did not receive backlog over the streamed connection")
	}

	// Append AFTER the stream is established; it must arrive without us closing.
	f, ferr := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec
	if ferr != nil {
		t.Fatalf("reopen for append: %v", ferr)
	}
	_, _ = f.WriteString("[2026-06-18T10:00:01-05:00] ERROR [test]: live append over http\n") //nolint:errcheck
	_ = f.Close()                                                                             //nolint:errcheck

	if !waitFor("live append over http", 3*time.Second) {
		t.Fatal("appended line did not stream to the client (buffering, not streaming)")
	}

	cancel() // disconnect; server handler must observe ctx and stop
}
