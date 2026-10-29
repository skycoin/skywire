// Package dmsg pkg/dmsg/dmsg/dial_pause_test.go c1-net-dmsg
package dmsg

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
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
