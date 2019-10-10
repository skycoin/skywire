package appserver

import (
	"fmt"
	"net"

	"github.com/pkg/errors"
	"github.com/skycoin/skywire/pkg/app2/idmanager"

	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/app2/appnet"
	"github.com/skycoin/skywire/pkg/routing"
)

// RPCGateway is a RPC interface for the app server.
type RPCGateway struct {
	lm  *idmanager.Manager // contains listeners associated with their IDs
	cm  *idmanager.Manager // contains connections associated with their IDs
	log *logging.Logger
}

// newRPCGateway constructs new server RPC interface.
func newRPCGateway(log *logging.Logger) *RPCGateway {
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
func (r *RPCGateway) Dial(remote *appnet.Addr, resp *DialResp) error {
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
		if err := wrappedConn.Close(); err != nil {
			r.log.WithError(err).Error("error closing conn")
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
func (r *RPCGateway) Listen(local *appnet.Addr, lisID *uint16) error {
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
		if err := l.Close(); err != nil {
			r.log.WithError(err).Error("error closing listener")
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
func (r *RPCGateway) Accept(lisID *uint16, resp *AcceptResp) error {
	lis, err := r.getListener(*lisID)
	if err != nil {
		return err
	}

	connID, free, err := r.cm.ReserveNextID()
	if err != nil {
		return err
	}

	conn, err := lis.Accept()
	if err != nil {
		free()
		return err
	}

	wrappedConn, err := appnet.WrapConn(conn)
	if err != nil {
		free()
		return err
	}

	if err := r.cm.Set(*connID, wrappedConn); err != nil {
		if err := wrappedConn.Close(); err != nil {
			r.log.WithError(err).Error("error closing DMSG transport")
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

// Write writes to the connection.
func (r *RPCGateway) Write(req *WriteReq, n *int) error {
	conn, err := r.getConn(req.ConnID)
	if err != nil {
		return err
	}

	*n, err = conn.Write(req.B)
	if err != nil {
		return err
	}

	return nil
}

// ReadReq contains arguments for `Read`.
type ReadReq struct {
	ConnID uint16
	BufLen int
}

// ReadResp contains response parameters for `Read`.
type ReadResp struct {
	B []byte
	N int
}

// Read reads data from connection specified by `connID`.
func (r *RPCGateway) Read(req *ReadReq, resp *ReadResp) error {
	conn, err := r.getConn(req.ConnID)
	if err != nil {
		return err
	}

	buf := make([]byte, req.BufLen)
	resp.N, err = conn.Read(buf)
	if err != nil {
		return err
	}

	resp.B = make([]byte, resp.N)
	copy(resp.B, buf[:resp.N])

	return nil
}

// CloseConn closes connection specified by `connID`.
func (r *RPCGateway) CloseConn(connID *uint16, _ *struct{}) error {
	conn, err := r.popConn(*connID)
	if err != nil {
		return err
	}

	return conn.Close()
}

// CloseListener closes listener specified by `lisID`.
func (r *RPCGateway) CloseListener(lisID *uint16, _ *struct{}) error {
	lis, err := r.popListener(*lisID)
	if err != nil {
		return err
	}

	return lis.Close()
}

// popListener gets listener from the manager by `lisID` and removes it.
// Handles type assertion.
func (r *RPCGateway) popListener(lisID uint16) (net.Listener, error) {
	lisIfc, err := r.lm.Pop(lisID)
	if err != nil {
		return nil, errors.Wrap(err, "no listener")
	}

	return idmanager.AssertListener(lisIfc)
}

// popConn gets conn from the manager by `connID` and removes it.
// Handles type assertion.
func (r *RPCGateway) popConn(connID uint16) (net.Conn, error) {
	connIfc, err := r.cm.Pop(connID)
	if err != nil {
		return nil, errors.Wrap(err, "no conn")
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
