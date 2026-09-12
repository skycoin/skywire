package dmsg

import (
	"bytes"
	"context"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

func TestLocalRelayHelloRoundTrip(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	var buf bytes.Buffer
	require.NoError(t, writeLocalRelayHello(&buf, pk, 70))
	require.Equal(t, localRelayHelloLen, buf.Len())

	gotPK, gotPort, err := readLocalRelayHello(&buf)
	require.NoError(t, err)
	require.Equal(t, pk, gotPK)
	require.Equal(t, uint16(70), gotPort)
}

func TestLocalRelayHelloRejects(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	good := new(bytes.Buffer)
	require.NoError(t, writeLocalRelayHello(good, pk, 70))

	t.Run("bad magic", func(t *testing.T) {
		b := append([]byte(nil), good.Bytes()...)
		b[0] = 'X'
		_, _, err := readLocalRelayHello(bytes.NewReader(b))
		require.ErrorIs(t, err, ErrLocalRelayHello)
	})
	t.Run("bad version", func(t *testing.T) {
		b := append([]byte(nil), good.Bytes()...)
		b[len(localRelayMagic)] = 99
		_, _, err := readLocalRelayHello(bytes.NewReader(b))
		require.ErrorIs(t, err, ErrLocalRelayHello)
	})
	t.Run("short", func(t *testing.T) {
		_, _, err := readLocalRelayHello(bytes.NewReader(good.Bytes()[:4]))
		require.ErrorIs(t, err, ErrLocalRelayHello)
	})
	t.Run("null key", func(t *testing.T) {
		var null bytes.Buffer
		require.NoError(t, writeLocalRelayHello(&null, cipher.PubKey{}, 70))
		_, _, err := readLocalRelayHello(&null)
		require.ErrorIs(t, err, ErrLocalRelayHello)
	})
}

// newLocalRelayTestClient makes a client whose discovery lists NO servers —
// the shape a standalone attached process runs with: nothing to register,
// nowhere else to go, only the relay.
func newLocalRelayTestClient(t *testing.T, name string, relayOnly bool, maxRelayed int) (*Client, cipher.PubKey) {
	t.Helper()
	pk, sk := GenKeyPair(t, name)
	log := logging.MustGetLogger(name)
	c := NewClient(pk, sk, entryOnlyDisc{disc.NewMock(0)}, &Config{
		MinSessions:       1,
		RelayOnly:         relayOnly,
		NoRegister:        true,
		MaxRelayedStreams: maxRelayed,
	})
	c.SetLogger(log)
	return c, pk
}

// TestAttachLocalRelay is the whole feature end to end over a real unix
// socket: an acceptor client serving ServeLocalRelay, and a second client that
// attaches to it with AttachLocalRelay and ends up holding exactly one
// session — to the acceptor, on the skynet carrier — under its OWN key.
func TestAttachLocalRelay(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "relay.sock")
	lis, err := net.Listen("unix", sock)
	require.NoError(t, err)

	relay, relayPK := newLocalRelayTestClient(t, "relay-acceptor", false, DefaultClientMaxRelayedStreams)
	defer relay.Close() //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	go func() {
		_ = relay.ServeLocalRelay(ctx, lis, 70, nil) //nolint:errcheck
	}()

	attached, attachedPK := newLocalRelayTestClient(t, "attached-client", true, 0)
	defer attached.Close() //nolint:errcheck

	gotPK, err := attached.AttachLocalRelay(ctx, "unix", sock)
	require.NoError(t, err)
	require.Equal(t, relayPK, gotPK, "the hello must name the acceptor")

	go attached.Serve(ctx)

	select {
	case <-attached.Ready():
	case <-ctx.Done():
		t.Fatal("attached client never became ready")
	}

	// Exactly one session, and it is the relay: this is what RelayOnly buys —
	// no server sessions held, nothing published.
	sessions := attached.AllSessions()
	require.Len(t, sessions, 1)
	require.Equal(t, relayPK, sessions[0].RemotePK())
	require.Equal(t, CarrierSkynet, sessions[0].Carrier())

	// The identity did NOT rotate: the acceptor sees the attaching client's
	// own key, which is the entire point of attaching rather than proxying.
	require.Eventually(t, func() bool {
		for _, pk := range relay.RelaySessions() {
			if pk == attachedPK {
				return true
			}
		}
		return false
	}, 10*time.Second, 50*time.Millisecond)
}

