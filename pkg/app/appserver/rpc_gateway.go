package appserver

import (
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/idmanager"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
)

// RPCIOErr is used to return an error coming from network stack.
//
// Since client is implemented as an RPC client, we need to correctly
// pass all kinds of network errors from gateway back to the client.
// `net.Error` is an interface, so we can't pass it directly, we have to
// disassemble error on the server side and reassemble it back on the
// client side.
type RPCIOErr struct {
	Text           string
	IsNetErr       bool
	IsTimeoutErr   bool
	IsTemporaryErr bool
}

// ToError converts `*RPCIOErr` to `error`.
func (e *RPCIOErr) ToError() error {
	if e == nil {
		return nil
	}

	if !e.IsNetErr {
		switch e.Text {
		case io.EOF.Error():
			return io.EOF
		case io.ErrClosedPipe.Error():
			return io.ErrClosedPipe
		case io.ErrUnexpectedEOF.Error():
			return io.ErrUnexpectedEOF
		default:
			return errors.New(e.Text)
		}
	}

	return &netErr{
		err:       errors.New(e.Text),
		timeout:   e.IsTimeoutErr,
		temporary: e.IsTemporaryErr,
	}
}

// RPCGateway is a RPC interface for the app server.
type RPCGateway struct {
	lm  *idmanager.Manager // contains listeners associated with their IDs
	cm  *idmanager.Manager // contains connections associated with their IDs
	log *logging.Logger
}

// NewRPCGateway constructs new server RPC interface.
func NewRPCGateway(log *logging.Logger) *RPCGateway {
	return &RPCGateway{
		lm:  idmanager.New(),
		cm:  idmanager.New(),
		log: log,
	}
}

// DialResp contains response parameters for `Dial`.
type DialResp struct {
	ConnID    uint16
	LocalPort routing.Port
}

// Dial dials to the remote.
func (r *RPCGateway) Dial(remote *appnet.Addr, resp *DialResp) (err error) {
	defer rpcutil.LogCall(r.log, "Dial", remote)(resp, &err)

	reservedConnID, free, err := r.cm.ReserveNextID()
	if err != nil {
		return err
	}

	conn, err := appnet.Dial(*remote)
	if err != nil {
		free()
		return err
	}

	wrappedConn, err := appnet.WrapConn(conn)
	if err != nil {
		free()
		return err
	}

	if err := r.cm.Set(*reservedConnID, wrappedConn); err != nil {
		if cErr := wrappedConn.Close(); cErr != nil {
			r.log.WithError(cErr).Error("Error closing wrappedConn.")
		}
		free()
		return err
	}

	localAddr := wrappedConn.LocalAddr().(appnet.Addr)

	resp.ConnID = *reservedConnID
	resp.LocalPort = localAddr.Port

	return nil
}

// Listen starts listening.
func (r *RPCGateway) Listen(local *appnet.Addr, lisID *uint16) (err error) {
	defer rpcutil.LogCall(r.log, "Listen", local)(lisID, &err)

	nextLisID, free, err := r.lm.ReserveNextID()
	if err != nil {
		return err
	}

	l, err := appnet.Listen(*local)
	if err != nil {
		free()
		return err
	}

	if err := r.lm.Set(*nextLisID, l); err != nil {
		if cErr := l.Close(); cErr != nil {
			r.log.WithError(cErr).Error("Error closing listener.")
		}
		free()
		return err
	}

	*lisID = *nextLisID
	return nil
}

// AcceptResp contains response parameters for `Accept`.
type AcceptResp struct {
	Remote appnet.Addr
	ConnID uint16
}

// Accept accepts connection from the listener specified by `lisID`.
func (r *RPCGateway) Accept(lisID *uint16, resp *AcceptResp) (err error) {
	defer rpcutil.LogCall(r.log, "Accept", lisID)(resp, &err)

	log := r.log.WithField("func", "Accept")

	log.Debug("Getting listener...")
	lis, err := r.getListener(*lisID)
	if err != nil {
		return err
	}

	log.Debug("Reserving next ID...")
	connID, free, err := r.cm.ReserveNextID()
	if err != nil {
		return err
	}

	log.Debug("Accepting conn...")
	conn, err := lis.Accept()
	if err != nil {
		free()
		return err
	}

	log.Debug("Wrapping conn...")
	wrappedConn, err := appnet.WrapConn(conn)
	if err != nil {
		free()
		return err
	}

	if err := r.cm.Set(*connID, wrappedConn); err != nil {
		if cErr := wrappedConn.Close(); cErr != nil {
			r.log.WithError(cErr).Error("Failed to close wrappedConn.")
		}
		free()
		return err
	}

	remote := wrappedConn.RemoteAddr().(appnet.Addr)

	resp.Remote = remote
	resp.ConnID = *connID

	return nil
}

// WriteReq contains arguments for `Write`.
type WriteReq struct {
	ConnID uint16
	B      []byte
}

