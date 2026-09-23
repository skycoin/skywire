// Package wisp pkg/wisp/harness_test.go c4-app-proxy
//
// The far end a test acts as. It lives apart from server_test.go, and without
// a build tag, because the transport-agnostic tests need it on js/wasm too —
// where httptest and websocket.Accept do not exist, so those tests cannot.
package wisp

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

// fakeEgress hands out in-memory sockets so a test can act as the far end.
type fakeEgress struct {
	mu       sync.Mutex
	tcpPeers []net.Conn
	udpPeers []*fakeDatagram
	udpErr   error
	lastHost string
	lastPort uint16
}

func (f *fakeEgress) DialTCP(_ context.Context, host string, port uint16) (net.Conn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastHost, f.lastPort = host, port
	mine, theirs := net.Pipe()
	f.tcpPeers = append(f.tcpPeers, theirs)
	return mine, nil
}

func (f *fakeEgress) DialUDP(_ context.Context, host string, port uint16) (DatagramStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastHost, f.lastPort = host, port
	if f.udpErr != nil {
		return nil, f.udpErr
	}
	d := &fakeDatagram{out: make(chan []byte, 8), in: make(chan []byte, 8), host: host}
	f.udpPeers = append(f.udpPeers, d)
	return d, nil
}

func (f *fakeEgress) Describe() string { return "fake" }

func (f *fakeEgress) tcpPeer(t *testing.T, i int) net.Conn {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		if len(f.tcpPeers) > i {
			c := f.tcpPeers[i]
			f.mu.Unlock()
			return c
		}
		f.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no TCP peer %d after 2s", i)
	return nil
}

func (f *fakeEgress) udpPeer(t *testing.T) *fakeDatagram {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		if len(f.udpPeers) > 0 {
			d := f.udpPeers[0]
			f.mu.Unlock()
			return d
		}
		f.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no UDP peer after 2s")
	return nil
}

// udpPeerTo is udpPeer by destination rather than by dial order: streams to
// different destinations open from independent goroutines, so their order in
// udpPeers is a race (the CI runner opened the second destination first).
func (f *fakeEgress) udpPeerTo(t *testing.T, host string) *fakeDatagram {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		for _, d := range f.udpPeers {
			if d.host == host {
				f.mu.Unlock()
				return d
			}
		}
		f.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no UDP peer to %s after 2s", host)
	return nil
}

// fakeDatagram is a DatagramStream whose two directions are channels.
type fakeDatagram struct {
	out       chan []byte // written by the server, read by the test
	in        chan []byte // written by the test, read by the server
	host      string
	closeOnce sync.Once
	closed    chan struct{}
	initOnce  sync.Once
}

func (d *fakeDatagram) ensure() {
	d.initOnce.Do(func() { d.closed = make(chan struct{}) })
}

func (d *fakeDatagram) WriteDatagram(b []byte) error {
	d.ensure()
	cp := make([]byte, len(b))
	copy(cp, b)
	select {
	case d.out <- cp:
		return nil
	case <-d.closed:
		return net.ErrClosed
	}
}

func (d *fakeDatagram) ReadDatagram() ([]byte, error) {
	d.ensure()
	select {
	case b := <-d.in:
		return b, nil
	case <-d.closed:
		return nil, net.ErrClosed
	}
}

func (d *fakeDatagram) Close() error {
	d.ensure()
	d.closeOnce.Do(func() { close(d.closed) })
	return nil
}
