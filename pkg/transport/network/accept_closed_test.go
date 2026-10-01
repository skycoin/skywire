//go:build !tinygo

// Package network pkg/transport/network/accept_closed_test.go c2-net-transport
package network

import (
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/soheilhy/cmux"
	"github.com/stretchr/testify/require"

	types "github.com/skycoin/skywire/pkg/transport/types"
)

// TestAcceptLoopStopsWhenTheSharedPortCloses is G4's crash, minus the phone.
// A visor shutdown whose transport manager overran its close timeout closed
// the shared transport port (cmux) while the stcpr client was still open.
// Every Accept then failed at once with "mux: server closed", and the loop,
// which knew only the standard library's closed errors and only on a closed
// client, retried in a busy loop for good. It must return instead.
func TestAcceptLoopStopsWhenTheSharedPortCloses(t *testing.T) {
	master, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	mux := cmux.New(master)
	stcpr := mux.Match(cmux.Any())
	go mux.Serve() //nolint:errcheck

	c := newTestGenericClient(t, types.STCPR)
	stopped := make(chan struct{})
	go func() {
		c.acceptTransports(stcpr)
		close(stopped)
	}()
	<-c.listenStarted

	mux.Close() // the shared port goes first; the client stays open
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("the accept loop kept retrying a closed listener")
	}
	require.False(t, c.isClosed(), "the client was not closed; the listener under it was")
}

// failingListener fails every Accept with an error that is not a close.
type failingListener struct{ calls atomic.Int64 }

func (l *failingListener) Accept() (net.Conn, error) {
	l.calls.Add(1)
	return nil, errors.New("accept: too many open files")
}
func (l *failingListener) Close() error   { return nil }
func (l *failingListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }

// TestAcceptLoopBacksOffOnALastingError: an error that may pass is retried,
// but after a growing pause, so a lasting one costs a few tries, not the
// millions a second the loop used to make (and log).
func TestAcceptLoopBacksOffOnALastingError(t *testing.T) {
	lis := &failingListener{}
	c := newTestGenericClient(t, types.STCPR)
	stopped := make(chan struct{})
	go func() {
		c.acceptTransports(lis)
		close(stopped)
	}()
	<-c.listenStarted
	time.Sleep(200 * time.Millisecond)
	require.NoError(t, c.Close())
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("closing the client did not end the accept loop while it backed off")
	}
	// 5, 10, 20, 40, 80 ms of pauses fit in 200 ms: about six tries.
	require.LessOrEqual(t, lis.calls.Load(), int64(10), "Accept was retried without a pause")
}

func TestAcceptRetryPauseGrowsToASecond(t *testing.T) {
	var pause time.Duration
	var seen []time.Duration
	for range 10 {
		pause = acceptRetryPause(pause)
		seen = append(seen, pause)
	}
	require.Equal(t, 5*time.Millisecond, seen[0])
	require.Equal(t, 10*time.Millisecond, seen[1])
	require.Equal(t, time.Second, seen[9])
}
