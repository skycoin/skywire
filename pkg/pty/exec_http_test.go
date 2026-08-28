//go:build !windows

// Package pty pkg/pty/exec_http_test.go c3-vis-pty
package pty

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/logging"
)

// serveExecStream wires the host handler onto one end of a net.Pipe, skipping
// the dmsgpty mux handshake (which the mux itself performs before the handler
// is reached). Returns the client end.
func serveExecStream(t *testing.T) net.Conn {
	t.Helper()
	cliConn, hostConn := net.Pipe()
	go func() {
		_ = handleExecStream(nil)(context.Background(), nil, hostConn) //nolint:errcheck
	}()
	t.Cleanup(func() { _ = cliConn.Close() }) //nolint:errcheck
	return cliConn
}

// runOverPipe runs one request end-to-end and returns what the client saw.
func runOverPipe(t *testing.T, req ExecStreamReq) (*ExecStreamResult, string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	res, err := runExecStream(context.Background(), serveExecStream(t), req, &stdout, &stderr)
	return res, stdout.String(), stderr.String(), err
}

func TestExecStreamCarriesOutputAndExitCode(t *testing.T) {
	res, out, errOut, err := runOverPipe(t, ExecStreamReq{
		Name: "/bin/sh",
		Arg:  []string{"-c", "echo out; echo err 1>&2; exit 7"},
	})
	if err != nil {
		t.Fatalf("runExecStream: %v", err)
	}
	if strings.TrimSpace(out) != "out" {
		t.Errorf("stdout = %q, want %q", out, "out")
	}
	// stderr arriving on its own writer is what the framed body buys; a
	// single HTTP body would otherwise have merged the two.
	if strings.TrimSpace(errOut) != "err" {
		t.Errorf("stderr = %q, want %q", errOut, "err")
	}
	// The exit code rides an HTTP trailer, so this also asserts that the
	// trailer block was declared, emitted and parsed.
	if res.ExitCode != 7 {
		t.Errorf("exit code = %d, want 7", res.ExitCode)
	}
	if res.TimedOut {
		t.Error("TimedOut set on a command that exited on its own")
	}
}

// The point of the whole subsystem: output must reach the caller while the
// command is still running, not in one dump at the end. This writes, sleeps,
// then writes again, and checks the first line arrived long before the
// command could have exited. A buffering implementation fails it.
func TestExecStreamDeliversOutputBeforeExit(t *testing.T) {
	firstSeen := make(chan time.Duration, 1)
	start := time.Now()
	w := writerFunc(func(p []byte) (int, error) {
		if strings.Contains(string(p), "first") {
			select {
			case firstSeen <- time.Since(start):
			default:
			}
		}
		return len(p), nil
	})

	res, err := runExecStream(context.Background(), serveExecStream(t), ExecStreamReq{
		Name: "/bin/sh",
		Arg:  []string{"-c", "echo first; sleep 2; echo second"},
	}, w, nil)
	if err != nil {
		t.Fatalf("runExecStream: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d", res.ExitCode)
	}

	select {
	case at := <-firstSeen:
		if at > 1500*time.Millisecond {
			t.Errorf("first line arrived after %v — that is buffering, not streaming", at)
		}
	default:
		t.Fatal("never saw the first line")
	}
}

// Output larger than one frame has to be chunked by the writer and
// reassembled by the reader, on top of HTTP's own chunked encoding. A build
// log is exactly this case.
func TestExecStreamChunksLargeOutput(t *testing.T) {
	const lines = 20000
	res, out, _, err := runOverPipe(t, ExecStreamReq{
		Name: "/bin/sh",
		Arg: []string{"-c", "i=0; while [ $i -lt " + strconv.Itoa(lines) +
			" ]; do echo aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa; i=$((i+1)); done"},
	})
	if err != nil {
		t.Fatalf("runExecStream: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d", res.ExitCode)
	}
	if got := strings.Count(out, "\n"); got != lines {
		t.Errorf("got %d lines, want %d — frames were lost or mis-assembled", got, lines)
	}
}

