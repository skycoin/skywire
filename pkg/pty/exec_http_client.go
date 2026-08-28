// Package pty pkg/pty/exec_http_client.go c3-vis-pty
// Client half of the streaming exec subsystem. See exec_http.go for why it is
// HTTP and what the framed body looks like.
//
// This does not use pkg/dmsg/dmsghttp's HTTPTransport. That transport dials
// its own dmsg stream and pools it, which skips the dmsgpty mux handshake
// (and therefore the whitelist gate) and would keep a finished exec stream in
// an idle pool for a subsystem that is one-command-per-conn. What is wanted
// here is one HTTP exchange over a conn somebody else already authorized, and
// that is exactly http.Request.Write + http.ReadResponse.
package pty

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// execStreamHostHeader is the Host: value. The peer is addressed by the dmsg
// stream itself, so the name is a constant placeholder — but it has to be
// present and syntactically valid or http.Request.Write refuses.
const execStreamHostHeader = "dmsgpty"

// ExecStreamResult is what the caller gets once the command has ended. It
// carries the same facts CommandExecResult does, minus the output — that
// already went to the writers as it arrived.
type ExecStreamResult struct {
	ExitCode   int
	TimedOut   bool
	DurationMS int64
	Err        string
}

// runExecStream drives one HTTP exchange over an already-dispatched conn:
// POST the request, demultiplex the framed body into stdout/stderr as it
// arrives, then read the trailers.
//
// The writers are called from this goroutine only, so a caller passing
// os.Stdout and os.Stderr needs no locking of its own.
func runExecStream(ctx context.Context, conn io.ReadWriter, req ExecStreamReq, stdout, stderr io.Writer) (*ExecStreamResult, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("exec-stream: encoding request: %w", err)
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+execStreamHostHeader+execHTTPPath, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("exec-stream: building request: %w", err)
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", execStreamMediaType)
	hreq.ContentLength = int64(len(b))
	if err := hreq.Write(conn); err != nil {
		return nil, fmt.Errorf("exec-stream: sending request: %w", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, hreq)
	if err != nil {
		return nil, fmt.Errorf("exec-stream: reading response: %w", err)
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		// Every pre-stream rejection lands here with a readable reason. The
		// status line is written before the handler can block on anything,
		// so a refused request can never leave this side waiting.
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096)) //nolint:errcheck
		return nil, fmt.Errorf("exec-stream: host refused: %s: %s",
			resp.Status, strings.TrimSpace(string(msg)))
	}
	if ct := resp.Header.Get("Content-Type"); ct != execStreamMediaType {
		return nil, fmt.Errorf("exec-stream: host answered with %q, not the framed stream — older binary?", ct)
	}

	if err := demuxExecStream(resp.Body, stdout, stderr); err != nil {
		return nil, err
	}
	return execStreamTrailer(resp)
}

// demuxExecStream reads framed records from the body until it ends cleanly,
// routing each to the matching writer.
func demuxExecStream(body io.Reader, stdout, stderr io.Writer) error {
	var hdr [execStreamFrameHdr]byte
	for {
		if _, err := io.ReadFull(body, hdr[:]); err != nil {
			if errors.Is(err, io.EOF) {
				// A clean end at a frame boundary: the body is complete and
				// the trailers are now readable.
				return nil
			}
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return errors.New("exec-stream: stream ended mid-frame")
			}
			return err
		}
		n := binary.BigEndian.Uint32(hdr[1:])
		if n > execStreamMaxFrame {
			return fmt.Errorf("exec-stream: frame %d > max %d", n, execStreamMaxFrame)
		}
		var w io.Writer
		switch hdr[0] {
		case execFrameKeepalive:
			// Sent only to keep the dmsg stream's read deadline extended
			// through a quiet command; carries nothing.
		case execFrameStdout:
			w = stdout
		case execFrameStderr:
			w = stderr
		default:
			return fmt.Errorf("exec-stream: unknown frame tag %d", hdr[0])
		}
		if n == 0 {
			continue
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(body, buf); err != nil {
			return fmt.Errorf("exec-stream: reading frame payload: %w", err)
		}
		if w == nil {
			continue
		}
		if _, err := w.Write(buf); err != nil {
			return err
		}
	}
}

// execStreamTrailer turns the response trailers into a result. Trailers are
// only populated once the body has been read to EOF, which demuxExecStream
// has just done.
func execStreamTrailer(resp *http.Response) (*ExecStreamResult, error) {
	code := resp.Trailer.Get(trailerExitCode)
	if code == "" {
		// The body ended but the exit status never arrived: the host died,
		// the visor restarted, or the transport dropped between the last
		// chunk and the trailer block. Say that, rather than reporting an
		// exit code that was never received.
		return nil, errors.New("exec-stream: stream ended before the command reported an exit code")
	}
	exit, err := strconv.Atoi(code)
	if err != nil {
		return nil, fmt.Errorf("exec-stream: malformed %s trailer %q: %w", trailerExitCode, code, err)
	}
	res := &ExecStreamResult{
		ExitCode: exit,
		TimedOut: resp.Trailer.Get(trailerTimedOut) == "true",
		Err:      resp.Trailer.Get(trailerError),
	}
	if d := resp.Trailer.Get(trailerDurationMS); d != "" {
		res.DurationMS, _ = strconv.ParseInt(d, 10, 64) //nolint:errcheck // advisory; a bad value is not worth failing the run over
	}
	return res, nil
}

// openExecStreamSubsystem performs the dmsgpty mux handshake. An "invalid
// request" response means the remote host has no ExecStreamURI registration —
// an older binary — which is worth naming plainly so operators do not chase
// it as a transport fault.
func openExecStreamSubsystem(conn net.Conn) error {
	if err := writeRequest(conn, ExecStreamURI); err != nil {
		return err
	}
	if err := readResponse(conn); err != nil {
		return fmt.Errorf("remote dmsgpty-host has no exec-stream subsystem (older binary?): %w", err)
	}
	return nil
}

// ExecStreamDmsg runs a command on rPK over the dmsg overlay, streaming its
// output to stdout/stderr as it is produced.
func ExecStreamDmsg(
	ctx context.Context,
	dmsgC *dmsg.Client,
	rPK cipher.PubKey,
	rPort uint16,
	req ExecStreamReq,
	stdout, stderr io.Writer,
) (*ExecStreamResult, error) {
	stream, err := dmsgC.DialStream(ctx, dmsg.Addr{PK: rPK, Port: rPort})
	if err != nil {
		return nil, err
	}
	defer func() { _ = stream.Close() }() //nolint:errcheck
	if err := openExecStreamSubsystem(stream); err != nil {
		return nil, err
	}
	return runExecStream(ctx, stream, req, stdout, stderr)
}

// ExecStreamTCP is the direct-TCP equivalent, for hosts reachable on
// Dmsgpty.SshListen without going over the overlay.
func ExecStreamTCP(
	ctx context.Context,
	addr string,
	localPK cipher.PubKey,
	localSK cipher.SecKey,
	rPK cipher.PubKey,
	req ExecStreamReq,
	stdout, stderr io.Writer,
) (*ExecStreamResult, error) {
	conn, err := dialNoiseTCP(ctx, addr, localPK, localSK, rPK)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }() //nolint:errcheck
	if err := openExecStreamSubsystem(conn); err != nil {
		return nil, err
	}
	return runExecStream(ctx, conn, req, stdout, stderr)
}
