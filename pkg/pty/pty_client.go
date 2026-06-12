// Package pty pkg/pty/pty_client.go
package pty

import (
	"fmt"
	"io"
	"net/rpc"
	"sync"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// PtyClient represents the client end of a dmsgpty session.
type PtyClient struct {
	log  logrus.FieldLogger
	rpcC *rpc.Client
	done chan struct{}
	once sync.Once

	// writeDone receives completed WriteAsync RPC calls so a later
	// WriteAsync can surface a prior write's error, instead of silently
	// dropping keystrokes until the Read path eventually notices the
	// broken conn. Buffered; net/rpc drops replies (with a log line)
	// rather than crashing if it ever fills.
	writeDone chan *rpc.Call
}

// NewPtyClient creates a new pty client that interacts with a local pty.
func NewPtyClient(conn io.ReadWriteCloser) (*PtyClient, error) {
	if err := writeRequest(conn, PtyURI); err != nil {
		return nil, err
	}
	if err := readResponse(conn); err != nil {
		return nil, err
	}
	return &PtyClient{
		log:       logging.MustGetLogger("pty:pty-client"),
		rpcC:      rpc.NewClient(conn),
		done:      make(chan struct{}),
		writeDone: make(chan *rpc.Call, 64),
	}, nil
}

// NewProxyClient creates a new pty client that interacts with a remote pty hosted on the given dmsg pk and port.
// Interactions are proxied via the local pty.Host
func NewProxyClient(conn io.ReadWriteCloser, rPK cipher.PubKey, rPort uint16) (*PtyClient, error) {
	uri := fmt.Sprintf("%s?pk=%s&port=%d", PtyProxyURI, rPK, rPort)
	if err := writeRequest(conn, uri); err != nil {
		return nil, err
	}
	if err := readResponse(conn); err != nil {
		return nil, err
	}
	return &PtyClient{
		log:       logging.MustGetLogger("pty:proxy-client"),
		rpcC:      rpc.NewClient(conn),
		done:      make(chan struct{}),
		writeDone: make(chan *rpc.Call, 64),
	}, nil
}

// Close closes the pty and closes the connection to the remote.
func (sc *PtyClient) Close() error {
	if closed := sc.close(); !closed {
		return nil
	}
	// No need to wait for reply.
	_ = sc.Stop() //nolint:errcheck
	return sc.rpcC.Close()
}

func (sc *PtyClient) close() (closed bool) {
	sc.once.Do(func() {
		close(sc.done)
		closed = true
	})
	return closed
}

// Stop stops the pty.
func (sc *PtyClient) Stop() error {
	return sc.call("Stop", &empty, &empty)
}

// Read reads from the pty.
func (sc *PtyClient) Read(b []byte) (int, error) {
	reqN := len(b)
	var respB []byte
	err := sc.call("Read", &reqN, &respB)
	return copy(b, respB), processRPCError(err)
}

// Write writes to the pty.
func (sc *PtyClient) Write(b []byte) (int, error) {
	var n int
	err := sc.call("Write", &b, &n)
	return n, processRPCError(err)
}

// WriteAsync writes to the pty WITHOUT waiting for the RPC reply, so an
// interactive caller (the web terminal's input loop) isn't blocked a full
// round-trip per keystroke. Ordering is preserved — net/rpc serializes
// request sends, and the send encodes b before returning, so the caller may
// reuse the buffer.
//
// It returns an error if the client is closed OR if a PRIOR async write has
// since failed: completed Write calls land on sc.writeDone, and each call
// reaps them first, so a broken conn surfaces on the input path within a
// keystroke or two instead of only when the Read path notices it.
func (sc *PtyClient) WriteAsync(b []byte) error {
	select {
	case <-sc.done:
		return io.ErrClosedPipe
	default:
	}
	// Reap completed prior writes; report the first error encountered.
	for {
		select {
		case call := <-sc.writeDone:
			if call.Error != nil {
				return processRPCError(call.Error)
			}
			continue
		default:
		}
		break
	}
	sc.rpcC.Go(sc.rpcMethod("Write"), &b, new(int), sc.writeDone)
	return nil
}

func (*PtyClient) rpcMethod(m string) string {
	return PtyRPCName + "." + m
}

func (sc *PtyClient) call(method string, args, reply interface{}) error {
	call := sc.rpcC.Go(sc.rpcMethod(method), args, reply, nil)
	select {
	case <-sc.done:
		return io.ErrClosedPipe
	case <-call.Done:
		return call.Error
	}
}

// Start starts the pty with optional environment variables.
func (sc *PtyClient) Start(name string, env []string, arg ...string) error {
	return sc.call("Start", &CommandReq{
		Name: name,
		Arg:  arg,
		Size: nil,
		Env:  env,
	}, &empty)
}

// StartSession starts the pty like Start but returns a persistent-session id the
// caller can stash and later Attach to after a reconnect. Only persistence-aware
// hosts expose it; on an older host the RPC returns a "method not found" error
// (processRPCError surfaces it) and the caller should fall back to Start, giving
// up reconnect support against that host. An empty id with nil error means the
// host ran StartSession but isn't tracking a session (shouldn't happen).
func (sc *PtyClient) StartSession(name string, env []string, arg ...string) (string, error) {
	var sid string
	err := sc.call("StartSession", &CommandReq{Name: name, Arg: arg, Env: env}, &sid)
	return sid, processRPCError(err)
}

// Attach rebinds to an existing persistent session by id (from a prior
// StartSession), replaying output produced while the client was disconnected.
// Returns an error if the session was reaped or is owned by another key, in
// which case the caller should start a fresh session.
func (sc *PtyClient) Attach(sid string) error {
	return processRPCError(sc.call("Attach", &sid, &empty))
}

// Exec runs a one-shot command on the remote host without a PTY.
// Same trust model as Start; see exec_gateway.go for the cap +
// timeout semantics. Returns the captured stdout/stderr/exit-code
// in the response; a non-nil error here means the RPC itself
// failed, NOT that the remote command exited non-zero (check
// resp.ExitCode and resp.TimedOut for that).
func (sc *PtyClient) Exec(req *CommandExecReq) (*CommandExecResult, error) {
	var resp CommandExecResult
	if err := sc.call("Exec", req, &resp); err != nil {
		return nil, processRPCError(err)
	}
	return &resp, nil
}

// execCall is the proxy-side hop used by ProxiedPtyGateway.Exec —
// forwards the request through this client's RPC connection.
func (sc *PtyClient) execCall(req *CommandExecReq, resp *CommandExecResult) error {
	return sc.call("Exec", req, resp)
}

// StartWithSize starts the pty with a specified size and optional environment variables.
func (sc *PtyClient) StartWithSize(name string, arg []string, c *WinSize, env []string) error {
	return sc.call("Start", &CommandReq{Name: name, Arg: arg, Size: c, Env: env}, &empty)
}

// SetPtySize sets the pty size.
func (sc *PtyClient) SetPtySize(size *WinSize) error {
	return sc.call("SetPtySize", size, &empty)
}

// Ping issues a no-op RPC round-trip. The interactive web terminal calls
// it periodically as a keepalive: the response read refreshes the dmsg
// stream's idle read deadline, so a terminal left idle isn't dropped
// after StreamIdleTimeout (2m). The dmsg analog of SSH ServerAliveInterval.
func (sc *PtyClient) Ping() error {
	return sc.call("Ping", &empty, &empty)
}
