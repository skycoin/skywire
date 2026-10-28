package addrresolver

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/logging"
)

// After a move the next bind reaches the AR with the new public IP, in the
// body and in the SW-PublicIP header, even while CXO keeps the old one alive.
func TestNetworkMovedRebindsWithNewPublicIP(t *testing.T) {
	type post struct{ body, header string }
	var (
		mu    sync.Mutex
		posts []post
	)
	mux := httputil.NewRouter()
	mux.Post(stcprBindPath, func(w http.ResponseWriter, r *http.Request) {
		var la LocalAddresses
		_ = json.NewDecoder(r.Body).Decode(&la) //nolint:errcheck
		mu.Lock()
		posts = append(posts, post{la.PublicIP, r.Header.Get("SW-PublicIP")})
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(arRouter(mux))
	defer srv.Close()

	c := newReadyClient(t, srv)
	c.SetCXOKeepaliveHealthyFunc(func() (bool, uint64) { return true, 1 })
	ctx := context.Background()

	c.SetPublicIP("203.0.113.5", "")
	require.NoError(t, c.BindSTCPR(ctx, "7777"))
	require.NoError(t, c.BindSTCPR(ctx, "7777"))
	mu.Lock()
	require.Equal(t, []post{{"203.0.113.5", "203.0.113.5"}}, posts, "an unchanged bind kept alive over CXO is skipped")
	mu.Unlock()

	c.SetPublicIP("198.51.100.7", "")
	c.NetworkMoved()
	require.NoError(t, c.BindSTCPR(ctx, "7777"))
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, posts, 2)
	require.Equal(t, post{"198.51.100.7", "198.51.100.7"}, posts[1])
}

// A move drops the SUDPH session, so the reconnect loop dials again and
// sends a payload built from the new public IP.
func TestNetworkMovedDropsSUDPHSession(t *testing.T) {
	local, remote := net.Pipe()
	defer remote.Close() //nolint:errcheck
	c := &httpClient{log: logging.MustGetLogger("ar-move"), closed: make(chan struct{}), ipReady: make(chan struct{})}
	c.sudphArConn = local

	served := make(chan struct{})
	go func() {
		c.serveSUDPHConn(local, make(chan RemoteVisor, 1))
		close(served)
	}()
	go func() { // drain heartbeats so writes do not block
		buf := make([]byte, 512)
		for {
			if _, err := remote.Read(buf); err != nil {
				return
			}
		}
	}()

	c.SetPublicIP("198.51.100.7", "2001:db8::7")
	c.NetworkMoved()
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("the SUDPH session survived a network move")
	}

	la, err := c.sudphPayload("40000")
	require.NoError(t, err)
	require.Equal(t, "198.51.100.7", la.PublicIP)
	require.Equal(t, "2001:db8::7", la.PublicIPv6)
}
