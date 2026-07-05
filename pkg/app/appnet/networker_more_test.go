// Package appnet pkg/app/appnet/networker_more_test.go: unit tests for the
// dmsg networker dial/listen wrappers, the top-level PingContextWith* helper
// branches that require a *SkywireNetworker, and the SkywireNetworker
// dial/ping early error (canceled context) paths.
package appnet

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

func testDmsgClient() *dmsg.Client {
	pk, sk := cipher.GenerateKeyPair()
	// Discovery points at an unroutable address so Dial fails fast.
	dc := disc.NewHTTP("http://127.0.0.1:1", &http.Client{Timeout: time.Second}, logging.MustGetLogger("disc"))
	return dmsg.NewClient(pk, sk, dc, nil)
}

func TestDmsgNetworker_Listen(t *testing.T) {
	n := NewDMSGNetworker(testDmsgClient())
	pk, _ := cipher.GenerateKeyPair()
	addr := Addr{Net: TypeDmsg, PubKey: pk, Port: 8123}

	lis, err := n.Listen(addr)
	require.NoError(t, err)
	require.NotNil(t, lis)
	require.NoError(t, lis.Close())

	// ListenContext is the underlying call path too.
	lis2, err := n.ListenContext(context.Background(), addr)
	require.NoError(t, err)
	require.NoError(t, lis2.Close())
}

func TestDmsgNetworker_DialFails(t *testing.T) {
	n := NewDMSGNetworker(testDmsgClient())
	pk, _ := cipher.GenerateKeyPair()
	addr := Addr{Net: TypeDmsg, PubKey: pk, Port: 80}

	// No sessions / unroutable discovery → Dial errors rather than hanging.
	_, err := n.Dial(addr)
	require.Error(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = n.DialContext(ctx, addr)
	require.Error(t, err)
}

func TestSkywireNetworker_DialCanceledCtx(t *testing.T) {
	r := testNetworker(t)
	pk, _ := cipher.GenerateKeyPair()
	addr := Addr{Net: TypeSkynet, PubKey: pk, Port: 5}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // ReserveEphemeral returns ctx.Err() before the router is touched

	_, err := r.DialContext(ctx, addr)
	require.Error(t, err)

	_, err = r.PingContext(ctx, pk, addr)
	require.Error(t, err)
}

func TestPingContextWithTransport_SkywireBranch(t *testing.T) {
	ClearNetworkers()
	defer ClearNetworkers()

	r := testNetworker(t)
	pk, _ := cipher.GenerateKeyPair()
	addr := Addr{Net: TypeSkynet, PubKey: pk, Port: 5}
	require.NoError(t, AddNetworker(addr.Net, r))

	// Non-empty but invalid transport ID → uuid.Parse error (before any
	// router interaction).
	_, err := PingContextWithTransport(context.Background(), pk, addr, "not-a-uuid")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid transport ID")
}

func TestPingContextWithRoute_SkywireBranches(t *testing.T) {
	ClearNetworkers()
	defer ClearNetworkers()

	r := testNetworker(t)
	pk, _ := cipher.GenerateKeyPair()
	addr := Addr{Net: TypeSkynet, PubKey: pk, Port: 5}
	require.NoError(t, AddNetworker(addr.Net, r))
	ctx := context.Background()

	validUUID := uuid.New().String()
	validPK := pk.Hex()

	t.Run("invalid forward hop transport id", func(t *testing.T) {
		fwd := []RouteHopInfo{{TpID: "bad"}}
		rev := []RouteHopInfo{{TpID: validUUID, From: validPK, To: validPK}}
		_, err := PingContextWithRoute(ctx, pk, addr, fwd, rev)
		require.Error(t, err)
		require.Contains(t, err.Error(), "forward hop")
	})

	t.Run("invalid forward hop from PK", func(t *testing.T) {
		fwd := []RouteHopInfo{{TpID: validUUID, From: "bad", To: validPK}}
		rev := []RouteHopInfo{{TpID: validUUID, From: validPK, To: validPK}}
		_, err := PingContextWithRoute(ctx, pk, addr, fwd, rev)
		require.Error(t, err)
	})

	t.Run("invalid reverse hop transport id", func(t *testing.T) {
		// Forward hops fully valid so conversion advances to the reverse loop.
		fwd := []RouteHopInfo{{TpID: validUUID, From: validPK, To: validPK}}
		rev := []RouteHopInfo{{TpID: "bad", From: validPK, To: validPK}}
		_, err := PingContextWithRoute(ctx, pk, addr, fwd, rev)
		require.Error(t, err)
		require.Contains(t, err.Error(), "reverse hop")
	})
}
