// Package appnet pkg/app/appnet/wrapped_conn.go
package appnet

import (
	"net"
)

// WrappedConn wraps `net.Conn` to support address conversion between
// specific `net.Addr` implementations and `Addr`.
type WrappedConn struct {
	net.Conn
	local  Addr
	remote Addr
}

// WrapConn wraps passed `conn`. Handles `net.Addr` type assertion.
func WrapConn(conn net.Conn) (net.Conn, error) {
	l, err := ConvertAddr(conn.LocalAddr())
	if err != nil {
		return nil, err
	}

	r, err := ConvertAddr(conn.RemoteAddr())
	if err != nil {
		return nil, err
	}

	return &WrappedConn{
		Conn:   conn,
		local:  l,
		remote: r,
	}, nil
}

// LocalAddr returns local address.
func (c *WrappedConn) LocalAddr() net.Addr {
	return c.local
}

// RemoteAddr returns remote address.
func (c *WrappedConn) RemoteAddr() net.Addr {
	return c.remote
}
