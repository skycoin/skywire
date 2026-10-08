package transport

import (
	"bytes"
	"math/rand"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport/network"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// pushMem is a transport whose plaintext stream the test feeds, read the way
// the push workers read a KCP session.
type pushMem struct {
	*memTransport
	mu      sync.Mutex
	data    []byte
	err     error
	notify  func()
	wrote   []byte
	nread   atomic.Int64
	maxRead int
}

func newPushMem() *pushMem { return &pushMem{memTransport: newMemTransport(), maxRead: 1500} }

func (p *pushMem) PushReader() (network.PushReader, bool) { return p, true }

func (p *pushMem) SetNotify(fn func()) {
	p.mu.Lock()
	p.notify = fn
	p.mu.Unlock()
}

func (p *pushMem) ReadPlain(_ []byte, emit func([]byte)) (int, error) {
	p.mu.Lock()
	if len(p.data) == 0 {
		err := p.err
		p.mu.Unlock()
		return 0, err
	}
	n := min(len(p.data), 1+rand.Intn(p.maxRead)) //nolint:gosec
	chunk := append([]byte(nil), p.data[:n]...)
	p.data = p.data[n:]
	p.mu.Unlock()
	p.nread.Add(int64(n))
	emit(chunk)
	return n, nil
}

func (p *pushMem) feed(b []byte) {
	p.mu.Lock()
	p.data = append(p.data, b...)
	fn := p.notify
	p.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (p *pushMem) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return 0, p.err
	}
	p.wrote = append(p.wrote, b...)
	return len(b), nil
}

func (p *pushMem) Close() error {
	p.mu.Lock()
	if p.err == nil {
		p.err = net.ErrClosed
	}
	fn := p.notify
	p.mu.Unlock()
	if fn != nil {
		fn()
	}
	return nil
}

func (p *pushMem) written() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.wrote...)
}

func withPushReads(t *testing.T) {
	t.Helper()
	old := pushReads
	pushReads = true
	t.Cleanup(func() { pushReads = old })
}

func newPushedMT(t *testing.T, readCh chan routing.Packet, onEnd func()) (*ManagedTransport, *pushMem) {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	rpk, _ := cipher.GenerateKeyPair()
	mt := NewManagedTransport(ManagedTransportConfig{
		client:        &fakeClient{pk: pk, sk: sk, typ: tptypes.SUDPH},
		DC:            NewDiscoveryMock(),
		LS:            InMemoryTransportLogStore(),
		RemotePK:      rpk,
		QueueDeletion: func(uuid.UUID) {},
	})
	pm := newPushMem()
	mt.transportMx.Lock()
	mt.setTransport(pm)
	mt.transportMx.Unlock()
	mt.Start(readCh, onEnd)
	return mt, pm
}

func dataPackets(t *testing.T, n int) ([]routing.Packet, []byte) {
	t.Helper()
	var pkts []routing.Packet
	var stream []byte
	for i := 0; i < n; i++ {
		payload := make([]byte, rand.Intn(2000)) //nolint:gosec
		rand.Read(payload)                       //nolint:errcheck,gosec
		p, err := routing.MakeDataPacket(routing.RouteID(i+1), payload)
		if err != nil {
			t.Fatal(err)
		}
		pkts = append(pkts, p)
		stream = append(stream, p...)
	}
	return pkts, stream
}

func expectPackets(t *testing.T, readCh <-chan routing.Packet, want []routing.Packet, slow bool) {
	t.Helper()
	for i := range want {
		select {
		case got := <-readCh:
			if !bytes.Equal(got, want[i]) {
				t.Fatalf("packet %d differs", i)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("got %d of %d packets", i, len(want))
		}
		if slow && i%10 == 0 {
			time.Sleep(time.Millisecond)
		}
	}
}

func TestPushDeliversInOrder(t *testing.T) {
	withPushReads(t)
	readCh := make(chan routing.Packet, 4096)
	mt, pm := newPushedMT(t, readCh, nil)
	defer mt.Close() //nolint:errcheck

	want, stream := dataPackets(t, 1000)
	for rest := stream; len(rest) > 0; {
		n := min(1+rand.Intn(5000), len(rest)) //nolint:gosec
		pm.feed(rest[:n])
		rest = rest[n:]
	}
	expectPackets(t, readCh, want, false)
}

// A full readCh holds the transport back instead of losing packets.
func TestPushBackpressure(t *testing.T) {
	withPushReads(t)
	readCh := make(chan routing.Packet, 1)
	mt, pm := newPushedMT(t, readCh, nil)
	defer mt.Close() //nolint:errcheck

	want, stream := dataPackets(t, 300)
	pm.feed(stream)
	expectPackets(t, readCh, want, true)
}

func TestPushAnswersPing(t *testing.T) {
	withPushReads(t)
	readCh := make(chan routing.Packet, 16)
	mt, pm := newPushedMT(t, readCh, nil)
	defer mt.Close() //nolint:errcheck

	pm.feed(routing.MakeTransportPingPacket(time.Now().UnixNano()))
	pong := routing.MakeTransportPongPacket(0)
	deadline := time.Now().Add(5 * time.Second)
	for {
		w := pm.written()
		if len(w) >= len(pong) && routing.Packet(w).Type() == routing.TransportPongPacket {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no pong written")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPushCloseEndsServing(t *testing.T) {
	withPushReads(t)
	ended := make(chan struct{})
	mt, pm := newPushedMT(t, make(chan routing.Packet, 1), func() { close(ended) })
	_ = pm.Close() //nolint:errcheck
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("onEnd not called after the conn ended")
	}
	done := make(chan struct{})
	go func() { _ = mt.Close(); close(done) }() //nolint:errcheck
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return")
	}
}

// A new conn replaces the old one without ending the transport.
func TestPushConnSwap(t *testing.T) {
	withPushReads(t)
	readCh := make(chan routing.Packet, 64)
	mt, old := newPushedMT(t, readCh, nil)
	defer mt.Close() //nolint:errcheck

	first, s1 := dataPackets(t, 5)
	old.feed(s1)
	expectPackets(t, readCh, first, false)

	newC := newPushMem()
	mt.transportMx.Lock()
	mt.setTransport(newC) // closes old
	mt.transportMx.Unlock()
	second, s2 := dataPackets(t, 5)
	newC.feed(s2)
	expectPackets(t, readCh, second, false)
	if !mt.isServing() {
		t.Fatal("transport ended on a conn swap")
	}
}

// Pushed transports share the workers instead of holding a goroutine each.
func TestPushNoGoroutinePerTransport(t *testing.T) {
	withPushReads(t)
	readCh := make(chan routing.Packet, 4096)
	newPushedMT(t, readCh, nil) // start the pool
	time.Sleep(50 * time.Millisecond)
	before := runtime.NumGoroutine()
	var mts []*ManagedTransport
	for i := 0; i < 100; i++ {
		mt, pm := newPushedMT(t, readCh, nil)
		mts = append(mts, mt)
		_, s := dataPackets(t, 1)
		pm.feed(s)
	}
	time.Sleep(200 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+10 {
		t.Fatalf("goroutines went from %d to %d for 100 transports", before, after)
	}
	for _, mt := range mts {
		_ = mt.Close() //nolint:errcheck
	}
}
