package visor

import (
	"io"
	"net"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// A visor's own proxy reaches its forwarded ports. They have no handler in
// the service registry, so a self-dial used to fail with host unreachable.
func TestSelfDialReachesForwardedPort(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck
	go func() {
		c, err := lis.Accept()
		if err != nil {
			return
		}
		defer c.Close()      //nolint:errcheck
		_, _ = io.Copy(c, c) //nolint:errcheck
	}()

	conf, _ := newTestConfig(t)
	v := &Visor{
		conf:           conf,
		log:            logging.MustGetLogger("test"),
		forwardedPorts: NewForwardedPorts(filepath.Join(t.TempDir(), "forwarded_ports.json")),
		services:       NewServiceRegistry(),
	}
	v.services.SetSelfDialFallback(v.selfDialForwarded)
	local := lis.Addr().(*net.TCPAddr).Port
	require.NoError(t, v.forwardedPorts.Register(visorapi.ForwardedPort{Port: 18095, LocalPort: local, DMSG: true}))

	c, err := v.services.SelfDialAs(18095, conf.PK)
	require.NoError(t, err)
	defer c.Close() //nolint:errcheck
	_, err = c.Write([]byte("ping"))
	require.NoError(t, err)
	buf := make([]byte, 4)
	_, err = io.ReadFull(c, buf)
	require.NoError(t, err)
	require.Equal(t, "ping", string(buf))

	_, err = v.services.SelfDial(18096)
	require.Error(t, err, "a port nothing serves still fails")
}
