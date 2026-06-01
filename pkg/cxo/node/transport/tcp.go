package transport

import (
	"net"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TCPFactory creates and manages TCP connections. Every connection
// (dial + accept) is Noise_XX-encrypted via wrapNoiseXX using the
// factory's identity, so CXO-TCP is no longer a plaintext transport.
type TCPFactory struct {
	AcceptedCallback func(conn *Connection)

	// localPK / localSK is the node identity used for the Noise XX
	// handshake on every TCP conn.
	localPK cipher.PubKey
	localSK cipher.SecKey

	// MaxPendingAccepts caps in-flight AcceptedCallback invocations.
	// Zero (default) leaves accepts unbounded for backwards compatibility;
	// set it from the Node's MaxPendingConnections to apply backpressure
	// to the listener queue when handshake processing falls behind.
	MaxPendingAccepts int

	mu       sync.Mutex
	listener net.Listener
	closed   bool
	sem      chan struct{} // nil when MaxPendingAccepts == 0
}

// NewTCPFactory creates a new TCPFactory bound to the given identity,
// used for the Noise XX handshake on every TCP connection.
func NewTCPFactory(localPK cipher.PubKey, localSK cipher.SecKey) *TCPFactory {
	return &TCPFactory{localPK: localPK, localSK: localSK}
}

// Listen starts listening on the given address for incoming TCP connections.
func (f *TCPFactory) Listen(address string) error {
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.listener = ln
	if f.MaxPendingAccepts > 0 {
		f.sem = make(chan struct{}, f.MaxPendingAccepts)
	}
	f.mu.Unlock()

	go f.acceptLoop(ln)
	return nil
}

// ListenerAddress returns the actual address the listener is bound to.
// Useful when listening on ":0" to get the OS-assigned port.
func (f *TCPFactory) ListenerAddress() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listener != nil {
		return f.listener.Addr().String()
	}
	return ""
}

// Connect dials a TCP address and returns a Connection.
func (f *TCPFactory) Connect(address string) (*Connection, error) {
	raw, err := net.Dial("tcp", address)
	if err != nil {
		return nil, err
	}
	nc, err := wrapNoiseXX(raw, f.localPK, f.localSK, true)
	if err != nil {
		_ = raw.Close() //nolint:errcheck,gosec
		return nil, err
	}
	return newConnection(nc, true), nil
}

// Close stops the factory and closes the listener.
func (f *TCPFactory) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed {
		return
	}
	f.closed = true
	if f.listener != nil {
		f.listener.Close() //nolint:errcheck,gosec
	}
}

func (f *TCPFactory) acceptLoop(ln net.Listener) {
	// f.sem is set once in Listen before this goroutine starts, so
	// reading it without the lock is safe.
	sem := f.sem
	for {
		// Block before Accept when the in-flight callback queue is full,
		// so the kernel listen backlog absorbs the burst rather than the
		// Node piling up handshake goroutines on a contended mutex.
		if sem != nil {
			sem <- struct{}{}
		}
		conn, err := ln.Accept()
		if err != nil {
			if sem != nil {
				<-sem
			}
			return
		}
		cb := f.AcceptedCallback
		if cb == nil {
			conn.Close() //nolint:errcheck,gosec
			if sem != nil {
				<-sem
			}
			continue
		}
		go func() {
			defer func() {
				if sem != nil {
					<-sem
				}
			}()
			// Noise XX handshake runs here (in the accept goroutine, not
			// the accept loop) so a slow handshake never stalls accepts;
			// the sem bounds concurrent handshakes.
			nc, herr := wrapNoiseXX(conn, f.localPK, f.localSK, false)
			if herr != nil {
				conn.Close() //nolint:errcheck,gosec
				return
			}
			cb(newConnection(nc, true))
		}()
	}
}