func TestExecStreamTimeoutIsReported(t *testing.T) {
	res, _, _, err := runOverPipe(t, ExecStreamReq{
		Name:      "/bin/sh",
		Arg:       []string{"-c", "sleep 30"},
		TimeoutMS: 300,
	})
	if err != nil {
		t.Fatalf("runExecStream: %v", err)
	}
	if !res.TimedOut {
		t.Error("TimedOut not set on a command killed by the deadline")
	}
	if res.ExitCode != -1 {
		t.Errorf("exit code = %d, want -1 for a signal-killed command", res.ExitCode)
	}
}

// A spawn failure has no exit code to report, so the reason has to travel in
// the error trailer rather than being swallowed.
func TestExecStreamReportsSpawnFailure(t *testing.T) {
	res, _, _, err := runOverPipe(t, ExecStreamReq{Name: "/nonexistent/definitely-not-here"})
	if err != nil {
		t.Fatalf("runExecStream: %v", err)
	}
	if res.ExitCode != -1 {
		t.Errorf("exit code = %d, want -1", res.ExitCode)
	}
	if res.Err == "" {
		t.Error("no reason reported for a command that could not be spawned")
	}
}

// A rejected request must leave the client with something to read. The
// equivalent test on the hand-rolled-frame branch found a real deadlock: the
// handler returned without writing anything and the client blocked forever.
// Here the rejection is an HTTP status line, which the server writes before
// it can block on anything — so the failure mode is structurally absent. The
// test guards the claim, and would hang (not fail) if it stopped holding.
func TestExecStreamRejectsEmptyCommandWithoutDeadlock(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		_, _, _, err := runOverPipe(t, ExecStreamReq{})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("an empty command name was accepted")
		}
		if !strings.Contains(err.Error(), "400") {
			t.Errorf("err = %v, want a 400-shaped rejection", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("client blocked on a rejected request — the error path wrote nothing")
	}
}

// A request that is not the one route served gets a status, not a hang.
func TestExecStreamRejectsWrongMethod(t *testing.T) {
	conn := serveExecStream(t)
	hreq, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"http://"+execStreamHostHeader+execHTTPPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- hreq.Write(conn) }()

	resp, err := http.ReadResponse(bufio.NewReader(conn), hreq)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("writing request: %v", err)
	}
}

// A malformed body is a 400 as well, not a torn stream.
func TestExecStreamRejectsMalformedRequest(t *testing.T) {
	conn := serveExecStream(t)
	hreq, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"http://"+execStreamHostHeader+execHTTPPath, strings.NewReader("{not json"))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = hreq.Write(conn) }() //nolint:errcheck

	resp, err := http.ReadResponse(bufio.NewReader(conn), hreq)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// A client that does not ask for the framed media type gets plain interleaved
