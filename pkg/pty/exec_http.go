// Package pty pkg/pty/exec_http.go c3-vis-pty
// Streaming one-shot exec, served as HTTP over the dmsgpty stream.
//
// The capability is the same as PtyGateway.Exec — run one command on a peer
// visor and learn its exit code — but the output arrives as it is produced
// instead of being buffered until the command exits. Exec cannot do that
// because it is dispatched over net/rpc (see host_mux.go), and net/rpc is
// strictly one call / one reply: there is no way to hand the caller bytes
// mid-call. That is what forces exec_gateway.go to accumulate stdout and
// stderr in memory, and what forces execMaxOutputBytes (16 MiB) on them —
// buffering a peer's unbounded output is a memory-exhaustion vector. Neither
// limit is inherent to the capability; both are inherited from the dispatch.
//
// Why HTTP rather than a bespoke protocol: chunked transfer-encoding is
// streaming, and HTTP trailers exist for precisely the problem this subsystem
// has — a status that is only known AFTER an unbounded body has been sent.
// Declaring "Trailer: X-Exec-Exit-Code, ..." up front and setting those
// headers once the command has exited is the protocol-level answer to
// "where does the exit code go", so this file does not have to invent one.
// The response status line also gives every pre-stream failure a place to
// live: a rejected request is a 400 with a plain-text body, which the client
// always has something to read from. A hand-rolled protocol has to remember
// to say something on every error path or it deadlocks the reader.
//
// Dispatch: this rides hostMux.HandleConn against ExecStreamURI, the same
// raw-conn path sftp uses. The mux does URI matching and the accept response,
// then hands the stream over; from that byte onward the conn speaks HTTP/1.1.
// It is NOT reachable through pkg/dmsg/dmsghttp's ListenAndServe — that binds
// its own dmsg port with no dmsgpty handshake and no whitelist gate. Only the
// wire format is shared, and it is stdlib net/http on both ends, so nothing
// new is vendored.
//
// Exec is NOT deprecated by this. For bounded reads — a unit file, a config,
// a status probe — one round trip returning a structured CommandExecResult is
// a better shape than a stream the caller has to reassemble.
//
// Trust model: identical to Exec, Start and sftp. The conn arriving here was
// already authorized by h.authorize() against the dmsgpty whitelist in the
// listening loop. A peer trusted to open an interactive shell is already
// trusted to run one command, and this is strictly less powerful than the
// shell. There is deliberately no second ACL — see the header of
// sftp_subsystem.go for the same reasoning applied to filesystem access.
package pty

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/logging"
)

// JSON here is the package-level jsoniter config declared in whitelist.go,
// not encoding/json — the package already binds that name.

// Endpoint and media types.
const (
	// execHTTPPath is the only route served. A single route rather than a
	// mux: the conn is one-shot (see handleExecStream), so there is nothing
	// for a router to route between.
	execHTTPPath = "/exec"

	// execStreamMediaType selects the framed body described below. A client
	// that does not ask for it — curl, a browser, anything ad-hoc — gets
	// text/plain with stdout and stderr interleaved, which is what a
	// terminal would have shown. That fallback is the whole reason to pick
	// HTTP: the subsystem stays debuggable with tools nobody had to write.
	execStreamMediaType = "application/vnd.skywire.exec-stream.v1"
)

// Trailer names. HTTP trailers carry the command's exit status because it is
// not known until the body is complete, which is exactly the case RFC 9110
// §6.5 defines trailers for. The alternative — a sentinel record at the end
// of the body — is what a bespoke protocol is forced into, and it makes the
// body format load-bearing for control information.
const (
	trailerExitCode   = "X-Exec-Exit-Code"
	trailerTimedOut   = "X-Exec-Timed-Out"
	trailerDurationMS = "X-Exec-Duration-Ms"
	trailerError      = "X-Exec-Error"
)

