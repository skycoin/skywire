package cliutil

import (
	"context"
	"net"
	"time"

	"github.com/0magnet/bottle/vnet"
)

// loopbackDialTimeout bounds one dial of a SOCKS5 proxy the CLI talks to.
const loopbackDialTimeout = 30 * time.Second

// LoopbackDialer is the forward dialer for reaching a local SOCKS5 proxy. It
// dials through vnet, so inside a browser tab 127.0.0.1:<port> reaches the
// in-tab visor's listener (proxy.Direct would use the host network and be
// refused); on native builds it is net.DialTimeout.
var LoopbackDialer loopbackDialer

type loopbackDialer struct{}

func (loopbackDialer) Dial(network, addr string) (net.Conn, error) {
	return vnet.DialTimeout(network, addr, loopbackDialTimeout)
}

func (d loopbackDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	timeout := loopbackDialTimeout
	if dl, ok := ctx.Deadline(); ok {
		timeout = time.Until(dl)
	}
	return vnet.DialTimeout(network, addr, timeout)
}
