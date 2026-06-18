// Package dmsgtest pkg/dmsgtest/coldstart_test.go
//
//nolint:errcheck,gosec
package dmsgtest

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// TestColdStart_UnregisteredDiscReachableViaSharedServer reproduces the
// dmsg-only cold-start registration path and proves the property the fleet
// recovery depends on:
//
//	dmsg-discovery is NOT discoverable (it never registers itself), and a
//	dmsg-server cannot dial a client. So the ONLY way a server can register
//	itself over dmsg-HTTP is: disc dials OUT to the dmsg-server (the
//	connectConfiguredServers fix), and the server's transit client reaches
//	disc through that shared server via dialViaConnectedServers — with no
//	discovery entry for disc anywhere.
//
// The test wires:
//   - server S (real listener)
//   - "disc": an unregistered client that dials S (stands in for
//     connectConfiguredServers) and listens on its dmsg-HTTP port
//   - "transit": a dmsg-server's transit client that dials S and must reach
//     disc with NO disc entry in its discovery
//
// The positive assertion is that transit→disc succeeds purely via the shared
// server. The negative control is that an unregistered PK NOT connected to any
// shared server is unreachable — proving disc's outbound dial is the necessary
// enabler.
func TestColdStart_UnregisteredDiscReachableViaSharedServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	// --- Server S ---------------------------------------------------------
	sharedDC := disc.NewMock(0)
	pkS, skS := cipher.GenerateKeyPair()
	lisS, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err)
	srvS := dmsg.NewServer(pkS, skS, sharedDC, &dmsg.ServerConfig{
		MaxSessions:    10,
		UpdateInterval: dmsg.DefaultUpdateInterval,
	}, nil)
	go srvS.Serve(lisS, "")
	defer srvS.Close()
	select {
	case <-srvS.Ready():
	case <-ctx.Done():
		t.Fatal("server S not ready")
	}
	// Registration is async — Serve no longer gates Accept on the
	// entry-publish loop (the cold-start fix), so Ready() means "accepting",
	// not "registered". Wait for the entry to land in discovery.
	var entryS *disc.Entry
	require.Eventually(t, func() bool {
		e, eerr := sharedDC.Entry(ctx, pkS)
		if eerr != nil {
			return false
		}
		entryS = e
		return true
	}, 15*time.Second, 200*time.Millisecond, "server S must register in discovery")

	// --- "disc": unregistered client that dials OUT to S ------------------
	// Its discovery knows S (so it can connect to S), but disc never
	// PostEntry's ITSELF anywhere — exactly like dmsg-discovery.
	discDC := disc.NewMock(0)
	require.NoError(t, discDC.PostEntry(ctx, entryS))
	pkDisc, skDisc := cipher.GenerateKeyPair()
	discClient := dmsg.NewClient(pkDisc, skDisc, discDC, &dmsg.Config{MinSessions: 1})
	go discClient.Serve(ctx)
	defer discClient.Close()
	select {
	case <-discClient.Ready():
	case <-ctx.Done():
		t.Fatal("disc client not ready")
	}
	// disc listens on its dmsg-HTTP port and echoes — stands in for the
	// dmsg-discovery setEntry handler the server registers against.
	const discPort = uint16(80)
	lisDisc, err := discClient.Listen(discPort)
	require.NoError(t, err)
	go func() {
		for {
			s, aerr := lisDisc.AcceptStream()
			if aerr != nil {
				return
			}
			go func(st *dmsg.Stream) {
				defer st.Close()
				buf := make([]byte, 4)
				if _, rerr := io.ReadFull(st, buf); rerr != nil {
					return
				}
				st.Write(buf)
			}(s)
		}
	}()

	// --- "transit": a dmsg-server's transit client ------------------------
	// Knows S (to connect to S) but has NO entry for disc. It must reach
	// disc purely via the shared server.
	transitDC := disc.NewMock(0)
	require.NoError(t, transitDC.PostEntry(ctx, entryS))
	pkT, skT := cipher.GenerateKeyPair()
	transitClient := dmsg.NewClient(pkT, skT, transitDC, &dmsg.Config{MinSessions: 1})
	go transitClient.Serve(ctx)
	defer transitClient.Close()
	select {
	case <-transitClient.Ready():
	case <-ctx.Done():
		t.Fatal("transit client not ready")
	}

	// Defining precondition: disc is NOT discoverable by the transit client.
	_, err = transitDC.Entry(ctx, pkDisc)
	require.Error(t, err, "disc must be unregistered in the transit client's discovery")

	// --- Positive: transit reaches unregistered disc via the shared server.
	var stream *dmsg.Stream
	for attempt := 0; attempt < 8; attempt++ {
		stream, err = transitClient.DialStream(ctx, dmsg.Addr{PK: pkDisc, Port: discPort})
		if err == nil {
			break
		}
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			t.Fatal("ctx done while dialing disc")
		}
	}
	require.NoError(t, err, "transit client must reach unregistered disc via the shared server (dialViaConnectedServers)")
	defer stream.Close()

	// Confirm it is a real working stream end-to-end.
	_, err = stream.Write([]byte("ping"))
	require.NoError(t, err)
	got := make([]byte, 4)
	_, err = io.ReadFull(stream, got)
	require.NoError(t, err)
	require.Equal(t, "ping", string(got))

	// --- Negative control: an unregistered PK NOT on any shared server is
	// unreachable. Proves disc's OUTBOUND dial to S is the necessary enabler.
	pkGhost, _ := cipher.GenerateKeyPair()
	nctx, ncancel := context.WithTimeout(ctx, 10*time.Second)
	defer ncancel()
	_, err = transitClient.DialStream(nctx, dmsg.Addr{PK: pkGhost, Port: discPort})
	require.Error(t, err, "an unregistered PK not connected to any shared server must be unreachable")
}