// Stream tags inside the framed body.
//
// DESIGN NOTE — stdout vs stderr over one HTTP body.
//
// HTTP gives a request one response body. The buffered path returns stdout
// and stderr as separate fields and callers depend on the split, so it has to
// be recovered somehow. Four options were considered:
//
//   - Two endpoints. Rejected: two responses need two requests, and the two
//     requests would run two different commands. Correlating them would mean
//     inventing a session id — a bespoke protocol, arrived at sideways.
//   - A multiplexing response header. Rejected: headers are sent before the
//     body, so a header can only describe the whole stream, not tag bytes
//     within it.
//   - multipart/mixed, one part per write. Rejected: the boundary must not
//     occur in the content, and this content is arbitrary bytes from an
//     arbitrary command — the guarantee is probabilistic, not structural.
//     Each part also costs ~60 bytes of MIME headers per write.
//   - text/event-stream (SSE). Rejected: it is line-oriented, so binary or
//     ANSI-laden build output must be base64'd (+33%), and Go has no stdlib
//     SSE reader, so the client side is hand-written regardless.
//
// So: a minimal length-prefixed frame inside the body,
// [1 byte tag][4 byte big-endian length][payload]. Length-prefixed rather
// than delimited because a delimiter would need escaping, and build logs are
// exactly the output that contains whatever sentinel was chosen.
//
// The honest finding is that HTTP removes the need to frame *the stream* —
// chunked encoding and trailers do that — but not the need to multiplex two
// streams inside one body. HTTP/2, which has real multiplexing, would answer
// this properly; dmsghttp speaks HTTP/1.1 over a raw stream and h2c would be
// a far larger change than the thing being built.
const (
	execFrameKeepalive byte = 0
	execFrameStdout    byte = 1
	execFrameStderr    byte = 2
)

// execStreamFrameHdr is the frame header size: tag + uint32 length.
const execStreamFrameHdr = 5

// execStreamMaxFrame bounds one frame so a malformed or hostile length prefix
// cannot make the reader allocate without limit. It bounds a frame, not the
// session — total output is unbounded here by design.
const execStreamMaxFrame = 1 << 20 // 1 MiB

// execStreamKeepalive is how often a zero-length keepalive frame is emitted
// while the command produces nothing.
//
// This is not decoration. dmsg.Stream.Read extends its read deadline only on
// a successful read (stream.go), and dmsg.StreamIdleTimeout is 2 minutes — so
// a client blocked on a body that has been silent for two minutes has its own
// stream torn out from under it. A build that spends three minutes in a
// single compile unit would look like a dropped transport. Any streaming
// subsystem over dmsg needs this, framed or not; the framed body is what
// gives it somewhere to put a byte that is not command output.
//
// The text/plain fallback has no such place, and therefore inherits the
// 2-minute quiet limit. That is acceptable for a debugging mode.
const execStreamKeepalive = 45 * time.Second

// execStreamDefaultTimeout and execStreamMaxTimeout are far longer than the
// buffered path's 30s / 5min. Those exist to stop `tail -f` being mis-used as
// Exec and pinning host memory; neither concern applies when nothing is
// buffered and the bytes leave as they arrive. A long-running streaming
// command is no more powerful than the interactive shell the same whitelist
// already grants, and that has no timeout at all. What remains is a backstop
// against a caller that starts something endless and goes away — and note
// that this path also dies when the client does, because writing to a closed
// response kills the handler, which cancels the command's context.
const (
	execStreamDefaultTimeout = 30 * time.Minute
	execStreamMaxTimeout     = 24 * time.Hour
)

// ExecStreamReq is the request body, JSON. It mirrors CommandExecReq so the
// buffered and streaming paths take the same shape of input.
//
// Stdin stays request-side and capped, as in the buffered path: streaming
// stdin would make this a bidirectional session, which is what Start already
// is. A command that needs interactive input wants a TTY, not this.
type ExecStreamReq struct {
	Name      string   `json:"name"`
	Arg       []string `json:"arg,omitempty"`
	Env       []string `json:"env,omitempty"`
	Stdin     []byte   `json:"stdin,omitempty"`
	TimeoutMS int64    `json:"timeout_ms,omitempty"`
}

// writeExecFrame emits one frame. Callers writing from several goroutines
// must hold a mutex — a frame is two writes and they must not interleave.
func writeExecFrame(w io.Writer, tag byte, payload []byte) error {
	var hdr [execStreamFrameHdr]byte
	hdr[0] = tag
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload))) //nolint:gosec // bounded by execStreamMaxFrame at every call site
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	_, err := w.Write(payload)
	return err
}

