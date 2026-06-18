// Package visor pkg/visor/service_registry.go
//
// ServiceRegistry maps port numbers to connection handlers so the
// sky-forwarding server (and future transports) can dispatch incoming
// connections to the correct service without needing a localhost TCP
// bounce. Each handler is a function that owns a net.Conn for its
// lifetime — it reads requests, writes responses, and closes the
// conn when done.
//
// This replaces the old isPortRegistered / allowed-ports / localhost
// TCP dial pattern. Registration IS the access control: if a port
// isn't in the registry, the forwarding server refuses it. Services
// that shouldn't be exposed (RPC, hypervisor) simply don't register.
package visor

import (
	"fmt"
	"net"
	"net/http"
	"sort"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/visor/logserver"
)

// ConnHandler serves a single connection. The handler owns conn for
// its lifetime — it must close conn when done. Called in its own
// goroutine by the forwarding server.
type ConnHandler func(conn net.Conn)

// ServiceRegistry is a thread-safe port → handler map. The zero
// value is not usable; call NewServiceRegistry.
type ServiceRegistry struct {
	mu       sync.RWMutex
	handlers map[uint16]registeredService
}

type registeredService struct {
	label   string // human-readable name for logging / CLI
	handler ConnHandler
	hidden  bool // hidden from the /services catalog (still forwarded)
}

// NewServiceRegistry returns a ready-to-use registry.
func NewServiceRegistry() *ServiceRegistry {
	return &ServiceRegistry{
		handlers: make(map[uint16]registeredService),
	}
}

// Register adds a handler for port. Overwrites any existing handler
// on the same port (last writer wins — matches the "latest init
// module" semantics of the visor startup sequence).
func (r *ServiceRegistry) Register(port uint16, label string, handler ConnHandler) {
	r.mu.Lock()
	r.handlers[port] = registeredService{label: label, handler: handler}
	r.mu.Unlock()
}

// RegisterHidden adds a handler that is forwarded but NOT shown in
// the /services catalog. Use for services that should be accessible
// to PK-authenticated peers but not advertised publicly.
func (r *ServiceRegistry) RegisterHidden(port uint16, label string, handler ConnHandler) {
	r.mu.Lock()
	r.handlers[port] = registeredService{label: label, handler: handler, hidden: true}
	r.mu.Unlock()
}

// Unregister removes the handler for port. No-op if not registered.
func (r *ServiceRegistry) Unregister(port uint16) {
	r.mu.Lock()
	delete(r.handlers, port)
	r.mu.Unlock()
}

// Get returns the handler for port, or (nil, false) if unregistered.
func (r *ServiceRegistry) Get(port uint16) (ConnHandler, bool) {
	r.mu.RLock()
	svc, ok := r.handlers[port]
	r.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return svc.handler, true
}

// SelfDial returns a client-side net.Conn whose peer is the handler
// registered on port, served in-process over net.Pipe — no transport,
// no dmsg/skynet hop, no loopback. Used by the embedded resolving
// proxies to short-circuit a request destined for THIS visor's own PK
// instead of dialing out over the network back to self (which dmsg does
// not loopback and which is 202-prone). Errors if no handler is
// registered on port. The caller owns the returned conn and must close
// it; the handler goroutine exits when its end closes.
func (r *ServiceRegistry) SelfDial(port uint16) (net.Conn, error) {
	h, ok := r.Get(port)
	if !ok {
		return nil, fmt.Errorf("no local service registered on port %d", port)
	}
	clientEnd, serverEnd := net.Pipe()
	go h(serverEnd)
	return clientEnd, nil
}

