package app

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/rpc"
	"time"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appcommon"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appnet"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appserver"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
)

//go:generate mockery -name RPCClient -case underscore -inpkg

// RPCClient describes RPC interface to communicate with the server.
type RPCClient interface {
	Dial(remote appnet.Addr) (connID uint16, localPort routing.Port, err error)
	Listen(local appnet.Addr) (uint16, error)
	Accept(lisID uint16) (connID uint16, remote appnet.Addr, err error)
	Write(connID uint16, b []byte) (int, error)
	Read(connID uint16, b []byte) (int, error)
	CloseConn(id uint16) error
	CloseListener(id uint16) error
	SetDeadline(connID uint16, d time.Time) error
	SetReadDeadline(connID uint16, d time.Time) error
	SetWriteDeadline(connID uint16, d time.Time) error
}

type netErr struct {
	err       error
	timeout   bool
	temporary bool
}

func (e *netErr) Error() string {
	return e.err.Error()
}

func (e *netErr) Timeout() bool {
	return e.timeout
}

func (e *netErr) Temporary() bool {
	return e.temporary
}

func wrapNetErr(err error, timeout, temporary bool) net.Error {
	return &netErr{
		err:       err,
		timeout:   timeout,
		temporary: temporary,
	}
}

// rpcClient implements `RPCClient`.
type rpcClient struct {
	rpc    *rpc.Client
	appKey appcommon.Key
}

// NewRPCClient constructs new `rpcClient`.
func NewRPCClient(rpc *rpc.Client, appKey appcommon.Key) RPCClient {
	return &rpcClient{
		rpc:    rpc,
		appKey: appKey,
	}
}

// Dial sends `Dial` command to the server.
func (c *rpcClient) Dial(remote appnet.Addr) (connID uint16, localPort routing.Port, err error) {
	var resp appserver.DialResp
	if err := c.rpc.Call(c.formatMethod("Dial"), &remote, &resp); err != nil {
		return 0, 0, err
	}

	return resp.ConnID, resp.LocalPort, nil
}

// Listen sends `Listen` command to the server.
func (c *rpcClient) Listen(local appnet.Addr) (uint16, error) {
	var lisID uint16
	if err := c.rpc.Call(c.formatMethod("Listen"), &local, &lisID); err != nil {
		return 0, err
	}

	return lisID, nil
}

// Accept sends `Accept` command to the server.
func (c *rpcClient) Accept(lisID uint16) (connID uint16, remote appnet.Addr, err error) {
	var acceptResp appserver.AcceptResp
	if err := c.rpc.Call(c.formatMethod("Accept"), &lisID, &acceptResp); err != nil {
		return 0, appnet.Addr{}, err
	}

	return acceptResp.ConnID, acceptResp.Remote, nil
}

// Write sends `Write` command to the server.
func (c *rpcClient) Write(connID uint16, b []byte) (int, error) {
	req := appserver.WriteReq{
		ConnID: connID,
		B:      b,
	}

	var resp appserver.WriteResp
	if err := c.rpc.Call(c.formatMethod("Write"), &req, &resp); err != nil {
		return 0, err
	}

	var err error
	if resp.Err != "" {
		if resp.IsNetErr {
			err = wrapNetErr(errors.New(resp.Err), resp.IsTimeoutErr, resp.IsTemporaryErr)
		} else {
			switch resp.Err {
			case io.EOF.Error():
				err = io.EOF
			case io.ErrClosedPipe.Error():
				err = io.ErrClosedPipe
			case io.ErrUnexpectedEOF.Error():
				err = io.ErrUnexpectedEOF
			default:
				err = errors.New(resp.Err)
			}
		}
	}

	return resp.N, err
}

// Read sends `Read` command to the server.
func (c *rpcClient) Read(connID uint16, b []byte) (int, error) {
	req := appserver.ReadReq{
		ConnID: connID,
		BufLen: len(b),
	}

	var resp appserver.ReadResp
	if err := c.rpc.Call(c.formatMethod("Read"), &req, &resp); err != nil {
		return 0, err
	}

	if resp.N != 0 {
		copy(b[:resp.N], resp.B[:resp.N])
	}

	var err error
	if resp.Err != "" {
		if resp.IsNetErr {
			err = wrapNetErr(errors.New(resp.Err), resp.IsTimeoutErr, resp.IsTemporaryErr)
		} else {
			switch resp.Err {
			case io.EOF.Error():
				err = io.EOF
			case io.ErrClosedPipe.Error():
				err = io.ErrClosedPipe
			case io.ErrUnexpectedEOF.Error():
				err = io.ErrUnexpectedEOF
			default:
				err = errors.New(resp.Err)
			}
		}
	}

	return resp.N, err
}

// CloseConn sends `CloseConn` command to the server.
func (c *rpcClient) CloseConn(id uint16) error {
	return c.rpc.Call(c.formatMethod("CloseConn"), &id, nil)
}

// CloseListener sends `CloseListener` command to the server.
func (c *rpcClient) CloseListener(id uint16) error {
	return c.rpc.Call(c.formatMethod("CloseListener"), &id, nil)
}

// SetDeadline sends `SetDeadline` command to the server.
func (c *rpcClient) SetDeadline(id uint16, t time.Time) error {
	req := appserver.DeadlineReq{
		ConnID:   id,
		Deadline: t,
	}

	return c.rpc.Call(c.formatMethod("SetDeadline"), &req, nil)
}

// SetReadDeadline sends `SetReadDeadline` command to the server.
func (c *rpcClient) SetReadDeadline(id uint16, t time.Time) error {
	req := appserver.DeadlineReq{
		ConnID:   id,
		Deadline: t,
	}

	return c.rpc.Call(c.formatMethod("SetReadDeadline"), &req, nil)
}

// SetWriteDeadline sends `SetWriteDeadline` command to the server.
func (c *rpcClient) SetWriteDeadline(id uint16, t time.Time) error {
	req := appserver.DeadlineReq{
		ConnID:   id,
		Deadline: t,
	}

	return c.rpc.Call(c.formatMethod("SetWriteDeadline"), &req, nil)
}

// formatMethod formats complete RPC method signature.
func (c *rpcClient) formatMethod(method string) string {
	const methodFmt = "%s.%s"
	return fmt.Sprintf(methodFmt, c.appKey, method)
}
