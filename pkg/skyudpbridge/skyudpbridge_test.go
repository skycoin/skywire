package skyudpbridge

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestFrameRoundTrip(t *testing.T) {
	cases := [][]byte{
		nil,
		{},
		[]byte("a"),
		bytes.Repeat([]byte{0xAB}, 1500),
		bytes.Repeat([]byte{0xCD}, MaxFrameSize),
	}
	for _, payload := range cases {
		var buf bytes.Buffer
		if err := WriteFrame(&buf, payload); err != nil {
			t.Fatalf("WriteFrame(len=%d): %v", len(payload), err)
		}
		out := make([]byte, MaxFrameSize)
		n, err := ReadFrame(&buf, out)
		if err != nil {
			t.Fatalf("ReadFrame(len=%d): %v", len(payload), err)
		}
		if n != len(payload) {
			t.Fatalf("ReadFrame returned n=%d, want %d", n, len(payload))
		}
		if !bytes.Equal(out[:n], payload) {
			t.Fatalf("ReadFrame body mismatch (len=%d)", len(payload))
		}
	}
}

func TestWriteFrameRejectsOversized(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, make([]byte, MaxFrameSize+1)); err == nil {
		t.Fatal("expected error for oversized frame, got nil")
	}
}

func TestReadFrameRejectsBufTooSmall(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, bytes.Repeat([]byte{1}, 100)); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFrame(&buf, make([]byte, 50)); err == nil {
		t.Fatal("expected error when buf < frame, got nil")
	}
}

func TestReadFrameEOF(t *testing.T) {
	if _, err := ReadFrame(bytes.NewReader(nil), make([]byte, 10)); err != io.EOF {
		t.Fatalf("ReadFrame on empty: got %v, want EOF", err)
	}
}

// pipeDialer implements Dialer by handing out one end of an in-memory
// net.Pipe per Dial. The other end is published on accepted so the
// test can pump it through a server.
type pipeDialer struct {
	mu       sync.Mutex
	accepted []net.Conn
}

func (p *pipeDialer) Dial(_ context.Context, _ cipher.PubKey, _ uint16) (net.Conn, error) {
	a, b := net.Pipe()
	p.mu.Lock()
	p.accepted = append(p.accepted, b)
	p.mu.Unlock()
	return a, nil
}

// TestClientFramesDatagramsOntoStream brings up the client side
// against a pipe-backed Dialer and verifies that a single UDP
// datagram surfaces on the peer end as exactly one length-prefixed
// frame with the original payload.
func TestClientFramesDatagramsOntoStream(t *testing.T) {
	pd := &pipeDialer{}
	cfg := ClientConfig{
		ListenUDP:   "127.0.0.1:0",
		Peer:        cipher.PubKey{},
		PeerPort:    9999,
		IdleTimeout: 5 * time.Second,
		DialTimeout: time.Second,
	}

	// Bind a UDP listener up front so we know the port for the test.
	udpAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	probe, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		t.Fatal(err)
	}
	bound := probe.LocalAddr().(*net.UDPAddr)
	_ = probe.Close() //nolint:errcheck,gosec
	cfg.ListenUDP = bound.String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := logrus.New()
	logger.SetLevel(logrus.PanicLevel)
	done := make(chan error, 1)
	go func() { done <- RunClient(ctx, cfg, pd, logger) }()

	// Wait briefly for the UDP listen to land.
	deadline := time.Now().Add(2 * time.Second)
	var sender *net.UDPConn
	for time.Now().Before(deadline) {
		sender, err = net.DialUDP("udp", nil, bound)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial UDP %s: %v", bound, err)
	}
	defer sender.Close() //nolint:errcheck,gosec

	payload := []byte("hello-skyudp")

	// Resend until the client side has bound + dialled the pipe.
	// RunClient opens the UDP socket asynchronously, so the first
	// few datagrams may be dropped before the listener is up.
	var peerConn net.Conn
	for time.Now().Before(deadline) {
		_, _ = sender.Write(payload) //nolint:errcheck,gosec
		pd.mu.Lock()
		if len(pd.accepted) > 0 {
			peerConn = pd.accepted[0]
		}
		pd.mu.Unlock()
		if peerConn != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if peerConn == nil {
		t.Fatal("Dial was never called within deadline")
	}
	defer peerConn.Close() //nolint:errcheck,gosec

	_ = peerConn.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck,gosec
	got := make([]byte, MaxFrameSize)
	n, err := ReadFrame(peerConn, got)
	if err != nil {
		t.Fatalf("ReadFrame on peer side: %v", err)
	}
	if !bytes.Equal(got[:n], payload) {
		t.Fatalf("peer frame = %q, want %q", got[:n], payload)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunClient did not exit after cancel")
	}
}
