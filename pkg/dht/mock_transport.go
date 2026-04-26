package dht

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
)

// MockNetwork simulates a DHT network for testing. Each node registers
// itself with a public key, and Dial connects to the target via in-process
// net.Pipe connections.
type MockNetwork struct {
	mu        sync.RWMutex
	listeners map[cipher.PubKey]*mockListener
}

// NewMockNetwork creates a new in-process test network.
func NewMockNetwork() *MockNetwork {
	return &MockNetwork{
		listeners: make(map[cipher.PubKey]*mockListener),
	}
}

// Transport returns a Transport implementation for the given pubkey.
func (mn *MockNetwork) Transport(pk cipher.PubKey) Transport {
	return &mockTransport{net: mn, pk: pk}
}

type mockTransport struct {
	net *MockNetwork
	pk  cipher.PubKey
}

func (t *mockTransport) Dial(ctx context.Context, pk cipher.PubKey) (io.ReadWriteCloser, error) {
	t.net.mu.RLock()
	lis, ok := t.net.listeners[pk]
	t.net.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("mock: no listener for %s", pk.String()[:8])
	}

	// Create a pair of connected pipes.
	clientConn, serverConn := net.Pipe()

	select {
	case lis.incoming <- mockConn{conn: serverConn, remotePK: t.pk}:
		return clientConn, nil
	case <-ctx.Done():
		clientConn.Close() //nolint:errcheck,gosec
		serverConn.Close() //nolint:errcheck,gosec
		return nil, ctx.Err()
	}
}

func (t *mockTransport) Listen() (Listener, error) {
	lis := &mockListener{
		pk:       t.pk,
		incoming: make(chan mockConn, 64),
		done:     make(chan struct{}),
	}
	t.net.mu.Lock()
	t.net.listeners[t.pk] = lis
	t.net.mu.Unlock()
	return lis, nil
}

type mockConn struct {
	conn     net.Conn
	remotePK cipher.PubKey
}

type mockListener struct {
	pk       cipher.PubKey
	incoming chan mockConn
	done     chan struct{}
	once     sync.Once
}

func (l *mockListener) Accept() (io.ReadWriteCloser, cipher.PubKey, error) {
	select {
	case mc := <-l.incoming:
		return mc.conn, mc.remotePK, nil
	case <-l.done:
		return nil, cipher.PubKey{}, fmt.Errorf("mock: listener closed")
	}
}

func (l *mockListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}
