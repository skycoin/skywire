//go:build !tinygo && !(js && wasm)

package network

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
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

// TestSetDmsgWTServer: a folded dmsg server's WebTransport front and the
// visor's own WT transport share the socket's one "h3" server, each on its own
// path, under one certificate — so a session reaches the right one by path.
func TestSetDmsgWTServer(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	mux := newSharedQUICMux(conn, logging.MustGetLogger("test_shared_quic"))
	t.Cleanup(func() { _ = mux.Close(); _ = conn.Close() }) //nolint:errcheck

	f := &ClientFactory{sharedQUIC: mux}
	addr, hash, err := f.SetDmsgWTServer(echoWTServer{tag: "dmsg"})
	require.NoError(t, err)
	require.Equal(t, conn.LocalAddr().String(), addr.String())

	// The visor's transport mounts its path on the same server afterwards.
	wt, err := sharedWebTransport(mux)
	require.NoError(t, err)
	require.Equal(t, hash, wt.certHash, "one certificate for every path")
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
		c, err := wtDial(ctx, fmt.Sprintf("https://%s%s", addr, path), hex.EncodeToString(hash[:]))
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
