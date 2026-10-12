// Package dmsg pkg/dmsg/dmsg/server_folded_key_test.go c2-dmsg-core
package dmsg

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// A visor that serves dmsg in process connects to another server under one key
// twice, as a client and as its server's peer link. The other server must keep
// both, and reach the client's ports through it. Filed under one key, each
// replaced and closed the other, so the peer link lived 0s and the client's
// ports answered "cannot connect to delegated server".
func TestServer_FoldedKeyKeepsClientAndPeerLink(t *testing.T) {
	dc := disc.NewMock(0)
	ctx := context.Background()
	listen := func() (net.Listener, string) {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		return lis, lis.Addr().String()
	}

	sPK, sSK := GenKeyPair(t, "fold-s")
	sLis, sAddr := listen()
	srvS := NewServer(sPK, sSK, dc, &ServerConfig{MaxSessions: 20}, nil)
	srvS.SetLogger(logging.MustGetLogger("fold-s"))
	sEntry := disc.NewServerEntry(sPK, 0, sAddr, 20)
	require.NoError(t, sEntry.Sign(sSK))
	require.NoError(t, dc.PostEntry(ctx, sEntry))

	// The folded key is a server S learned from discovery, not a configured peer.
	fPK, fSK := GenKeyPair(t, "fold-f")
	srvS.peerSessionsMx.Lock()
	srvS.peerPKs[fPK] = struct{}{}
	srvS.peerSessionsMx.Unlock()
	go func() { _ = srvS.Serve(sLis, sAddr) }() //nolint:errcheck
	t.Cleanup(func() { _ = srvS.Close() })      //nolint:errcheck

	fLis, fAddr := listen()
	confF := DefaultServerConfig()
	confF.Peers = []PeerEntry{{PK: sPK, Addr: sAddr}}
	srvF := NewServer(fPK, fSK, dc, confF, nil)
	srvF.SetLogger(logging.MustGetLogger("fold-f-server"))
	go func() { _ = srvF.Serve(fLis, fAddr) }() //nolint:errcheck
	t.Cleanup(func() { _ = srvF.Close() })      //nolint:errcheck

	cliF := NewClient(fPK, fSK, dc, &Config{MinSessions: 1})
	cliF.SetLogger(logging.MustGetLogger("fold-f-client"))
	t.Cleanup(func() { _ = cliF.Close() }) //nolint:errcheck
	_, err := cliF.EnsureAndObtainSession(ctx, sPK)
	require.NoError(t, err)
	lis, err := cliF.Listen(136)
	require.NoError(t, err)
	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(conn, conn) }() //nolint:errcheck
		}
	}()

	peerLink := func() *SessionCommon {
		srvS.peerSessionsMx.Lock()
		defer srvS.peerSessionsMx.Unlock()
		return srvS.peerSessions[fPK]
	}
	client := func() *SessionCommon {
		srvS.sessionsMx.Lock()
		defer srvS.sessionsMx.Unlock()
		return srvS.sessions[fPK]
	}
	require.Eventually(t, func() bool { return peerLink() != nil && client() != nil },
		15*time.Second, 50*time.Millisecond, "S must hold the folded key's peer link and its client session")
	link, cs := peerLink(), client()
	require.NotSame(t, link, cs, "the client slot must hold the client, not the peer link")
	require.False(t, cs.isPeer, "the client session must not be treated as a peer")

	xPK, xSK := GenKeyPair(t, "fold-x")
	cliX := NewClient(xPK, xSK, dc, &Config{MinSessions: 1})
	cliX.SetLogger(logging.MustGetLogger("fold-x"))
	t.Cleanup(func() { _ = cliX.Close() }) //nolint:errcheck
	_, err = cliX.EnsureAndObtainSession(ctx, sPK)
	require.NoError(t, err)
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	str, err := cliX.DialStreamVia(dialCtx, sPK, Addr{PK: fPK, Port: 136})
	require.NoError(t, err, "the folded key's port must be reachable through S")
	_, err = str.Write([]byte("ping"))
	require.NoError(t, err)
	buf := make([]byte, 4)
	_, err = io.ReadFull(str, buf)
	require.NoError(t, err)
	require.Equal(t, "ping", string(buf))
	require.NoError(t, str.Close())

	time.Sleep(3 * time.Second)
	require.Same(t, link, peerLink(), "the peer link must not have been replaced")
	require.Same(t, cs, client(), "the client session must not have been replaced")
}
