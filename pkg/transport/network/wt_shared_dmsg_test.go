//go:build !tinygo && !(js && wasm)

package network

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/webtransport-go"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// echoWTServer stands in for a dmsg server: it answers the first stream of each
// session with a fixed tag.
type echoWTServer struct{ tag string }

func (e echoWTServer) ServeWTSession(sess *webtransport.Session) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	str, err := sess.AcceptStream(ctx)
	if err != nil {
		return
	}
	_, _ = io.WriteString(str, e.tag) //nolint:errcheck
	_ = str.Close()                   //nolint:errcheck
}

// echoBackWTServer echoes what it reads on the first stream of each session
// until the client closes it, so a test can use one connection over time.
type echoBackWTServer struct{}

func (echoBackWTServer) ServeWTSession(sess *webtransport.Session) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	str, err := sess.AcceptStream(ctx)
	if err != nil {
		return
	}
	_, _ = io.Copy(str, str) //nolint:errcheck
	_ = str.Close()          //nolint:errcheck
}

func hexHash(h [32]byte) string { return hex.EncodeToString(h[:]) }

// sharedSocket is a shared QUIC socket on loopback, closed with the test.
func sharedSocket(t *testing.T) (*sharedQUICMux, net.Addr) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	mux := newSharedQUICMux(conn, logging.MustGetLogger("test_shared_quic"))
	t.Cleanup(func() { _ = mux.Close(); _ = conn.Close() }) //nolint:errcheck
	return mux, conn.LocalAddr()
}

// TestSetDmsgWTServer: a folded dmsg server's WebTransport front and the
// visor's own WT transport share the socket's one "h3" server, each on its own
// path, under one certificate — so a session reaches the right one by path.
func TestSetDmsgWTServer(t *testing.T) {
	mux, sockAddr := sharedSocket(t)

	f := &ClientFactory{sharedQUIC: mux}
	var addr net.Addr
	var hash [32]byte
	ok, err := f.SetDmsgWTServer(echoWTServer{tag: "dmsg"}, func(a net.Addr, h, _ [32]byte) { addr, hash = a, h })
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, sockAddr.String(), addr.String())

	// The visor's transport mounts its path on the same server afterwards.
	wt, err := sharedWebTransport(mux)
	require.NoError(t, err)
	require.Equal(t, hash, wt.cert.Hash(), "one certificate for every path")
	wt.mux.HandleFunc(wtPath, func(w http.ResponseWriter, r *http.Request) {
		sess, err := wt.srv.Upgrade(w, r)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		echoWTServer{tag: "transport"}.ServeWTSession(sess)
	})

	for path, want := range map[string]string{dmsg.WTPath: "dmsg", wtPath: "transport"} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		c, err := wtDial(ctx, fmt.Sprintf("https://%s%s", addr, path), hexHash(hash))
		require.NoError(t, err, path)
		_, err = c.Write([]byte{0}) // a stream is only seen once it carries data
		require.NoError(t, err)
		got, err := io.ReadAll(c)
		require.NoError(t, err, path)
		require.Equal(t, want, string(got), path)
		_ = c.Close() //nolint:errcheck
		cancel()
	}
}

// TestSharedWTCertRotation: when the certificate rotates, the new hashes are
// advertised, new connections get the new certificate, and a connection made
// before the rotation carries on undisturbed. A client that pinned the
// [current, next] pair it learned BEFORE the rotation still connects after it,
// with no fresh lookup; one that pinned only the old certificate is refused.
func TestSharedWTCertRotation(t *testing.T) {
	mux, _ := sharedSocket(t)
	f := &ClientFactory{sharedQUIC: mux}
	var mu sync.Mutex
	var advertised [][2][32]byte
	ok, err := f.SetDmsgWTServer(echoBackWTServer{}, func(_ net.Addr, h, next [32]byte) {
		mu.Lock()
		advertised = append(advertised, [2][32]byte{h, next})
		mu.Unlock()
	})
	require.NoError(t, err)
	require.True(t, ok)
	wt, err := sharedWebTransport(mux)
	require.NoError(t, err)
	url := fmt.Sprintf("https://%s%s", mux.localAddr(), dmsg.WTPath)
	oldHash, oldNext := wt.cert.Hash(), wt.cert.NextHash()
	require.NotEqual(t, oldHash, oldNext)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	before, err := wtDial(ctx, url, hexHash(oldHash), hexHash(oldNext))
	require.NoError(t, err)
	defer before.Close() //nolint:errcheck
	roundTrip := func(c net.Conn, msg string) {
		_, err := c.Write([]byte(msg))
		require.NoError(t, err)
		got := make([]byte, len(msg))
		_, err = io.ReadFull(c, got)
		require.NoError(t, err)
		require.Equal(t, msg, string(got))
	}
	roundTrip(before, "before rotation")

	require.NoError(t, wt.cert.Rotate())
	newHash, newNext := wt.cert.Hash(), wt.cert.NextHash()
	require.Equal(t, oldNext, newHash, "the advertised next certificate is the one served after the rotation")
	mu.Lock()
	require.Equal(t, [][2][32]byte{{oldHash, oldNext}, {newHash, newNext}}, advertised, "both hashes are advertised")
	mu.Unlock()

	stale, err := wtDial(ctx, url, hexHash(oldHash), hexHash(oldNext))
	require.NoError(t, err, "a client holding the pair from before the rotation still connects")
	defer stale.Close() //nolint:errcheck
	roundTrip(stale, "pinned before rotation")

	_, err = wtDial(ctx, url, hexHash(oldHash))
	require.Error(t, err, "a client pinning only the old certificate is refused")

	roundTrip(before, "still up after rotation")
}
