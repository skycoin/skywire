package addrresolver

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// udpPair returns a client whose control connection is one end of a pipe and
// whose read loop is running, and the other end, which plays the resolver.
func udpPair(t *testing.T) (*httpClient, net.Conn) {
	t.Helper()
	clientEnd, arEnd := net.Pipe()
	c := &httpClient{log: logging.MustGetLogger("udp-resolve-test"), closed: make(chan struct{})}
	c.sudphArConn = clientEnd
	go c.readSUDPHIntoChan(clientEnd, make(chan RemoteVisor, 4))
	t.Cleanup(func() { close(c.closed); _ = clientEnd.Close(); _ = arEnd.Close() }) //nolint:errcheck
	return c, arEnd
}

func TestResolveUDPAnswered(t *testing.T) {
	c, ar := udpPair(t)
	target, _ := cipher.GenerateKeyPair()
	go func() {
		buf := make([]byte, 4096)
		for i := 0; i < 2; i++ {
			n, err := ar.Read(buf)
			if err != nil {
				return
			}
			req, ok := ParseUDPResolveRequest(buf[:n])
			if !ok {
				return
			}
			reply := UDPResolveReply{Resolved: req.ID}
			if i == 0 {
				reply.Found, reply.Data = true, &VisorData{RemoteAddr: "1.2.3.4:5"}
			}
			b, _ := json.Marshal(reply) //nolint:errcheck
			_, _ = ar.Write(b)          //nolint:errcheck
		}
	}()
	data, err, ok := c.resolveUDP(context.Background(), target)
	require.True(t, ok)
	require.NoError(t, err)
	require.Equal(t, "1.2.3.4:5", data.RemoteAddr)

	_, err, ok = c.resolveUDP(context.Background(), target)
	require.True(t, ok)
	require.ErrorIs(t, err, ErrNoEntry)
}

func TestResolveUDPOldResolverFallsBack(t *testing.T) {
	c, ar := udpPair(t)
	go func() { _, _ = ar.Read(make([]byte, 4096)) }() //nolint:errcheck // reads the probe, never answers
	target, _ := cipher.GenerateKeyPair()
	start := time.Now()
	_, err, ok := c.resolveUDP(context.Background(), target)
	require.NoError(t, err)
	require.False(t, ok, "no answer means HTTP")
	require.Less(t, time.Since(start), udpResolveProbeWait+time.Second)

	start = time.Now()
	_, err, ok = c.resolveUDP(context.Background(), target)
	require.NoError(t, err)
	require.False(t, ok)
	require.Less(t, time.Since(start), 100*time.Millisecond, "the connection is not probed again")
}

func TestReplyIsNotADialRequest(t *testing.T) {
	_, ok := parseUDPResolveReply([]byte(`{"PK":"02","Addr":"1.2.3.4:5"}`))
	require.False(t, ok)
	r, ok := parseUDPResolveReply([]byte(`{"resolved":7,"found":true}`))
	require.True(t, ok)
	require.Equal(t, uint32(7), r.Resolved)
	_, ok = ParseUDPResolveRequest([]byte(`{"port":"30178"}`))
	require.False(t, ok, "an address update is not a lookup")
}
