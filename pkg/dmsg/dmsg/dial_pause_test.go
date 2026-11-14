// Package dmsg pkg/dmsg/dmsg/dial_pause_test.go c1-net-dmsg
package dmsg

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

func TestWaitDialAllowed(t *testing.T) {
	old := dialPausePoll
	dialPausePoll = 5 * time.Millisecond
	t.Cleanup(func() { dialPausePoll = old })

	ce := &Client{done: make(chan struct{})}
	require.True(t, ce.waitDialAllowed(), "not paused: dial at once")

	ce.SetDialPaused(true)
	require.True(t, ce.DialPaused())
	got := make(chan bool, 1)
	go func() { got <- ce.waitDialAllowed() }()
	select {
	case <-got:
		t.Fatal("a paused client must not dial")
	case <-time.After(30 * time.Millisecond):
	}
	ce.SetDialPaused(false)
	select {
	case ok := <-got:
		require.True(t, ok)
	case <-time.After(time.Second):
		t.Fatal("resuming did not release the dial")
	}

	ce.SetDialPaused(true)
	go func() { got <- ce.waitDialAllowed() }()
	close(ce.done)
	select {
	case ok := <-got:
		require.False(t, ok, "a closed client stops waiting and does not dial")
	case <-time.After(time.Second):
		t.Fatal("closing did not release the wait")
	}
}

// A stream dial reaches for a new server session outside the serve loop.
func TestPausedClientRefusesSessionDials(t *testing.T) {
	ce := &Client{done: make(chan struct{})}
	ce.SetDialPaused(true)
	_, err := ce.dialSession(context.Background(), &disc.Entry{})
	require.ErrorIs(t, err, ErrDialPaused)
}

// A dial in flight when the pause begins must not leave its session up.
func TestPauseDropsASessionDialedDuringIt(t *testing.T) {
	dc := disc.NewMock(0)
	srvPK, srvSK := GenKeyPair(t, "pause-server")
	srv := NewServer(srvPK, srvSK, dc, &ServerConfig{MaxSessions: 10}, nil)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	entry := disc.NewServerEntry(srvPK, 0, lis.Addr().String(), 10)
	require.NoError(t, entry.Sign(srvSK))
	require.NoError(t, dc.PostEntry(context.Background(), entry))
	go func() { _ = srv.Serve(lis, lis.Addr().String()) }() //nolint:errcheck
	t.Cleanup(func() { _ = srv.Close() })                   //nolint:errcheck

	pk, sk := GenKeyPair(t, "pause-client")
	c := NewClient(pk, sk, dc, &Config{MinSessions: 1})
	go c.Serve(context.Background())
	t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck
	var ses ClientSession
	require.Eventually(t, func() bool {
		var ok bool
		ses, ok = c.Session(srvPK)
		return ok
	}, 15*time.Second, 50*time.Millisecond)

	c.SetDialPaused(true)
	_, err = c.finishDialedSession(context.Background(), ses, CarrierTCP, lis.Addr().String(), entry)
	require.ErrorIs(t, err, ErrDialPaused)
	require.Eventually(t, func() bool { return c.SessionCount() == 0 }, 5*time.Second, 20*time.Millisecond)
}

func TestRelayPausedRefusesSlots(t *testing.T) {
	ce := &Client{}
	ce.maxRelayedStreams = 8
	peer, _ := cipher.GenerateKeyPair()
	require.True(t, ce.tryAcquireRelaySlot(peer))
	ce.releaseRelaySlot(peer)

	ce.SetRelayPaused(true)
	require.False(t, ce.tryAcquireRelaySlot(peer))
	ce.SetRelayPaused(false)
	require.True(t, ce.tryAcquireRelaySlot(peer))
}
