package visor

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// callerConn is the host's end of a skynet stream from origin.
type callerConn struct {
	net.Conn
	origin cipher.PubKey
}

func (c callerConn) RemotePK() cipher.PubKey { return c.origin }

// askHost speaks the skynet port request to handleServerConn and returns the
// caller's end and the host's reply.
func askHost(t *testing.T, v *Visor, origin cipher.PubKey, msg clientMsg) (net.Conn, serverReply) {
	t.Helper()
	caller, host := net.Pipe()
	t.Cleanup(func() { _ = caller.Close() }) //nolint:errcheck
	go handleServerConn(logging.MustGetLogger("hosted-test"), callerConn{Conn: host, origin: origin}, v)
	ready := make([]byte, 1)
	_, err := caller.Read(ready)
	require.NoError(t, err)
	req, err := json.Marshal(msg)
	require.NoError(t, err)
	_, err = caller.Write(req)
	require.NoError(t, err)
	buf := make([]byte, 1024)
	n, err := caller.Read(buf)
	require.NoError(t, err)
	var reply serverReply
	require.NoError(t, json.Unmarshal(buf[:n], &reply))
	return caller, reply
}

// TestHandOffHosted: a skynet stream naming a service this visor hosts reaches
// that service's dmsg listener, attributed to the caller.
func TestHandOffHosted(t *testing.T) {
	_, sk := cipher.GenerateKeyPair()
	common, err := visorconfig.NewCommon(nil, filepath.Join(t.TempDir(), "skywire-config.json"), &sk)
	require.NoError(t, err)
	v := &Visor{conf: &visorconfig.V1{Common: common}}

	svcPK, svcSK := cipher.GenerateKeyPair()
	svc := dmsg.NewClient(svcPK, svcSK, disc.NewMock(0), nil)
	t.Cleanup(func() { _ = svc.Close() }) //nolint:errcheck
	lis, err := svc.Listen(80)
	require.NoError(t, err)
	v.hostedKeys.Store(svcPK, struct{}{})
	origin, _ := cipher.GenerateKeyPair()

	caller, reply := askHost(t, v, origin, clientMsg{Port: 80, PK: svcPK.String()})
	require.Nil(t, reply.Error)
	require.Equal(t, svcPK.String(), reply.PK, "the host echoes the service it handed the stream to")

	conn, err := lis.Accept()
	require.NoError(t, err)
	require.Equal(t, dmsg.Addr{PK: origin}, conn.RemoteAddr())
	go func() { _, _ = caller.Write([]byte("GET")) }() //nolint:errcheck
	buf := make([]byte, 3)
	_, err = conn.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "GET", string(buf))

	other, _ := cipher.GenerateKeyPair()
	_, reply = askHost(t, v, origin, clientMsg{Port: 80, PK: other.String()})
	require.NotNil(t, reply.Error, "a key this visor does not host is refused")
	require.Empty(t, reply.PK)

	_, reply = askHost(t, v, origin, clientMsg{Port: 81, PK: svcPK.String()})
	require.NotNil(t, reply.Error, "a port the service does not listen on is refused")
}
