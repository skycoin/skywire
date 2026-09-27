//go:build js && wasm

// Package deskhost pkg/wasmhv/deskhost/desk_rpc_js.go c4-wasm-desk
//
// The tab visor's API as the desk's windows reach it: over vnet to its RPC
// port, the methods `skywire cli` calls, rather than a CLI process per click.
package deskhost

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/0magnet/bottle/vnet"

	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// visorRPCAddr is the tab visor's RPC port on vnet.
const visorRPCAddr = "127.0.0.1:3435"

// visorRPC is one window's connection to the tab visor, dialed on first use
// and again after a connection failure.
type visorRPC struct {
	timeout time.Duration // per call

	mu   sync.Mutex
	conn net.Conn
	rpc  visorapi.API
}

func (v *visorRPC) api() (visorapi.API, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.rpc != nil {
		return v.rpc, nil
	}
	conn, err := vnet.DialTimeout("tcp", visorRPCAddr, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("visor RPC: %w", err)
	}
	v.conn, v.rpc = conn, visorapi.NewRPCClient(nil, conn, visorapi.RPCPrefix, v.timeout)
	return v.rpc, nil
}

// call runs f against the API and redials next time if the connection
// broke. An error the visor returns is only an error, not a broken
// connection.
func (v *visorRPC) call(f func(visorapi.API) error) error {
	a, err := v.api()
	if err != nil {
		return err
	}
	err = f(a)
	var netErr net.Error
	if err != nil && (errors.As(err, &netErr) || strings.Contains(err.Error(), "shut down") || strings.Contains(err.Error(), "EOF")) {
		v.dropRPC()
	}
	return err
}

func (v *visorRPC) dropRPC() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.conn != nil {
		_ = v.conn.Close() //nolint:errcheck
	}
	v.conn, v.rpc = nil, nil
}
