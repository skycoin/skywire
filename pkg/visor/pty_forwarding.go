// Package visor pkg/visor/pty_forwarding.go c3-vis-pty
package visor

import (
	"context"
	"net"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/pty"
)

// servePtyOverForwarding offers the pty host, sftp included, on the skynet
// forwarding server at port, so a peer with a direct transport reaches it
// without a route or a dmsg server (the visor bridge's dialDmsgOverSkynet).
// Only direct streams are served: the transport between the two visors
// encrypts them end to end, while a relay splices a stream's bytes in the
// clear between its two legs, and this is a shell.
func servePtyOverForwarding(ctx context.Context, v *Visor, host *pty.Host, port uint16) {
	lis := newConnListener()
	v.services.RegisterHidden(port, "pty", func(conn net.Conn) {
		if oc, ok := conn.(overTransportConn); ok {
			conn = oc.Conn
		}
		if vc, ok := conn.(*vstreamConn); !ok || !vc.Direct() {
			_ = conn.Close() //nolint:errcheck
			return
		}
		if !lis.push(conn) {
			_ = conn.Close() //nolint:errcheck
		}
	})
	go func() {
		<-ctx.Done()
		v.services.Unregister(port)
		_ = lis.Close() //nolint:errcheck
	}()
	go func() {
		_ = host.ListenAndServeNet(ctx, lis, func(c net.Conn) (cipher.PubKey, bool) { //nolint:errcheck
			return remotePKFromForwardingConn(c)
		})
	}()
}

// connListener is a net.Listener fed by push.
type connListener struct {
	ch   chan net.Conn
	done chan struct{}
	once sync.Once
}

func newConnListener() *connListener {
	return &connListener{ch: make(chan net.Conn), done: make(chan struct{})}
}

func (l *connListener) push(c net.Conn) bool {
	select {
	case l.ch <- c:
		return true
	case <-l.done:
		return false
	}
}

func (l *connListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *connListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *connListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }
