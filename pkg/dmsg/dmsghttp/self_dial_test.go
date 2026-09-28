package dmsghttp

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// A request addressed to the transport owner's own key goes to the self
// dialer and never touches dmsg (the client is nil here and would panic).
func TestRoundTripSelfDial(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()

	served := make(chan uint16, 1)
	gotPath := make(chan string, 1)
	tr := MakeHTTPTransport(context.Background(), nil)
	tr.SetSelfDialer(pk, func(port uint16) (net.Conn, error) {
		served <- port
		client, server := net.Pipe()
		go func() {
			srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { //nolint:gosec
				gotPath <- r.URL.Path
				_, _ = io.WriteString(w, "self") //nolint:errcheck
			})}
			_ = srv.Serve(&oneConnListener{c: server}) //nolint:errcheck
		}()
		return client, nil
	})

	c := &http.Client{Transport: tr}
	resp, err := c.Get("http://" + pk.Hex() + ":80/tpd/health")
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, "self", string(body))
	require.Equal(t, "/tpd/health", <-gotPath)
	require.Equal(t, uint16(80), <-served)

	// Another key is not ours: with no dmsg client the round trip must not
	// reach the self dialer.
	require.Panics(t, func() {
		_, _ = c.Get("http://" + other.Hex() + ":80/x") //nolint:errcheck
	})
}

type oneConnListener struct {
	c    net.Conn
	done bool
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	if l.done {
		select {}
	}
	l.done = true
	return l.c, nil
}
func (l *oneConnListener) Close() error   { return nil }
func (l *oneConnListener) Addr() net.Addr { return l.c.LocalAddr() }