// execStreamWriter adapts a stream tag onto io.Writer so exec.Cmd can write
// straight into the response. The mutex is shared between the stdout writer,
// the stderr writer and the keepalive ticker, because all three write into
// one response body; os/exec drives stdout and stderr from separate
// goroutines whenever the sinks are not *os.File, which is the case here.
//
// Flush after every write is what makes this stream rather than buffer:
// net/http holds a 2 KiB output buffer, so without it a chatty-but-low-volume
// command would still arrive in lumps.
type execStreamWriter struct {
	mu     *sync.Mutex
	w      io.Writer
	f      http.Flusher
	tag    byte
	framed bool
}

func (sw execStreamWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	sw.mu.Lock()
	defer sw.mu.Unlock()

	if !sw.framed {
		n, err := sw.w.Write(p)
		sw.f.Flush()
		return n, err
	}
	// os/exec hands us whatever the pipe returned, which can exceed one
	// frame. Chunk rather than reject: the caller cannot control the size.
	for off := 0; off < len(p); {
		end := off + execStreamMaxFrame
		if end > len(p) {
			end = len(p)
		}
		if err := writeExecFrame(sw.w, sw.tag, p[off:end]); err != nil {
			return off, err
		}
		off = end
	}
	sw.f.Flush()
	return len(p), nil
}

// handleExecStream returns the connHandleFunc registered against
// ExecStreamURI. One conn serves exactly one command.
//
// The conn arrives after the dmsgpty mux handshake, so it is positioned at
// the first byte of an HTTP request. Serving it means handing net/http a
// listener that yields this one conn — there is no exported way to serve a
// single already-accepted conn, and the alternative (hand-rolling the
// response with http.ReadRequest plus a manual chunked writer) would give
// back the chunked-encoding and trailer machinery that is the reason for
// choosing HTTP at all.
func handleExecStream(h *Host) connHandleFunc {
	return func(ctx context.Context, _ *url.URL, conn net.Conn) error {
		log := logging.MustGetLogger("dmsgpty:exec-stream")
		if h != nil && h.dmsgC != nil {
			if ml := h.dmsgC.MasterLogger(); ml != nil {
				log = ml.PackageLogger("dmsgpty:exec-stream")
			}
		}

		srv := newExecStreamServer(execStreamHandler(ctx, log))

		// Close the conn on ctx-cancel so a host shutdown tears the session
		// down deterministically, as the sftp subsystem does.
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				_ = conn.Close() //nolint:errcheck
			case <-done:
			}
		}()
		defer close(done)

		lis := &singleConnListener{conn: conn, released: make(chan struct{})}
		err := srv.Serve(lis)
		// Serve always ends with an Accept error here: the listener yields
		// its one conn, then blocks until that conn is closed and reports
		// closure. A closed listener is the normal end of a session.
		if err != nil && (errors.Is(err, net.ErrClosed) || errors.Is(err, http.ErrServerClosed)) {
			return nil
		}
		if err != nil {
			log.WithError(err).Debug("exec-stream: serve ended with error.")
		}
		return err
	}
}

// Server deadlines. The request side can be bounded normally; the response
// side cannot be bounded at all, and the asymmetry is the one genuinely
// HTTP-specific hazard in this subsystem:
//
//   - WriteTimeout MUST stay zero. net/http installs it as the conn's write
//     deadline once, at the moment the request is read (server.go, the
//     deferred SetWriteDeadline in conn.readRequest), and never refreshes it.
//     It is therefore a hard ceiling on total handler runtime, not an
//     idle-write bound as the name suggests. Any non-zero value silently
//     truncates a command that outlives it — a 30s WriteTimeout, the value
//     dmsghttp's own ListenAndServe uses, would cap the 25-minute build this
//     subsystem exists to serve at 30 seconds.
//     TestExecStreamWriteTimeoutTruncatesTheStream reproduces it.
//
//   - ReadTimeout is safe here, but only because of a detail worth naming.
//     It stays installed on the conn after the request is read, and once the
//     request body is fully consumed net/http starts a background read to
//     detect client disconnects; a read error there cancels the request
//     context, which would kill the command. What saves it is that
//     connReader.startBackgroundRead clears the read deadline before that
//     read begins. The handler consuming r.Body to EOF is what puts it on
//     that path, so the io.ReadAll below is load-bearing beyond just getting
//     the request. TestExecStreamSurvivesRequestReadTimeout pins the
//     behavior so a stdlib change does not quietly reintroduce it.
const (
	execStreamReadHeaderTimeout = 10 * time.Second
	execStreamReadTimeout       = 30 * time.Second
)

