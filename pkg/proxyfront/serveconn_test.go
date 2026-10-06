package proxyfront

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ServeConn sends a conn to the right server by its first byte, and gives the
// byte back — a listener-less caller gets the same split Split gives.
func TestServeConnSplitsByFirstByte(t *testing.T) {
	for _, tc := range []struct {
		name  string
		first []byte
		socks bool
	}{
		{name: "socks5 greeting", first: []byte{0x05, 0x01, 0x00}, socks: true},
		{name: "http request", first: []byte("GET / HTTP/1.1\r\n"), socks: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close() //nolint:errcheck

			got := make(chan string, 1)
			firstByte := make(chan byte, 1)
			handler := func(label string) func(net.Conn) {
				return func(c net.Conn) {
					defer c.Close() //nolint:errcheck
					b := make([]byte, 1)
					if _, err := c.Read(b); err == nil {
						firstByte <- b[0]
					}
					got <- label
				}
			}
			go ServeConn(server, handler("socks"), handler("http"))

			require.NoError(t, client.SetDeadline(time.Now().Add(5*time.Second)))
			_, err := client.Write(tc.first)
			require.NoError(t, err)

			want := "http"
			if tc.socks {
				want = "socks"
			}
			select {
			case label := <-got:
				require.Equal(t, want, label)
			case <-time.After(5 * time.Second):
				t.Fatal("neither server was handed the conn")
			}
			// The sniffed byte is still there for the server to read.
			select {
			case b := <-firstByte:
				require.Equal(t, tc.first[0], b)
			case <-time.After(5 * time.Second):
				t.Fatal("the peeked byte was swallowed")
			}
		})
	}
}

// A conn that dies before its first byte is closed rather than handed on.
func TestServeConnClosesAConnThatSaysNothing(t *testing.T) {
	client, server := net.Pipe()
	require.NoError(t, client.Close())

	handled := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		ServeConn(server,
			func(net.Conn) { handled <- struct{}{} },
			func(net.Conn) { handled <- struct{}{} })
		close(done)
	}()

	select {
	case <-done:
	case <-handled:
		t.Fatal("a silent conn was handed to a server")
	case <-time.After(5 * time.Second):
		t.Fatal("ServeConn never returned")
	}
	// The server end was closed on the way out, not leaked open.
	_, err := server.Write([]byte{0x05})
	require.Error(t, err)
}
