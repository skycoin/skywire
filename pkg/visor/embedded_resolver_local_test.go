// Package visor pkg/visor/embedded_resolver_local_test.go
package visor

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func TestPublishLocalResolverUsesTheVisorsOwnPK(t *testing.T) {
	t.Cleanup(appnet.ClearLocalServices)

	pk, _ := cipher.GenerateKeyPair()
	v := &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{PK: pk}}, log: logging.MustGetLogger("test")}

	svc := appnet.LocalService{Port: routing.Port(4446), Label: "skynet_web", Suffixes: []string{".skynet"}}
	v.publishLocalResolver(svc, func(conn net.Conn) { _ = conn.Close() }) //nolint:errcheck

	// The key is the visor's own PK on the resolver's own SOCKS port, read as a
	// routing port — what an app's dial to its own visor carries.
	addr := appnet.Addr{Net: appnet.TypeSkynet, PubKey: pk, Port: routing.Port(4446)}
	require.True(t, appnet.HasLocalService(addr))
	got, _ := appnet.LocalServiceFor(addr)
	require.Equal(t, svc, got)
	require.Equal(t, []appnet.LocalService{svc}, appnet.LocalServices(pk))

	v.unpublishLocalResolver(routing.Port(4446))
	require.False(t, appnet.HasLocalService(addr))
	require.Empty(t, appnet.LocalServices(pk))
}

func TestLocalPublishHooks(t *testing.T) {
	t.Run("unwired resolver publishes nothing", func(t *testing.T) {
		onPublish, cleanup := localPublishHooks(nil, nil, 4446, "skynet_web", ".skynet")
		require.Nil(t, onPublish)
		require.NotNil(t, cleanup)
		cleanup() // must not panic
	})

	t.Run("a port that is not a port publishes nothing", func(t *testing.T) {
		var published int
		publish := func(appnet.LocalService, func(net.Conn)) { published++ }
		for _, port := range []uint{0, 65536, 1 << 20} {
			onPublish, cleanup := localPublishHooks(publish, nil, port, "skynet_web", ".skynet")
			require.Nil(t, onPublish, port)
			cleanup()
		}
		require.Zero(t, published)
	})

	t.Run("publishes the configured port and suffix", func(t *testing.T) {
		var got appnet.LocalService
		var unpublished routing.Port
		publish := func(svc appnet.LocalService, _ func(net.Conn)) { got = svc }
		unpublish := func(p routing.Port) { unpublished = p }

		onPublish, cleanup := localPublishHooks(publish, unpublish, 4445, "dmsg_web", ".dmsg")
		require.NotNil(t, onPublish)
		onPublish(func(net.Conn) {})
		require.Equal(t, appnet.LocalService{
			Port: routing.Port(4445), Label: "dmsg_web", Suffixes: []string{".dmsg"},
		}, got)

		cleanup()
		require.Equal(t, routing.Port(4445), unpublished)
	})

	t.Run("no suffix means no name to match", func(t *testing.T) {
		var got appnet.LocalService
		publish := func(svc appnet.LocalService, _ func(net.Conn)) { got = svc }
		onPublish, _ := localPublishHooks(publish, nil, 4445, "dmsg_web", "")
		onPublish(func(net.Conn) {})
		require.Nil(t, got.Suffixes)
	})
}

// Both resolver types take the wiring, so one visor call covers either kind.
func TestWireLocalResolverPublish(t *testing.T) {
	t.Cleanup(appnet.ClearLocalServices)

	pk, _ := cipher.GenerateKeyPair()
	v := &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{PK: pk}}, log: logging.MustGetLogger("test")}

	skynet := &EmbeddedSkynetWeb{}
	v.wireLocalResolverPublish(skynet)
	require.NotNil(t, skynet.publishLocal)
	require.NotNil(t, skynet.unpublishLocal)

	dmsgWeb := &EmbeddedDmsgWeb{}
	v.wireLocalResolverPublish(dmsgWeb)
	require.NotNil(t, dmsgWeb.publishLocal)
	require.NotNil(t, dmsgWeb.unpublishLocal)

	// A nil runtime is a no-op rather than a panic: a resolver whose
	// construction was skipped still reaches this call.
	v.wireLocalResolverPublish(nil)
}
