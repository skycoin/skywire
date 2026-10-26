package commands

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skysocks"
)

// fakeResolver answers one SOCKS5 CONNECT and reports which resolver port was
// dialed and the address the client asked for.
func fakeResolver(got chan<- string) func(routing.Port) (net.Conn, error) {
	return func(p routing.Port) (net.Conn, error) {
		c, s := net.Pipe()
		go func() {
			defer s.Close() //nolint:errcheck
			buf := make([]byte, 262)
			if _, err := io.ReadFull(s, buf[:2]); err != nil {
				return
			}
			if _, err := io.ReadFull(s, buf[:buf[1]]); err != nil {
				return
			}
			_, _ = s.Write([]byte{5, 0}) //nolint:errcheck
			if _, err := io.ReadFull(s, buf[:5]); err != nil {
				return
			}
			n := int(buf[4])
			if _, err := io.ReadFull(s, buf[:n+2]); err != nil {
				return
			}
			host := string(buf[:n])
			portNum := binary.BigEndian.Uint16(buf[n : n+2])
			got <- net.JoinHostPort(host, strconv.Itoa(int(portNum))) + "@" + strconv.Itoa(int(p))
			_, _ = s.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80}) //nolint:errcheck
		}()
		return c, nil
	}
}

// TestResolverStatusDial checks that a status host reaches the resolver that
// answers its suffix, or the fallback, as a SOCKS5 CONNECT for host:port.
func TestResolverStatusDial(t *testing.T) {
	got := make(chan string, 1)
	svcs := []appnet.LocalService{
		{Port: 4445, Label: "dmsg_web", Suffixes: []string{".dmsg"}},
		{Port: 4446, Label: "skynet_web", Suffixes: []string{".skynet"}},
	}
	res := skysocks.NewLocalResolvers(svcs, fakeResolver(got))
	dial := resolverStatusDial(res, 4445)
	for host, want := range map[string]string{
		"status.dmsg":    "status.dmsg:80@4445",
		"status.skynet":  "status.skynet:80@4446",
		"status.skywire": "status.skywire:80@4445",
	} {
		c, err := dial(context.Background(), host, 80)
		require.NoError(t, err, host)
		require.Equal(t, want, <-got, host)
		_ = c.Close() //nolint:errcheck
	}
}
