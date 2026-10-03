//go:build !tinygo && !(js && wasm)

package network

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// A conn upgraded through a hypervisor's /tp/ws is marked attached and one
// upgraded on the transport port is not, and the mark reaches the transport.
func TestWSAttachedMark(t *testing.T) {
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	lis := newWSListenerOver(tcp, nil)
	defer lis.Close() //nolint:errcheck

	hv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lis.accept(w, r, true)
	}))
	defer hv.Close()

	for _, tc := range []struct {
		url      string
		attached bool
	}{
		{"ws://" + tcp.Addr().String() + "/", false},
		{"ws" + strings.TrimPrefix(hv.URL, "http") + "/tp/ws", true},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		ws, _, err := websocket.Dial(ctx, tc.url, nil) //nolint:bodyclose
		if err != nil {
			cancel()
			t.Fatalf("dial %s: %v", tc.url, err)
		}
		conn, err := lis.Accept()
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		tp := &transport{Conn: conn, rawConn: conn}
		if tp.Attached() != tc.attached {
			t.Errorf("%s: Attached() = %v, want %v", tc.url, tp.Attached(), tc.attached)
		}
		_ = conn.Close()                                //nolint:errcheck
		_ = ws.Close(websocket.StatusNormalClosure, "") //nolint:errcheck
		cancel()
	}
}