// text — the property that makes this debuggable with curl, which is half the
// argument for choosing HTTP at all.
func TestExecStreamPlainTextForOrdinaryClients(t *testing.T) {
	conn := serveExecStream(t)
	body, err := json.Marshal(ExecStreamReq{
		Name: "/bin/sh",
		Arg:  []string{"-c", "echo hello; echo world 1>&2; exit 3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	hreq, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"http://"+execStreamHostHeader+execHTTPPath, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	hreq.ContentLength = int64(len(body))
	go func() { _ = hreq.Write(conn) }() //nolint:errcheck

	resp, err := http.ReadResponse(bufio.NewReader(conn), hreq)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
	var got bytes.Buffer
	if _, err := got.ReadFrom(resp.Body); err != nil {
		t.Fatalf("reading body: %v", err)
	}
	// Both streams are present, interleaved, exactly as a terminal shows them.
	if !strings.Contains(got.String(), "hello") || !strings.Contains(got.String(), "world") {
		t.Errorf("body = %q, want both streams", got.String())
	}
	if code := resp.Trailer.Get(trailerExitCode); code != "3" {
		t.Errorf("%s trailer = %q, want \"3\"", trailerExitCode, code)
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// serveExecStreamWith runs the handler behind a caller-configured server, so
// the deadline tests can misconfigure one on purpose.
func serveExecStreamWith(t *testing.T, tune func(*http.Server)) net.Conn {
	t.Helper()
	cliConn, hostConn := net.Pipe()
	srv := newExecStreamServer(execStreamHandler(context.Background(),
		logging.MustGetLogger("dmsgpty:exec-stream-test")))
	tune(srv)
	go func() {
		_ = srv.Serve(&singleConnListener{conn: hostConn, released: make(chan struct{})}) //nolint:errcheck
	}()
	t.Cleanup(func() { _ = cliConn.Close() }) //nolint:errcheck
	return cliConn
}

// http.Server.WriteTimeout is a hard ceiling on handler runtime, not the
// idle-write bound its name suggests: net/http installs it as the conn's
// write deadline once, when the request is read, and never refreshes it. For
// a streaming handler that makes it a cap on how long the command may run.
// This reproduces the truncation so the zero in newExecStreamServer is
// defended by a test rather than by a comment alone.
func TestExecStreamWriteTimeoutTruncatesTheStream(t *testing.T) {
	conn := serveExecStreamWith(t, func(s *http.Server) {
		s.WriteTimeout = 500 * time.Millisecond
	})
	res, err := runExecStream(context.Background(), conn, ExecStreamReq{
		Name: "/bin/sh",
		Arg:  []string{"-c", "echo early; sleep 3; echo late"},
	}, nil, nil)
	if err == nil && res.ExitCode == 0 {
		t.Fatal("a 500ms WriteTimeout no longer truncates a 3s command — " +
			"the WriteTimeout note on newExecStreamServer is stale, please update it")
	}

	if got := newExecStreamServer(nil).WriteTimeout; got != 0 {
		t.Errorf("shipped server has WriteTimeout = %v; it MUST be 0 or it caps command runtime", got)
	}
}

// The mirror image: ReadTimeout stays installed on the conn after the request
// is read, and net/http's post-body background read would cancel the request
// context (killing the command) if the deadline fired — except that
// startBackgroundRead clears it first. That is a stdlib implementation detail
// this subsystem depends on, so it gets a test: a command far outliving
// ReadTimeout must still finish normally.
func TestExecStreamSurvivesRequestReadTimeout(t *testing.T) {
	conn := serveExecStreamWith(t, func(s *http.Server) {
		s.ReadTimeout = 300 * time.Millisecond
	})
	var out bytes.Buffer
	res, err := runExecStream(context.Background(), conn, ExecStreamReq{
		Name: "/bin/sh",
		Arg:  []string{"-c", "sleep 1.5; echo survived"},
	}, &out, nil)
	if err != nil {
		t.Fatalf("a command outliving ReadTimeout was cut short: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", res.ExitCode)
	}
	if strings.TrimSpace(out.String()) != "survived" {
		t.Errorf("stdout = %q, want %q", out.String(), "survived")
	}
}

// Keepalive frames exist so a silent command does not let the client's dmsg
// stream hit its 2-minute idle read deadline. They must be invisible to the
// caller's writers.
func TestExecStreamKeepaliveFramesAreNotOutput(t *testing.T) {
	// Feed the demuxer a body with keepalives interleaved around real output
	// to assert they are dropped rather than written or mistaken for a tag.
	var body bytes.Buffer
	if err := writeExecFrame(&body, execFrameKeepalive, nil); err != nil {
		t.Fatal(err)
	}
	if err := writeExecFrame(&body, execFrameStdout, []byte("payload")); err != nil {
		t.Fatal(err)
	}
	if err := writeExecFrame(&body, execFrameKeepalive, nil); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if err := demuxExecStream(&body, &out, &errOut); err != nil {
		t.Fatalf("demuxExecStream: %v", err)
	}
	if out.String() != "payload" {
		t.Errorf("stdout = %q, want %q", out.String(), "payload")
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want empty", errOut.String())
	}
}

// End-to-end through the real dmsgpty mux: the URI handshake the production
// dispatch performs, then the HTTP exchange on the same conn. The per-handler
// tests above bypass the mux, so without this nothing would catch a missing
// or misspelled ExecStreamURI registration in dmsgEndpoints.
func TestExecStreamThroughMux(t *testing.T) {
	mux := hostMux{}
	if err := mux.HandleConn(ExecStreamURI, handleExecStream(nil)); err != nil {
		t.Fatal(err)
	}
	srvConn, cliConn := net.Pipe()
	defer func() { _ = cliConn.Close() }() //nolint:errcheck
	go func() {
		_ = mux.ServeConn(context.Background(), srvConn) //nolint:errcheck
	}()

	if err := openExecStreamSubsystem(cliConn); err != nil {
		t.Fatalf("openExecStreamSubsystem: %v", err)
	}
	var out bytes.Buffer
	res, err := runExecStream(context.Background(), cliConn, ExecStreamReq{
		Name: "/bin/sh",
		Arg:  []string{"-c", "echo through-the-mux; exit 5"},
	}, &out, nil)
	if err != nil {
		t.Fatalf("runExecStream: %v", err)
	}
	if strings.TrimSpace(out.String()) != "through-the-mux" {
		t.Errorf("stdout = %q", out.String())
	}
	if res.ExitCode != 5 {
		t.Errorf("exit code = %d, want 5", res.ExitCode)
	}
}

// An older host with no ExecStreamURI registration must reject the handshake
// rather than half-accept it, and the client must name that plainly instead
// of reporting it as a transport fault.
func TestExecStreamOlderHostRejectsHandshake(t *testing.T) {
	mux := hostMux{}
	srvConn, cliConn := net.Pipe()
	defer func() { _ = cliConn.Close() }() //nolint:errcheck
	go func() {
		_ = mux.ServeConn(context.Background(), srvConn) //nolint:errcheck
	}()

	err := openExecStreamSubsystem(cliConn)
	if err == nil {
		t.Fatal("a host with no exec-stream registration accepted the handshake")
	}
	if !strings.Contains(err.Error(), "older binary") {
		t.Errorf("err = %v, want it to name the likely cause", err)
	}
}

// A client that goes away must take the remote command with it, not leave it
// running to the default 30-minute deadline. net/http gives this for free:
// once the request body has been consumed the server keeps a background read
// on the conn, and the EOF from a closed client cancels the request context,
// which cancels the command's context.
//
// The handler returning promptly is the observable: without cancellation it
// would sit for execStreamDefaultTimeout with an orphan process attached.
func TestExecStreamClientDisconnectKillsCommand(t *testing.T) {
	cliConn, hostConn := net.Pipe()
	handlerDone := make(chan struct{})
	go func() {
		_ = handleExecStream(nil)(context.Background(), nil, hostConn) //nolint:errcheck
		close(handlerDone)
	}()

	body, err := json.Marshal(ExecStreamReq{
		Name: "/bin/sh",
		Arg:  []string{"-c", "echo started; sleep 600"},
	})
	if err != nil {
		t.Fatal(err)
	}
	hreq, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"http://"+execStreamHostHeader+execHTTPPath, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	hreq.Header.Set("Accept", execStreamMediaType)
	hreq.ContentLength = int64(len(body))
	go func() { _ = hreq.Write(cliConn) }() //nolint:errcheck

	resp, err := http.ReadResponse(bufio.NewReader(cliConn), hreq)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	// Wait until the command has actually started before walking away.
	hdr := make([]byte, execStreamFrameHdr)
	if _, err := io.ReadFull(resp.Body, hdr); err != nil {
		t.Fatalf("reading first frame header: %v", err)
	}

	_ = cliConn.Close() //nolint:errcheck

	select {
	case <-handlerDone:
	case <-time.After(20 * time.Second):
		t.Fatal("handler still running after the client left — the command was orphaned")
	}
}
