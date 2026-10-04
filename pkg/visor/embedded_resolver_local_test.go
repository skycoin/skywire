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

	served := make(chan struct{}, 1)
	v.publishLocalResolver(4446, "skynet_web", func(conn net.Conn) {
		served <- struct{}{}
		_ = conn.Close() //nolint:errcheck
	})

	// The key is the visor's own PK on the resolver's own SOCKS port, read as a
	// routing port — what an app's dial to its own visor carries.
	addr := appnet.Addr{Net: appnet.TypeSkynet, PubKey: pk, Port: routing.Port(4446)}
	require.True(t, appnet.HasLocalService(addr))
	label, _ := appnet.LocalServiceLabel(addr)
	require.Equal(t, "skynet_web", label)

	v.unpublishLocalResolver(4446)
	require.False(t, appnet.HasLocalService(addr))
}

func TestLocalPublishHooks(t *testing.T) {
	t.Run("unwired resolver publishes nothing", func(t *testing.T) {
		onPublish, cleanup := localPublishHooks(nil, nil, 4446, "skynet_web")
		require.Nil(t, onPublish)
		require.NotNil(t, cleanup)
		cleanup() // must not panic
	})

	t.Run("a port that is not a port publishes nothing", func(t *testing.T) {
		var published int
		publish := func(uint16, string, func(net.Conn)) { published++ }
		for _, port := range []uint{0, 65536, 1 << 20} {
			onPublish, cleanup := localPublishHooks(publish, nil, port, "skynet_web")
			require.Nil(t, onPublish, port)
			cleanup()
		}
		require.Zero(t, published)
	})

	t.Run("publishes and cleans up on the configured port", func(t *testing.T) {
		var gotPort, unpublished uint16
		var gotLabel string
		publish := func(p uint16, label string, _ func(net.Conn)) { gotPort, gotLabel = p, label }
		unpublish := func(p uint16) { unpublished = p }

		onPublish, cleanup := localPublishHooks(publish, unpublish, 4445, "dmsg_web")
		require.NotNil(t, onPublish)
		onPublish(func(net.Conn) {})
		require.Equal(t, uint16(4445), gotPort)
		require.Equal(t, "dmsg_web", gotLabel)

		cleanup()
		require.Equal(t, uint16(4445), unpublished)
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
