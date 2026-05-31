// Package network dmsg_test.go: end-to-end coverage for the dmsg
// client/listener/transport adapters using an in-memory dmsg env.
package network

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

func TestDmsgAdapters_EndToEnd(t *testing.T) {
	const port = uint16(80)

	env := dmsgtest.NewEnv(t, dmsgtest.DefaultTimeout)
	require.NoError(t, env.Startup(dmsgtest.DefaultTimeout, 2, 2, &dmsg.Config{MinSessions: 2}))
	t.Cleanup(env.Shutdown)

	dcA := env.AllClients()[0]
	dcB := env.AllClients()[1]

	aClient := newDmsgClient(dcA)
	bClient := newDmsgClient(dcB)

	// Trivial accessors.
	require.Equal(t, types.DMSG, aClient.Type())
	require.Equal(t, dcA.LocalPK(), aClient.PK())
	require.Equal(t, dcA.LocalSK(), aClient.SK())
	require.NoError(t, aClient.Start()) // no-op
	require.NoError(t, aClient.Close()) // no-op (dmsgC owned elsewhere)

	// B listens.
	lis, err := bClient.Listen(port)
	require.NoError(t, err)
	require.Equal(t, dcB.LocalPK(), lis.PK())
	require.Equal(t, port, lis.Port())
	require.Equal(t, types.DMSG, lis.Network())

	// Accept in the background.
	acceptCh := make(chan Transport, 1)
	go func() {
		tp, aerr := lis.AcceptTransport()
		if aerr == nil {
			acceptCh <- tp
		}
	}()

	// A dials B.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dialed, err := aClient.Dial(ctx, dcB.LocalPK(), port)
	require.NoError(t, err)

	var accepted Transport
	select {
	case accepted = <-acceptCh:
	case <-time.After(20 * time.Second):
		t.Fatal("listener did not accept the dialed transport")
	}

	// Transport-adapter accessors on the dialing side.
	require.Equal(t, dcA.LocalPK(), dialed.LocalPK())
	require.Equal(t, dcB.LocalPK(), dialed.RemotePK())
	require.Equal(t, port, dialed.RemotePort())
	require.NotZero(t, dialed.LocalPort())
	require.Equal(t, types.DMSG, dialed.Network())
	require.NotNil(t, dialed.LocalRawAddr())
	require.NotNil(t, dialed.RemoteRawAddr())

	// LocalAddr returns an address now that A has live sessions.
	addr, err := aClient.LocalAddr()
	require.NoError(t, err)
	require.NotNil(t, addr)

	// Data flows across the adapter.
	go func() { _, _ = dialed.Write([]byte("ping")) }()
	buf := make([]byte, 4)
	_ = accepted.SetReadDeadline(time.Now().Add(10 * time.Second))
	n, err := accepted.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "ping", string(buf[:n]))

	require.NoError(t, dialed.Close())
	require.NoError(t, accepted.Close())
	require.NoError(t, lis.Close())
}
