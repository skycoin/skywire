package app2

import (
	"fmt"
	"net/rpc"

	"github.com/skycoin/skywire/pkg/app2/appnet"
	"github.com/skycoin/skywire/pkg/app2/appserver"
	"github.com/skycoin/skywire/pkg/routing"
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
}

// rpcClient implements `RPCClient`.
type rpcCLient struct {
	rpc    *rpc.Client
	appKey appserver.Key
}

// NewRPCClient constructs new `rpcClient`.
func NewRPCClient(rpc *rpc.Client, appKey appserver.Key) RPCClient {
	return &rpcCLient{
		rpc:    rpc,
		appKey: appKey,
	}
}

// Dial sends `Dial` command to the server.
func (c *rpcCLient) Dial(remote appnet.Addr) (connID uint16, localPort routing.Port, err error) {
	var resp appserver.DialResp
	if err := c.rpc.Call(c.formatMethod("Dial"), &remote, &resp); err != nil {
		return 0, 0, err
	}

	return resp.ConnID, resp.LocalPort, nil
}

// Listen sends `Listen` command to the server.
func (c *rpcCLient) Listen(local appnet.Addr) (uint16, error) {
	var lisID uint16
	if err := c.rpc.Call(c.formatMethod("Listen"), &local, &lisID); err != nil {
		return 0, err
	}

	return lisID, nil
}

// Accept sends `Accept` command to the server.
func (c *rpcCLient) Accept(lisID uint16) (connID uint16, remote appnet.Addr, err error) {
	var acceptResp appserver.AcceptResp
	if err := c.rpc.Call(c.formatMethod("Accept"), &lisID, &acceptResp); err != nil {
		return 0, appnet.Addr{}, err
	}

	return acceptResp.ConnID, acceptResp.Remote, nil
}

// Write sends `Write` command to the server.
func (c *rpcCLient) Write(connID uint16, b []byte) (int, error) {
	req := appserver.WriteReq{
		ConnID: connID,
		B:      b,
	}

	var n int
	if err := c.rpc.Call(c.formatMethod("Write"), &req, &n); err != nil {
		return n, err
	}

	return n, nil
}

// Read sends `Read` command to the server.
func (c *rpcCLient) Read(connID uint16, b []byte) (int, error) {
	req := appserver.ReadReq{
		ConnID: connID,
		BufLen: len(b),
	}

	var resp appserver.ReadResp
	if err := c.rpc.Call(c.formatMethod("Read"), &req, &resp); err != nil {
		return 0, err
	}

	copy(b[:resp.N], resp.B[:resp.N])

	return resp.N, nil
}

// CloseConn sends `CloseConn` command to the server.
func (c *rpcCLient) CloseConn(id uint16) error {
	return c.rpc.Call(c.formatMethod("CloseConn"), &id, nil)
}

// CloseListener sends `CloseListener` command to the server.
func (c *rpcCLient) CloseListener(id uint16) error {
	return c.rpc.Call(c.formatMethod("CloseListener"), &id, nil)
}

// formatMethod formats complete RPC method signature.
func (c *rpcCLient) formatMethod(method string) string {
	const methodFmt = "%s.%s"
	return fmt.Sprintf(methodFmt, c.appKey, method)
}