// newExecStreamServer builds the one-conn HTTP server. Factored out so the
// deadline choices above are testable rather than merely asserted.
func newExecStreamServer(h http.Handler) *http.Server {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: execStreamReadHeaderTimeout,
		ReadTimeout:       execStreamReadTimeout,
		WriteTimeout:      0,
		// net/http logs torn connections at whatever the stdlib default
		// logger points at. A dropped dmsg stream is routine here, so it goes
		// to the subsystem's own logger via the handler instead of stderr.
		ErrorLog: stdlog.New(io.Discard, "", 0),
	}
	// One command per conn. Without this the server would hold the conn open
	// waiting for a second request that no client sends, and the dmsg stream
	// would linger until its idle timeout.
	srv.SetKeepAlivesEnabled(false)
	return srv
}

// singleConnListener yields one already-accepted conn to http.Server and then
// blocks until that conn is closed before reporting the listener closed.
//
// The blocking matters. If the second Accept returned an error immediately,
// Serve would return while the handler was still running — the caller would
// treat the subsystem as finished and the mux would close the conn out from
// under a command that had barely started. Waiting for the conn's own Close
// makes Serve's return coincide with the end of the session.
type singleConnListener struct {
	conn     net.Conn
	once     sync.Once
	released chan struct{}
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	var first bool
	l.once.Do(func() { first = true })
	if first {
		return &closeNotifyConn{Conn: l.conn, closed: l.released}, nil
	}
	<-l.released
	return nil, net.ErrClosed
}

func (l *singleConnListener) Close() error { return nil }

func (l *singleConnListener) Addr() net.Addr { return l.conn.LocalAddr() }

// closeNotifyConn signals the listener when net/http is done with the conn.
type closeNotifyConn struct {
	net.Conn
	once   sync.Once
	closed chan struct{}
}

func (c *closeNotifyConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { close(c.closed) })
	return err
}

