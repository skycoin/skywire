// Package appnet pkg/app/appnet/local_service.go c2-vis-appsvc
//
// Local services: in-process handlers that an app on THIS visor reaches over
// the data plane it already holds, with no localhost port and no network hop. A
// dial to the visor's own PK on a registered port is served over net.Pipe.
//
// The resolving proxies are the motivating case. An app wanting to hand a
// `<pk>.skynet` name to the resolver had only one way in: the resolver's SOCKS5
// listener on 127.0.0.1, which means a second proxy hop and a port the operator
// has to configure on both sides.
//
// This is deliberately NOT pkg/visor's ServiceRegistry. That map is the one
// handleServerConn dispatches INCOMING peer connections from, on the sky
// forwarding ports, where the CALLER names the port it wants — so membership
// there is mesh reachability, and the registry path returns before the
// per-port PK whitelist below it. A resolving proxy must never be reachable
// that way: a peer would be resolving this visor's alias table through its
// MITM CA and leaving through its exit. A local service has no port on the
// forwarding server, so a peer has nothing to name.
package appnet

import (
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// LocalHandler serves one connection and owns it for its lifetime: it reads
// requests, writes responses, and closes the conn when done.
type LocalHandler func(conn net.Conn)

// ErrNoLocalService is returned by DialLocalService for an address with no
// handler registered on it.
var ErrNoLocalService = errors.New("no local service registered on that address")

// localKey identifies a local service. The address family is deliberately not
// part of the key: an app reaches its own visor the same way whichever of
// skynet / dmsg it happens to address it by, and a local service is served
// before either carrier is involved.
type localKey struct {
	pk   cipher.PubKey
	port routing.Port
}

type localService struct {
	label   string
	handler LocalHandler
}

// nolint: gochecknoglobals
var (
	localServices   = make(map[localKey]localService)
	localServicesMx sync.RWMutex
)

// RegisterLocalService makes handler reachable to apps on this visor that dial
// pk:port, where pk is this visor's own public key. Overwrites any handler on
// the same address — matching AddNetworker's last-writer-wins semantics, which
// a restarted resolver relies on.
//
// label names the service in logs and errors.
func RegisterLocalService(pk cipher.PubKey, port routing.Port, label string, handler LocalHandler) {
	localServicesMx.Lock()
	localServices[localKey{pk: pk, port: port}] = localService{label: label, handler: handler}
	localServicesMx.Unlock()
}

// UnregisterLocalService removes the handler on pk:port. No-op if none.
func UnregisterLocalService(pk cipher.PubKey, port routing.Port) {
	localServicesMx.Lock()
	delete(localServices, localKey{pk: pk, port: port})
	localServicesMx.Unlock()
}

// ClearLocalServices removes every registration. Mirrors ClearNetworkers, for
// tests.
func ClearLocalServices() {
	localServicesMx.Lock()
	localServices = make(map[localKey]localService)
	localServicesMx.Unlock()
}

// LocalServiceLabel returns the label registered on addr, and whether addr
// names a local service at all.
func LocalServiceLabel(addr Addr) (string, bool) {
	localServicesMx.RLock()
	svc, ok := localServices[localKey{pk: addr.PubKey, port: addr.Port}]
	localServicesMx.RUnlock()
	return svc.label, ok
}

// HasLocalService reports whether addr names a registered local service.
func HasLocalService(addr Addr) bool {
	_, ok := LocalServiceLabel(addr)
	return ok
}

// DialLocalService serves addr in-process over net.Pipe: the handler runs on
// its own goroutine with the server end, and the client end is returned. No
// transport, no route, no listener.
func DialLocalService(addr Addr) (net.Conn, error) {
	localServicesMx.RLock()
	svc, ok := localServices[localKey{pk: addr.PubKey, port: addr.Port}]
	localServicesMx.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoLocalService, addr)
	}

	clientEnd, serverEnd := net.Pipe()

	// The handler's conn reports the service's own address as its peer. A
	// handler that reads a caller identity off RemoteAddr — the dmsghttp
	// landing page takes its PK whitelist decision that way — then sees this
	// visor's own PK, which is the truth: the caller is an app on this visor,
	// and local access through one's own service is inherently authorized.
	go svc.handler(&localConn{Conn: serverEnd, local: addr, remote: addr})

	// The client end has no routing port of its own because no route group
	// exists to name. Port 0 lands in the dial response's LocalPort, which is
	// only ever used to name a route group back to the visor.
	return &localConn{
		Conn:   clientEnd,
		local:  Addr{Net: addr.Net, PubKey: addr.PubKey, Port: 0},
		remote: addr,
	}, nil
}

// localConn gives a net.Pipe conn the addresses the app plane expects: WrapConn
// converts both ends through ConvertAddr, which has no case for pipeAddr.
type localConn struct {
	net.Conn
	local  Addr
	remote Addr
}

// LocalAddr returns the app-side address of the pipe.
func (c *localConn) LocalAddr() net.Addr { return c.local }

// RemoteAddr returns the address of the local service being served.
func (c *localConn) RemoteAddr() net.Addr { return c.remote }
