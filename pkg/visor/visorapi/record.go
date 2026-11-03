// Package visorapi pkg/visor/visorapi/record.go c3-vis-core
package visorapi

import (
	"bytes"
	"errors"
	"io"
	"sync"

	rpc "github.com/0magnet/gobrpc"

	"github.com/skycoin/skywire/pkg/logging"
)

// ErrNotDeferrable is a call to an offline visor that cannot wait for it.
var ErrNotDeferrable = errors.New("the visor is offline and this cannot wait for it")

// CallRecorder keeps the calls made through a recording client as gob RPC
// request frames, each one complete, so the visor can serve it later.
type CallRecorder struct {
	allow  func(method string) bool
	mu     sync.Mutex
	frames [][]byte
}

// NewRecordingRPCClient returns an API that records the calls allow accepts
// instead of sending them, and refuses the rest with ErrNotDeferrable. Every
// recorded call returns a zero reply.
func NewRecordingRPCClient(allow func(method string) bool) (API, *CallRecorder) {
	rec := &CallRecorder{allow: allow}
	return &rpcClient{log: logging.MustGetLogger("visor_rpc_record"), rec: rec, prefix: RPCPrefix}, rec
}

// Frames returns the recorded request frames in call order.
func (r *CallRecorder) Frames() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]byte(nil), r.frames...)
}

func (r *CallRecorder) record(prefix, method string, args any) error {
	if !r.allow(method) {
		return ErrNotDeferrable
	}
	conn := &captureConn{closed: make(chan struct{})}
	c := rpc.NewClient(conn)
	call := c.Go(prefix+"."+method, args, new(struct{}), make(chan *rpc.Call, 1))
	_ = c.Close() //nolint:errcheck
	select {
	case done := <-call.Done:
		if done.Error != nil && !errors.Is(done.Error, rpc.ErrShutdown) && !errors.Is(done.Error, io.ErrUnexpectedEOF) {
			return done.Error
		}
	default:
	}
	r.mu.Lock()
	r.frames = append(r.frames, conn.buf.Bytes())
	r.mu.Unlock()
	return nil
}

// captureConn keeps what is written and reads nothing until it is closed.
type captureConn struct {
	buf    bytes.Buffer
	once   sync.Once
	closed chan struct{}
}

func (c *captureConn) Write(p []byte) (int, error) { return c.buf.Write(p) }

func (c *captureConn) Read([]byte) (int, error) {
	<-c.closed
	return 0, io.EOF
}

func (c *captureConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}