// WriteResp contains response parameters for `Write`.
type WriteResp struct {
	N   int
	Err *RPCIOErr
}

// Write writes to the connection.
func (r *RPCGateway) Write(req *WriteReq, resp *WriteResp) error {
	conn, err := r.getConn(req.ConnID)
	if err != nil {
		return err
	}

	resp.N, err = conn.Write(req.B)
	resp.Err = ioErrToRPCIOErr(err)

	// avoid error in RPC pipeline, error is included in response body
	return nil
}

// ReadReq contains arguments for `Read`.
type ReadReq struct {
	ConnID uint16
	BufLen int
}

// ReadResp contains response parameters for `Read`.
type ReadResp struct {
	B   []byte
	N   int
	Err *RPCIOErr
}

// Read reads data from connection specified by `connID`.
func (r *RPCGateway) Read(req *ReadReq, resp *ReadResp) error {
	conn, err := r.getConn(req.ConnID)
	if err != nil {
		return err
	}

	buf := make([]byte, req.BufLen)

	resp.N, err = conn.Read(buf)
	if resp.N != 0 {
		resp.B = make([]byte, resp.N)
		copy(resp.B, buf[:resp.N])
	}

	fmt.Printf("ERROR READING FROM APP CONN SERVER SIDE: %v\n", err)

	resp.Err = ioErrToRPCIOErr(err)

	// avoid error in RPC pipeline, error is included in response body
	return nil
}

// CloseConn closes connection specified by `connID`.
func (r *RPCGateway) CloseConn(connID *uint16, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "CloseConn", connID)(nil, &err)

	conn, err := r.popConn(*connID)
	if err != nil {
		return err
	}

	return conn.Close()
}

// CloseListener closes listener specified by `lisID`.
func (r *RPCGateway) CloseListener(lisID *uint16, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "CloseConn", lisID)(nil, &err)

	lis, err := r.popListener(*lisID)
	if err != nil {
		return err
	}

	return lis.Close()
}

// DeadlineReq contains arguments for deadline methods.
type DeadlineReq struct {
	ConnID   uint16
	Deadline time.Time
}

// SetDeadline sets deadline for connection specified by `connID`.
func (r *RPCGateway) SetDeadline(req *DeadlineReq, _ *struct{}) error {
	conn, err := r.getConn(req.ConnID)
	if err != nil {
		return err
	}

	return conn.SetDeadline(req.Deadline)
}

// SetReadDeadline sets read deadline for connection specified by `connID`.
func (r *RPCGateway) SetReadDeadline(req *DeadlineReq, _ *struct{}) error {
	conn, err := r.getConn(req.ConnID)
	if err != nil {
		return err
	}

	return conn.SetReadDeadline(req.Deadline)
}

// SetWriteDeadline sets read deadline for connection specified by `connID`.
func (r *RPCGateway) SetWriteDeadline(req *DeadlineReq, _ *struct{}) error {
	conn, err := r.getConn(req.ConnID)
	if err != nil {
		return err
	}

	return conn.SetWriteDeadline(req.Deadline)
}

// popListener gets listener from the manager by `lisID` and removes it.
// Handles type assertion.
func (r *RPCGateway) popListener(lisID uint16) (net.Listener, error) {
	lisIfc, err := r.lm.Pop(lisID)
	if err != nil {
		return nil, fmt.Errorf("no listener: %v", err)
	}

	return idmanager.AssertListener(lisIfc)
}

// popConn gets conn from the manager by `connID` and removes it.
// Handles type assertion.
func (r *RPCGateway) popConn(connID uint16) (net.Conn, error) {
	connIfc, err := r.cm.Pop(connID)
	if err != nil {
		return nil, fmt.Errorf("no conn: %v", err)
	}

	return idmanager.AssertConn(connIfc)
}

// getListener gets listener from the manager by `lisID`. Handles type assertion.
func (r *RPCGateway) getListener(lisID uint16) (net.Listener, error) {
	lisIfc, ok := r.lm.Get(lisID)
	if !ok {
		return nil, fmt.Errorf("no listener with key %d", lisID)
	}

	return idmanager.AssertListener(lisIfc)
}

// getConn gets conn from the manager by `connID`. Handles type assertion.
func (r *RPCGateway) getConn(connID uint16) (net.Conn, error) {
	connIfc, ok := r.cm.Get(connID)
	if !ok {
		return nil, fmt.Errorf("no conn with key %d", connID)
	}

	return idmanager.AssertConn(connIfc)
}

func ioErrToRPCIOErr(err error) *RPCIOErr {
	if err == nil {
		return nil
	}

	rpcIOErr := &RPCIOErr{
		Text: err.Error(),
	}

	if netErr, ok := err.(net.Error); ok {
		rpcIOErr.IsNetErr = true
		rpcIOErr.IsTimeoutErr = netErr.Timeout()
		rpcIOErr.IsTemporaryErr = netErr.Temporary()
	}

	return rpcIOErr
}