// execStreamHandler is the HTTP half. Every failure before the body starts is
// a status code with a plain-text explanation, so a client is never left
// waiting on a stream that will not speak — the failure mode that a
// hand-rolled protocol has to defend against by hand on each error path.
func execStreamHandler(hostCtx context.Context, log *logging.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "exec-stream: POST "+execHTTPPath+" only", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != execHTTPPath {
			http.Error(w, "exec-stream: unknown path "+r.URL.Path, http.StatusNotFound)
			return
		}

		// Read the request under the same bound the buffered path puts on
		// stdin, plus room for the JSON envelope around it.
		body, err := io.ReadAll(io.LimitReader(r.Body, execMaxStdinBytes+(1<<16)))
		if err != nil {
			http.Error(w, "exec-stream: reading request: "+err.Error(), http.StatusBadRequest)
			return
		}
		var req ExecStreamReq
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "exec-stream: decoding request: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.Name == "" {
			http.Error(w, "exec-stream: empty command name", http.StatusBadRequest)
			return
		}
		if len(req.Stdin) > execMaxStdinBytes {
			http.Error(w, fmt.Sprintf("exec-stream: stdin %d > max %d", len(req.Stdin), execMaxStdinBytes),
				http.StatusRequestEntityTooLarge)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			// Not reachable with net/http's own ResponseWriter; a guard so a
			// wrapped writer degrades loudly rather than silently buffering.
			http.Error(w, "exec-stream: response writer cannot flush", http.StatusInternalServerError)
			return
		}

		framed := r.Header.Get("Accept") == execStreamMediaType

		timeout := time.Duration(req.TimeoutMS) * time.Millisecond
		if timeout <= 0 {
			timeout = execStreamDefaultTimeout
		}
		if timeout > execStreamMaxTimeout {
			timeout = execStreamMaxTimeout
		}

		// r.Context() is canceled when the client goes away, so a caller
		// pressing Ctrl-C kills the remote command instead of orphaning it.
		// hostCtx folds in host shutdown.
		runCtx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		stopOnHost := make(chan struct{})
		defer close(stopOnHost)
		go func() {
			select {
			case <-hostCtx.Done():
				cancel()
			case <-stopOnHost:
			}
		}()

		// Declare the trailers BEFORE anything is written; net/http only
		// emits trailers it was told about here.
		w.Header().Set("Trailer", trailerExitCode+", "+trailerTimedOut+", "+
			trailerDurationMS+", "+trailerError)
		if framed {
			w.Header().Set("Content-Type", execStreamMediaType)
		} else {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		}
		w.WriteHeader(http.StatusOK)
		// Flush the header block immediately. The client's ReadResponse
		// returns at this point, which is what lets it start rendering
		// before the command has produced anything — and, more practically,
		// what distinguishes "the command is running" from "the host has not
		// answered".
		flusher.Flush()

		start := time.Now()
		cmd := exec.CommandContext(runCtx, req.Name, req.Arg...) //nolint:gosec // intentional remote exec; gated by the dmsgpty whitelist
		cmd.Env = mergeEnv(envSnapshotForExec(), req.Env)
		// Setsid, as in the buffered path: SIGKILL on timeout then reaches
		// the whole process group rather than leaving orphans behind.
		cmd.SysProcAttr = execSysProcAttr()
		if len(req.Stdin) > 0 {
			cmd.Stdin = bytes.NewReader(req.Stdin)
		}

		var mu sync.Mutex
		cmd.Stdout = execStreamWriter{mu: &mu, w: w, f: flusher, tag: execFrameStdout, framed: framed}
		cmd.Stderr = execStreamWriter{mu: &mu, w: w, f: flusher, tag: execFrameStderr, framed: framed}

		if framed {
			stopKA := make(chan struct{})
			defer close(stopKA)
			go keepaliveLoop(stopKA, &mu, w, flusher)
		}

		runErr := cmd.Run()

		exitCode, timedOut, errText := classifyExecResult(runCtx, runErr)
		mu.Lock()
		w.Header().Set(trailerExitCode, strconv.Itoa(exitCode))
		w.Header().Set(trailerTimedOut, strconv.FormatBool(timedOut))
		w.Header().Set(trailerDurationMS, strconv.FormatInt(time.Since(start).Milliseconds(), 10))
		if errText != "" {
			w.Header().Set(trailerError, errText)
		}
		mu.Unlock()
		log.WithField("cmd", req.Name).WithField("exit_code", exitCode).
			WithField("timed_out", timedOut).Debug("exec-stream: command ended.")
	})
}

// keepaliveLoop emits a zero-length frame every execStreamKeepalive while the
// command is silent, so the client's dmsg stream keeps extending its read
// deadline. See the constant for why this is load-bearing rather than
// cosmetic.
func keepaliveLoop(stop <-chan struct{}, mu *sync.Mutex, w io.Writer, f http.Flusher) {
	t := time.NewTicker(execStreamKeepalive)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			mu.Lock()
			err := writeExecFrame(w, execFrameKeepalive, nil)
			if err == nil {
				f.Flush()
			}
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

// classifyExecResult maps the (ctx, error) pair from cmd.Run onto the trailer
// fields, following os/exec conventions: -1 on signal-kill, 0 on success, the
// process's own code otherwise.
func classifyExecResult(runCtx context.Context, runErr error) (exitCode int, timedOut bool, errText string) {
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return -1, true, ""
	}
	if runErr == nil {
		return 0, false, ""
	}
	var ee *exec.ExitError
	if errors.As(runErr, &ee) {
		return ee.ExitCode(), false, ""
	}
	// A spawn failure — command not found, not executable. The exit code is
	// meaningless, so the reason has to travel as text.
	return -1, false, runErr.Error()
}
