// Package dmsg pkg/dmsg/dmsg/relay_inproc_test.go
package dmsg

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A guest attached in-process holds exactly one session — to its host, over an
// in-memory pipe — and the host sees the GUEST's own key. That identity is the
// whole point: the resolving proxy answers under a key the survey whitelist
// knows, while the host pays for dmsg once.
func TestAttachInProcess(t *testing.T) {
	host, hostPK := newLocalRelayTestClient(t, "inproc-host", false, DefaultClientMaxRelayedStreams)
	defer host.Close() //nolint:errcheck

	guest, guestPK := newLocalRelayTestClient(t, "inproc-guest", true, 0)
	defer guest.Close() //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	require.NoError(t, AttachInProcess(ctx, host, guest, 70, nil))

	go guest.Serve(ctx)

	select {
	case <-guest.Ready():
	case <-ctx.Done():
		t.Fatal("in-process guest never became ready")
	}

	sessions := guest.AllSessions()
	require.Len(t, sessions, 1, "a guest holds exactly one session: its host")
	require.Equal(t, hostPK, sessions[0].RemotePK())
	require.Equal(t, CarrierSkynet, sessions[0].Carrier())

	require.Eventually(t, func() bool {
		for _, pk := range host.RelaySessions() {
			if pk == guestPK {
				return true
			}
		}
		return false
	}, 10*time.Second, 50*time.Millisecond,
		"the host must see the guest under the guest's OWN key")
}

func TestAttachInProcessRejects(t *testing.T) {
	host, hostPK := newLocalRelayTestClient(t, "inproc-host-2", false, DefaultClientMaxRelayedStreams)
	defer host.Close() //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	t.Run("nil client", func(t *testing.T) {
		require.Error(t, AttachInProcess(ctx, host, nil, 70, nil))
	})

	// Two dmsg clients on one key evict each other from every shared server,
	// so a guest that is really its host is named at startup, not debugged.
	t.Run("guest shares the host key", func(t *testing.T) {
		err := AttachInProcess(ctx, host, host, 70, nil)
		require.ErrorContains(t, err, "shares the host's key")
	})

	// The dialer refuses anything that is not this host over skynet, so a
	// guest whose relay peers were repointed fails loudly.
	t.Run("dialer refuses another carrier", func(t *testing.T) {
		d := host.InProcessRelayDialer(ctx, nil)
		_, err := d(ctx, "tcp", SkynetAddr(hostPK, 70))
		require.ErrorContains(t, err, "unsupported carrier")
	})

	t.Run("dialer refuses another host", func(t *testing.T) {
		other, otherPK := newLocalRelayTestClient(t, "inproc-other", true, 0)
		defer other.Close() //nolint:errcheck
		d := host.InProcessRelayDialer(ctx, nil)
		_, err := d(ctx, CarrierSkynet, SkynetAddr(otherPK, 70))
		require.ErrorContains(t, err, "host is")
	})
}