// TestAttachLocalRelayRefused checks the allow callback is honored on the
// local pipe exactly as it is on the skynet one: a key the acceptor refuses
// gets its conn closed after the handshake, and never becomes a relay session.
func TestAttachLocalRelayRefused(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "relay.sock")
	lis, err := net.Listen("unix", sock)
	require.NoError(t, err)

	relay, relayPK := newLocalRelayTestClient(t, "relay-acceptor", false, DefaultClientMaxRelayedStreams)
	defer relay.Close() //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	go func() {
		_ = relay.ServeLocalRelay(ctx, lis, 70, func(cipher.PubKey) bool { return false }) //nolint:errcheck
	}()

	attached, _ := newLocalRelayTestClient(t, "attached-client", true, 0)
	defer attached.Close() //nolint:errcheck

	// The probe still succeeds: the hello is written before the handshake, so
	// the acceptor's identity is readable by anyone who can open the socket.
	// Admission is decided later, on proof of key.
	gotPK, err := attached.AttachLocalRelay(ctx, "unix", sock)
	require.NoError(t, err)
	require.Equal(t, relayPK, gotPK)

	go attached.Serve(ctx)

	require.Never(t, func() bool {
		return len(relay.RelaySessions()) > 0
	}, 3*time.Second, 100*time.Millisecond)
}

func TestLocalRelayDialerWrongPeer(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "relay.sock")
	lis, err := net.Listen("unix", sock)
	require.NoError(t, err)

	relay, _ := newLocalRelayTestClient(t, "relay-acceptor", false, DefaultClientMaxRelayedStreams)
	defer relay.Close() //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() {
		_ = relay.ServeLocalRelay(ctx, lis, 70, nil) //nolint:errcheck
	}()

	// A socket that now fronts a different visor than the one nominated must
	// fail loudly rather than hand the conn to a Noise handshake keyed to the
	// wrong peer, which would hang until the handshake timeout.
	other, _ := cipher.GenerateKeyPair()
	d := LocalRelayDialer{Network: "unix", Addr: sock}
	_, err = d.SessionDialer()(ctx, CarrierSkynet, SkynetAddr(other, 70))
	require.Error(t, err)
	require.Contains(t, err.Error(), "expected")
}

func TestLocalRelaySessionDialerRejectsOtherCarriers(t *testing.T) {
	d := LocalRelayDialer{Network: "unix", Addr: "/nonexistent.sock"}
	_, err := d.SessionDialer()(context.Background(), CarrierTCP, "1.2.3.4:80")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported carrier")
}

// TestAttachedClientRestsInsteadOfHuntingServers is the regression for the
// serve loop treating a SUCCESSFUL attach as "nothing to connect to".
//
// relayEntries() lists nominees that still NEED a session, so an attached
// client's list is empty precisely because its one nominee is connected; its
// discovery is empty by design. The loop read both emptinesses together as a
// failure and warned + backed off on a client that was up and serving. The
// symptom in production was `dmsg web --attach` logging "No entries found"
// every few seconds forever while it answered requests correctly.
//
// The assertion is behavioral rather than log-scraping: once ready, the client
// must SETTLE — the same single relay session, still on the skynet carrier,
// across several backoff intervals. A client stuck in the old path re-entered
// discovery on every pass, so it could not be relied on to hold still.
func TestAttachedClientRestsInsteadOfHuntingServers(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "relay.sock")
	lis, err := net.Listen("unix", sock)
	require.NoError(t, err)

	relay, _ := newLocalRelayTestClient(t, "rest-acceptor", false, DefaultClientMaxRelayedStreams)
	defer relay.Close() //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() {
		_ = relay.ServeLocalRelay(ctx, lis, 70, nil) //nolint:errcheck
	}()

	attached, _ := newLocalRelayTestClient(t, "rest-client", true, 0)
	// A logger private to this client: the package-level logger is shared with
	// every other client in the test binary, and redirecting IT races with their
	// serve goroutines.
	logs := &syncBuffer{}
	lr := logrus.New()
	lr.SetOutput(logs)
	lr.SetLevel(logrus.DebugLevel)
	attached.SetLogger(&logging.Logger{FieldLogger: lr})
	defer attached.Close() //nolint:errcheck

	_, err = attached.AttachLocalRelay(ctx, "unix", sock)
	require.NoError(t, err)
	go attached.Serve(ctx)

	select {
	case <-attached.Ready():
	case <-ctx.Done():
		t.Fatal("attached client never became ready")
	}

	// sessionsSatisfied is what the fixed guard consults; it must agree that a
	// relay-only client holding its relay session wants nothing more.
	require.True(t, attached.sessionsSatisfied(),
		"a relay-only client on its relay must be satisfied, else the loop hunts for servers")

	// The symptom itself: a satisfied attached client must stop re-entering
	// discovery. Each pass through the old path emitted this warning and grew
	// the backoff; the fixed path blocks until a session drops.
	time.Sleep(1500 * time.Millisecond)
	require.Zero(t, strings.Count(logs.String(), "No entries found"),
		"a serving attached client must not report \"No entries found\"")
	require.Len(t, attached.AllSessions(), 1, "still exactly one session")
}

// syncBuffer is a logrus output sink the test can read while the serve
// goroutine is still writing to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
