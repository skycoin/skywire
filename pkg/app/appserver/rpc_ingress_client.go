// Package appserver pkg/app/appserver/rpc_ingress_client.go
package appserver

import (
	"fmt"
	"net/rpc"
	"time"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/routing"
)

//go:generate mockery -name RPCIngressClient -case underscore -inpkg

// RPCIngressClient describes RPC interface to communicate with the server.
type RPCIngressClient interface {
	SetDetailedStatus(status string) error
	SetConnectionDuration(dur int64) error
	SetError(appErr string) error
	SetAppPort(appPort routing.Port) error
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

// rpcIngressClient implements `RPCIngressClient`.
type rpcIngressClient struct {
	rpc     *rpc.Client
	procKey appcommon.ProcKey
}

// NewRPCIngressClient constructs new `rpcIngressClient`.
func NewRPCIngressClient(rpc *rpc.Client, procKey appcommon.ProcKey) RPCIngressClient {
	return &rpcIngressClient{
		rpc:     rpc,
		procKey: procKey,
	}
}

// SetDetailedStatus sets detailed status of an app.
func (c *rpcIngressClient) SetDetailedStatus(status string) error {
	return c.rpc.Call(c.formatMethod("SetDetailedStatus"), &status, nil)
}

// SetConnectionDuration sets the connection duration for an app
func (c *rpcIngressClient) SetConnectionDuration(dur int64) error {
	return c.rpc.Call(c.formatMethod("SetConnectionDuration"), dur, nil)
}

// SetError sets error of an app.
func (c *rpcIngressClient) SetError(appErr string) error {
	return c.rpc.Call(c.formatMethod("SetError"), &appErr, nil)
}

// SetAppPort sets port of an app.
func (c *rpcIngressClient) SetAppPort(port routing.Port) error {
	return c.rpc.Call(c.formatMethod("SetAppPort"), &port, nil)
}

// RPCErr is used to preserve the type of the errors we return via RPC
type RPCErr struct {
	Err string
}

func (e RPCErr) Error() string {
	return e.Err
}

// Dial sends `Dial` command to the server.
func (c *rpcIngressClient) Dial(remote appnet.Addr) (connID uint16, localPort routing.Port, err error) {
	var resp DialResp
	if err := c.rpc.Call(c.formatMethod("Dial"), &remote, &resp); err != nil {
		return 0, 0, RPCErr{err.Error()}
	}

	return resp.ConnID, resp.LocalPort, nil
}

// Listen sends `Listen` command to the server.
func (c *rpcIngressClient) Listen(local appnet.Addr) (uint16, error) {
	var lisID uint16
	if err := c.rpc.Call(c.formatMethod("Listen"), &local, &lisID); err != nil {
		return 0, err
	}

	return lisID, nil
}

// Accept sends `Accept` command to the server.
func (c *rpcIngressClient) Accept(lisID uint16) (connID uint16, remote appnet.Addr, err error) {
	var acceptResp AcceptResp
	if err := c.rpc.Call(c.formatMethod("Accept"), &lisID, &acceptResp); err != nil {
		return 0, appnet.Addr{}, err
	}

	return acceptResp.ConnID, acceptResp.Remote, nil
}

// Write sends `Write` command to the server.
func (c *rpcIngressClient) Write(connID uint16, b []byte) (int, error) {
	req := WriteReq{
		ConnID: connID,
		B:      b,
	}

	var resp WriteResp
	if err := c.rpc.Call(c.formatMethod("Write"), &req, &resp); err != nil {
		return 0, err
	}

	return resp.N, resp.Err.ToError()
}

// Read sends `Read` command to the server.
func (c *rpcIngressClient) Read(connID uint16, b []byte) (int, error) {
	req := ReadReq{
		ConnID: connID,
		BufLen: len(b),
	}

	var resp ReadResp
	if err := c.rpc.Call(c.formatMethod("Read"), &req, &resp); err != nil {
		return 0, err
	}

	if resp.N != 0 {
		copy(b[:resp.N], resp.B[:resp.N])
	}

	return resp.N, resp.Err.ToError()
}

// CloseConn sends `CloseConn` command to the server.
func (c *rpcIngressClient) CloseConn(id uint16) error {
	return c.rpc.Call(c.formatMethod("CloseConn"), &id, nil)
}

// CloseListener sends `CloseListener` command to the server.
func (c *rpcIngressClient) CloseListener(id uint16) error {
	return c.rpc.Call(c.formatMethod("CloseListener"), &id, nil)
}

// SetDeadline sends `SetDeadline` command to the server.
func (c *rpcIngressClient) SetDeadline(id uint16, t time.Time) error {
	req := DeadlineReq{
		ConnID:   id,
		Deadline: t,
	}

	return c.rpc.Call(c.formatMethod("SetDeadline"), &req, nil)
}

// SetReadDeadline sends `SetReadDeadline` command to the server.
func (c *rpcIngressClient) SetReadDeadline(id uint16, t time.Time) error {
	req := DeadlineReq{
		ConnID:   id,
		Deadline: t,
	}

	return c.rpc.Call(c.formatMethod("SetReadDeadline"), &req, nil)
}

// SetWriteDeadline sends `SetWriteDeadline` command to the server.
func (c *rpcIngressClient) SetWriteDeadline(id uint16, t time.Time) error {
	req := DeadlineReq{
		ConnID:   id,
		Deadline: t,
	}

	return c.rpc.Call(c.formatMethod("SetWriteDeadline"), &req, nil)
}

// formatMethod formats complete RPC method signature.
func (c *rpcIngressClient) formatMethod(method string) string {
	const methodFmt = "%s.%s"
	return fmt.Sprintf(methodFmt, c.procKey.String(), method)
}