// SelfDialAs is SelfDial but stamps the server-end conn's RemoteAddr with
// remotePK (as a dmsg.Addr, which stringifies to "<pk>:<port>") so the
// in-process handler sees a noise-authenticated-equivalent caller identity.
//
// Used by the embedded resolving proxies' self-loopback path: a request for
// THIS visor's own PK is served in-process over net.Pipe (no transport, no
// remote PK), which would otherwise render as anonymous on the landing page.
// Stamping the visor's OWN PK — which is always on its own landing-page
// whitelist — makes the self view authenticated, because local self-access
// through one's own proxy is inherently authorized. Only the SERVER end is
// stamped (the side the handler reads RemoteAddr from); the returned client
// end is unchanged. The port stamped into the addr is informational only —
// the landing-page whitelist check keys off the PK host part.
func (r *ServiceRegistry) SelfDialAs(port uint16, remotePK cipher.PubKey) (net.Conn, error) {
	h, ok := r.Get(port)
	if !ok {
		return nil, fmt.Errorf("no local service registered on port %d", port)
	}
	clientEnd, serverEnd := net.Pipe()
	go h(&pkAddrConn{Conn: serverEnd, remote: dmsg.Addr{PK: remotePK, Port: port}})
	return clientEnd, nil
}

// pkAddrConn overrides RemoteAddr so an in-process self-loopback conn reports a
// dmsg.Addr ("<pk>:<port>") instead of net.Pipe's address-less pipeAddr. The
// HTTP server populates Request.RemoteAddr from RemoteAddr().String(), and the
// logserver landing page extracts the caller PK from it via net.SplitHostPort.
type pkAddrConn struct {
	net.Conn
	remote net.Addr
}

func (c *pkAddrConn) RemoteAddr() net.Addr { return c.remote }

// Ports returns all registered port numbers, sorted ascending.
func (r *ServiceRegistry) Ports() []uint16 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]uint16, 0, len(r.handlers))
	for p := range r.handlers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Services returns a snapshot of all registered (port, label) pairs.
type ServiceInfo struct {
	Port  uint16 `json:"port"`
	Label string `json:"label"`
}

// List returns all registered services as a sorted snapshot.
func (r *ServiceRegistry) List() []ServiceInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ServiceInfo, 0, len(r.handlers))
	for p, svc := range r.handlers {
		out = append(out, ServiceInfo{Port: p, Label: svc.label})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

// ListPublic returns non-hidden services. Satisfies the
// logserver.ServiceLister interface for the /services catalog.
func (r *ServiceRegistry) ListPublic() []logserver.ServiceEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]logserver.ServiceEntry, 0, len(r.handlers))
	for p, svc := range r.handlers {
		if svc.hidden {
			continue
		}
		out = append(out, logserver.ServiceEntry{Port: p, Label: svc.label})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

// HTTPHandler returns a ConnHandler that serves an http.Handler on
// the connection. Each call handles exactly one HTTP connection
// (which may carry multiple requests via keep-alive). This is the
// adapter used by HTTP-based DMSG services (port 80 log server,
// port 81 debug) to register in the handler registry.
func HTTPHandler(h http.Handler) ConnHandler {
	return func(conn net.Conn) {
		defer conn.Close()             //nolint:errcheck,gosec
		srv := http.Server{Handler: h} //nolint:gosec
		// Serve on a single-connection listener that yields conn
		// exactly once then blocks forever (the server closes when
		// the connection is done).
		_ = srv.Serve(&singleConnListener{conn: conn}) //nolint:errcheck,gosec
	}
}

// singleConnListener is a net.Listener that yields exactly one
// connection then blocks until Close is called. Used to feed a
// single skynet/route connection into http.Server.Serve.
type singleConnListener struct {
	conn net.Conn
	once sync.Once
	done chan struct{}
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	var conn net.Conn
	l.once.Do(func() {
		conn = l.conn
		l.done = make(chan struct{})
	})
	if conn != nil {
		return conn, nil
	}
	// Block until Close — the http.Server will call Close when the
	// connection handler returns, which breaks this Accept.
	if l.done != nil {
		<-l.done
	}
	return nil, fmt.Errorf("listener closed")
}

func (l *singleConnListener) Close() error {
	if l.done != nil {
		select {
		case <-l.done:
		default:
			close(l.done)
		}
	}
	return nil
}

func (l *singleConnListener) Addr() net.Addr {
	if l.conn != nil {
		return l.conn.LocalAddr()
	}
	return nil
}
